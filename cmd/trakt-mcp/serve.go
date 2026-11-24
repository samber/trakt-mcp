package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/samber/trakt-mcp/internal/config"
	"github.com/samber/trakt-mcp/internal/crypto"
	"github.com/samber/trakt-mcp/internal/jobs"
	"github.com/samber/trakt-mcp/internal/limits"
	"github.com/samber/trakt-mcp/internal/oauth"
	"github.com/samber/trakt-mcp/internal/obs"
	"github.com/samber/trakt-mcp/internal/store"
	"github.com/samber/trakt-mcp/internal/tools"
	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/web"
)

const (
	// readHeaderTimeout bounds slow-header (Slowloris) clients.
	readHeaderTimeout = 10 * time.Second
	// readTimeout bounds reading a whole request, body included, so a client
	// trickling a body cannot hold a connection; every request body here
	// (MCP JSON-RPC, OAuth forms) is a few KiB and arrives in well under a second.
	readTimeout = 30 * time.Second
	// idleTimeout closes idle keep-alive connections that PaaS routers leave open.
	idleTimeout = 120 * time.Second
	// shutdownTimeout drains in-flight requests on SIGINT/SIGTERM; a tool call
	// rarely exceeds a few seconds of Trakt latency.
	shutdownTimeout = 15 * time.Second
)

// runServe runs the HTTP server until ctx is done, then drains requests,
// stops the background jobs and closes the store. Logs go to stdout as JSON.
func runServe(ctx context.Context, cfg config.Config, stdout io.Writer) (err error) {
	log := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(log)

	if cfg.LogLevel <= slog.LevelDebug {
		log.WarnContext(ctx, "debug log level writes personal data (tool arguments and results) to logs")
	}

	kr, err := crypto.ParseKeyring(cfg.EncryptionKeys)
	if err != nil {
		return err
	}

	st, err := store.Open(ctx, cfg.DBPath, kr)
	if err != nil {
		return err
	}
	// Deferred first, so it runs last: after the servers and jobs stopped.
	defer func() { err = errors.Join(err, st.Close()) }()

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	metrics := obs.NewMetrics(reg)

	base := strings.TrimSuffix(cfg.BaseURL.String(), "/")
	up := trakt.NewUpstream(trakt.UpstreamConfig{
		ClientID:     cfg.TraktClientID,
		ClientSecret: cfg.TraktClientSecret,
		RedirectURL:  base + oauth.CallbackPath,
		UserAgent:    userAgent(),
		// Always wired, metrics or not: the transport also records the Trakt
		// status that tool-call log lines report.
		HTTP:   &http.Client{Transport: metrics.Transport(nil)},
		Logger: log,
	}, st)

	osrv, err := oauth.New(cfg, oauth.Deps{
		Store:    st,
		Clients:  oauth.NewClientResolver(st, oauth.NewCIMDFetcher(oauth.CIMDOptions{})),
		Upstream: up,
		Keyring:  kr,
		Logger:   log,
		Metrics:  metrics,
	})
	if err != nil {
		return err
	}

	lim := limits.New(cfg.RateLimitReads, cfg.RateLimitWrites, limits.WithGlobal(cfg.RateLimitGlobal))
	mcpHandler := tools.NewHandler(tools.Deps{
		Trakt:               up,
		Store:               st,
		Limiter:             lim,
		Verifier:            osrv.Verifier(),
		ResourceMetadataURL: osrv.ResourceMetadataURL(),
		Version:             version,
		Logger:              log,
		Metrics:             metrics,
	})

	var metricsHandler http.Handler
	if cfg.Metrics {
		metricsHandler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	}

	servers := []*http.Server{newHTTPServer(cfg.Listen, web.New(cfg, osrv, mcpHandler, metricsHandler), log)}
	if cfg.Metrics && cfg.MetricsListen != "" {
		servers = append(servers, newHTTPServer(cfg.MetricsListen, web.NewMetricsHandler(cfg, metricsHandler), log))
	}

	listeners, err := listenAll(ctx, servers)
	if err != nil {
		return err
	}

	bgCtx, stopBackground := context.WithCancel(ctx)
	defer stopBackground()

	var bg sync.WaitGroup
	bg.Go(func() { lim.Run(bgCtx) })
	bg.Go(func() { jobs.Run(bgCtx, st, up, time.Now) })

	runErr := serveUntilDone(ctx, log, servers, listeners)

	stopBackground()
	bg.Wait()

	return runErr
}

// listenAll binds every server's address before anything starts serving, so a
// taken port fails the command at once. On failure it closes the listeners
// already bound.
func listenAll(ctx context.Context, servers []*http.Server) ([]net.Listener, error) {
	listeners := make([]net.Listener, 0, len(servers))

	for _, srv := range servers {
		ln, err := new(net.ListenConfig).Listen(ctx, "tcp", srv.Addr)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("listen %s: %w", srv.Addr, err), closeAll(listeners))
		}

		listeners = append(listeners, ln)
	}

	return listeners, nil
}

// serveUntilDone serves every listener until ctx is done or one server fails,
// then drains all servers. It returns the serve failure and any shutdown errors.
func serveUntilDone(ctx context.Context, log *slog.Logger, servers []*http.Server, listeners []net.Listener) error {
	serveErr := make(chan error, len(servers))

	for i, srv := range servers {
		log.InfoContext(ctx, "listening", slog.String("addr", listeners[i].Addr().String()))
		go func() {
			if err := srv.Serve(listeners[i]); !errors.Is(err, http.ErrServerClosed) {
				serveErr <- fmt.Errorf("serve %s: %w", srv.Addr, err)
			}
		}()
	}

	var runErr error

	select {
	case <-ctx.Done():
		log.InfoContext(ctx, "shutting down")
	case runErr = <-serveErr:
	}

	// ctx is already done here; the drain needs its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	for _, srv := range servers {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("shutdown %s: %w", srv.Addr, err))
		}
	}

	return runErr
}

func newHTTPServer(addr string, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		IdleTimeout:       idleTimeout,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}

func closeAll(listeners []net.Listener) error {
	var errs []error
	for _, ln := range listeners {
		errs = append(errs, ln.Close())
	}

	return errors.Join(errs...)
}
