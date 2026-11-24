// Package web assembles the HTTP surface of the server: the MCP endpoint, the
// OAuth endpoints, the redirect from the root to the website, and the optional
// Prometheus endpoint behind Basic auth (spec §5.1, §12). The server renders no
// page of its own; the website lives in site/.
package web

import (
	"crypto/subtle"
	"io"
	"net/http"

	"github.com/samber/trakt-mcp/internal/config"
	"github.com/samber/trakt-mcp/internal/oauth"
)

const (
	// pathMCP is the MCP Streamable HTTP endpoint (spec §5.1).
	pathMCP = "/mcp"
	// patternMCP accepts POST only: the server runs in stateless mode, so
	// there is no GET stream or DELETE session to serve.
	patternMCP = "POST " + pathMCP
	// patternHome matches "/" exactly; "{$}" keeps it from catching every
	// unknown path, which must fall through to the mux's 404.
	patternHome = "GET /{$}"

	// patternOpenAIChallenge is where OpenAI reads the domain-verification
	// token before it lists the MCP server in its plugin directory.
	patternOpenAIChallenge = "GET /.well-known/openai-apps-challenge"

	// authHeaderChallenge names the protection space of the metrics 401 answer.
	authHeaderChallenge = `Basic realm="metrics"`
)

// New returns the main listener's handler. GET / redirects to cfg.SiteURL, which
// config requires. metricsHandler is served on
// cfg.MetricsPath behind Basic auth only when metrics are enabled without a
// separate listener; otherwise that path is an ordinary 404.
func New(cfg config.Config, oauthSrv *oauth.Server, mcpHandler, metricsHandler http.Handler) http.Handler {
	if cfg.SiteURL == nil {
		panic("web.New: config.SiteURL is required; config.Load enforces it for serve")
	}

	mux := http.NewServeMux()
	oauthSrv.Mount(mux)
	mux.Handle(patternMCP, mcpHandler)

	if cfg.OpenAIAppsChallenge != "" {
		mux.Handle(patternOpenAIChallenge, openAIChallengeHandler(cfg.OpenAIAppsChallenge))
	}

	// 302, not 301: browsers cache a 301 forever, which would pin the
	// redirect after the website moves.
	mux.Handle(patternHome, http.RedirectHandler(cfg.SiteURL.String(), http.StatusFound))

	if !cfg.Metrics || cfg.MetricsListen != "" || metricsHandler == nil {
		return mux
	}

	return withMetrics(cfg, metricsHandler, mux)
}

// openAIChallengeHandler answers with the token alone as plain text: OpenAI
// rejects JSON, lists, or a trailing newline, since it compares the exact body.
func openAIChallengeHandler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		_, _ = io.WriteString(w, token)
	})
}

// NewMetricsHandler returns the handler of the separate metrics listener
// (--metrics-listen): the metrics path behind Basic auth, 404 elsewhere.
func NewMetricsHandler(cfg config.Config, metricsHandler http.Handler) http.Handler {
	return withMetrics(cfg, metricsHandler, http.NotFoundHandler())
}

// withMetrics serves cfg.MetricsPath behind Basic auth and everything else
// with next. The path is matched exactly instead of being registered as a
// ServeMux pattern, so an operator-chosen path with pattern syntax cannot
// panic the mux.
func withMetrics(cfg config.Config, metricsHandler, next http.Handler) http.Handler {
	protected := basicAuth(cfg.MetricsUser, cfg.MetricsPassword, metricsHandler)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == cfg.MetricsPath {
			protected.ServeHTTP(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// basicAuth admits a request only with the configured user and password.
// Both comparisons always run, so timing does not tell which one failed.
func basicAuth(user, password string, next http.Handler) http.Handler {
	wantUser, wantPassword := []byte(user), []byte(password)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPassword, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(gotUser), wantUser)

		passwordOK := subtle.ConstantTimeCompare([]byte(gotPassword), wantPassword)
		if !ok || userOK&passwordOK != 1 {
			w.Header().Set("WWW-Authenticate", authHeaderChallenge)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)

			return
		}

		next.ServeHTTP(w, r)
	})
}
