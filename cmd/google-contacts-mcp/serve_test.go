package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samber/google-contacts-mcp/internal/config"
	"github.com/samber/google-contacts-mcp/internal/crypto"
)

// serveWait bounds startup and shutdown in the serve test.
const serveWait = 10 * time.Second

// serveConfig loads a serve config on a random loopback port.
func serveConfig(t *testing.T, extra ...string) config.Config {
	t.Helper()

	keys, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	vars := map[string]string{
		"GCMCP_ENCRYPTION_KEYS":      keys,
		"GCMCP_GOOGLE_CLIENT_SECRET": "client-secret",
	}

	args := append([]string{
		"--base-url=http://localhost:8080",
		"--listen=127.0.0.1:0",
		"--db-path=" + filepath.Join(t.TempDir(), "db.sqlite"),
		"--google-client-id=client-id",
	}, extra...)

	cfg, err := config.Load(config.CmdServe, args, func(k string) string { return vars[k] })
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	return cfg
}

type logLine struct {
	Level string `json:"level"`
	Msg   string `json:"msg"`
	Addr  string `json:"addr"`
}

// startServe runs runServe and returns the decoded log lines as they come,
// plus a stop function that cancels it and returns its error.
func startServe(t *testing.T, cfg config.Config) (lines <-chan logLine, stop func() error) {
	t.Helper()

	// runServe installs a process-wide slog default.
	lockSlogDefault(t)

	pr, pw := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		err := runServe(ctx, cfg, pw)
		done <- err

		_ = pw.Close()
	}()

	out := make(chan logLine, 64)
	go func() {
		defer close(out)

		sc := bufio.NewScanner(pr)
		for sc.Scan() {
			var l logLine
			if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
				t.Errorf("log line is not JSON: %q", sc.Text())
				continue
			}

			out <- l
		}
	}()

	return out, func() error {
		cancel()

		select {
		case err := <-done:
			// Drain the pipe so the logger never blocks on a full channel.
			for l := range out {
				_ = l
			}

			return err
		case <-time.After(serveWait):
			t.Fatal("runServe did not stop after cancel")
			return nil
		}
	}
}

func waitFor(t *testing.T, lines <-chan logLine, msg string) logLine {
	t.Helper()

	timeout := time.After(serveWait)

	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("log closed before %q", msg)
			}

			if l.Msg == msg {
				return l
			}
		case <-timeout:
			t.Fatalf("no %q log line", msg)
		}
	}
}

// httpGet issues a context-bound GET; the caller closes the response body.
func httpGet(t *testing.T, url string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}

	return resp
}

func TestRunServe_ServesAndShutsDown(t *testing.T) {
	t.Parallel()

	lines, stop := startServe(t, serveConfig(t))
	addr := waitFor(t, lines, "listening").Addr

	resp := httpGet(t, "http://"+addr+"/.well-known/oauth-authorization-server")

	var meta struct {
		Issuer string `json:"issuer"`
	}

	decErr := json.NewDecoder(resp.Body).Decode(&meta)
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}

	if decErr != nil {
		t.Fatalf("decode metadata: %v", decErr)
	}

	if meta.Issuer != "http://localhost:8080" {
		t.Errorf("issuer = %q", meta.Issuer)
	}

	resp = httpGet(t, "http://"+addr+"/metrics")

	if err := resp.Body.Close(); err != nil {
		t.Fatalf("close body: %v", err)
	}

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/metrics with metrics off: status %d, want 404", resp.StatusCode)
	}

	if err := stop(); err != nil {
		t.Errorf("runServe: %v", err)
	}
}

func TestRunServe_DebugWarnsAboutPersonalData(t *testing.T) {
	t.Parallel()

	lines, stop := startServe(t, serveConfig(t, "--log-level=debug"))

	l := waitFor(t, lines, "debug log level writes personal data (tool arguments and results) to logs")
	if l.Level != "WARN" {
		t.Errorf("level = %q, want WARN", l.Level)
	}

	waitFor(t, lines, "listening")

	if err := stop(); err != nil {
		t.Errorf("runServe: %v", err)
	}
}

func TestRunServe_ListenErrorFails(t *testing.T) {
	t.Parallel()

	cfg := serveConfig(t)
	cfg.Listen = "256.0.0.1:1"

	lines, stop := startServe(t, cfg)
	for l := range lines {
		if strings.Contains(l.Msg, "listening") {
			t.Error("listening on an invalid address")
		}
	}

	if err := stop(); err == nil || !strings.Contains(err.Error(), "listen") {
		t.Errorf("err = %v, want a listen error", err)
	}
}

func TestNewHTTPServer_Timeouts(t *testing.T) {
	t.Parallel()

	srv := newHTTPServer(":0", http.NotFoundHandler(), slog.Default())
	if srv.ReadTimeout != readTimeout || srv.ReadHeaderTimeout != readHeaderTimeout || srv.IdleTimeout != idleTimeout {
		t.Errorf("timeouts = read %v, header %v, idle %v", srv.ReadTimeout, srv.ReadHeaderTimeout, srv.IdleTimeout)
	}

	if readTimeout <= 0 {
		t.Error("readTimeout must bound slow request bodies")
	}
}
