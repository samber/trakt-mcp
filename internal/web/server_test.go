package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samber/trakt-mcp/internal/config"
	"github.com/samber/trakt-mcp/internal/crypto"
	"github.com/samber/trakt-mcp/internal/oauth"
	"github.com/samber/trakt-mcp/internal/store"
	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	testKey         = "A:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	testBaseURL     = "https://trakt-mcp.example.com"
	testSiteURL     = "https://site.example.com"
	testMetricsPath = "/metrics"
	testMetricsUser = "prom"
	testMetricsPass = "0123456789abcdef-pass"
	metricsBody     = "metrics-ok"
	mcpBody         = "mcp-ok"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()

	u, err := url.Parse(testBaseURL)
	if err != nil {
		t.Fatalf("parse base: %v", err)
	}

	site, err := url.Parse(testSiteURL)
	if err != nil {
		t.Fatalf("parse site: %v", err)
	}

	return config.Config{
		BaseURL:         u,
		SiteURL:         site,
		TraktClientID:   "client-id",
		EnableDCR:       true,
		MetricsPath:     testMetricsPath,
		MetricsUser:     testMetricsUser,
		MetricsPassword: testMetricsPass,
	}
}

func newOAuth(t *testing.T, cfg config.Config) *oauth.Server {
	t.Helper()

	kr, err := crypto.ParseKeyring(testKey)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"), kr)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	up := trakt.NewUpstream(trakt.UpstreamConfig{ClientID: "client-id", ClientSecret: "secret", RedirectURL: testBaseURL + oauth.CallbackPath}, st)

	srv, err := oauth.New(cfg, oauth.Deps{
		Store:    st,
		Clients:  oauth.NewClientResolver(st, oauth.NewCIMDFetcher(oauth.CIMDOptions{})),
		Upstream: up,
		Keyring:  kr,
	})
	if err != nil {
		t.Fatalf("oauth.New: %v", err)
	}

	return srv
}

func fixed(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})
}

func newHandler(t *testing.T, mutate func(*config.Config)) http.Handler {
	t.Helper()

	cfg := testConfig(t)
	if mutate != nil {
		mutate(&cfg)
	}

	return New(cfg, newOAuth(t, cfg), fixed(mcpBody), fixed(metricsBody))
}

type response struct {
	status int
	body   string
	header http.Header
}

func do(t *testing.T, h http.Handler, method, target string, setup func(*http.Request)) response {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, nil)
	if setup != nil {
		setup(req)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return response{status: rec.Code, body: rec.Body.String(), header: rec.Header()}
}

func assertSame404(t *testing.T, got, want response) {
	t.Helper()

	if got.status != http.StatusNotFound || want.status != http.StatusNotFound {
		t.Fatalf("status = %d and %d, want both 404", got.status, want.status)
	}

	if got.body != want.body {
		t.Errorf("body = %q, want %q", got.body, want.body)
	}

	if got.header.Get("Content-Type") != want.header.Get("Content-Type") {
		t.Errorf("Content-Type = %q, want %q", got.header.Get("Content-Type"), want.header.Get("Content-Type"))
	}
}

const testChallenge = "challenge-token-123"

func TestOpenAIChallenge_ServedAsExactPlainText(t *testing.T) {
	t.Parallel()

	h := newHandler(t, func(c *config.Config) { c.OpenAIAppsChallenge = testChallenge })
	got := do(t, h, http.MethodGet, "/.well-known/openai-apps-challenge", nil)

	if got.status != http.StatusOK || got.body != testChallenge {
		t.Errorf("status = %d, body = %q, want 200 and exactly the token", got.status, got.body)
	}

	if ct := got.header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestOpenAIChallenge_UnsetSame404AsUnknown(t *testing.T) {
	t.Parallel()

	h := newHandler(t, nil)
	unknown := do(t, h, http.MethodGet, "/no-such-page", nil)
	challenge := do(t, h, http.MethodGet, "/.well-known/openai-apps-challenge", nil)
	assertSame404(t, challenge, unknown)
}

func TestOpenAIChallenge_RejectsOtherMethods(t *testing.T) {
	t.Parallel()

	h := newHandler(t, func(c *config.Config) { c.OpenAIAppsChallenge = testChallenge })

	if got := do(t, h, http.MethodPost, "/.well-known/openai-apps-challenge", nil); got.status != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", got.status)
	}
}

func TestMetrics_DisabledSame404AsUnknown(t *testing.T) {
	t.Parallel()

	h := newHandler(t, func(c *config.Config) { c.Metrics = false })
	unknown := do(t, h, http.MethodGet, "/no-such-page", nil)
	metrics := do(t, h, http.MethodGet, testMetricsPath, func(r *http.Request) {
		r.SetBasicAuth(testMetricsUser, testMetricsPass)
	})
	assertSame404(t, metrics, unknown)
}

func TestMetrics_BasicAuth(t *testing.T) {
	t.Parallel()

	h := newHandler(t, func(c *config.Config) { c.Metrics = true })

	cases := []struct {
		name       string
		user, pass string
		noCreds    bool
		want       int
	}{
		{name: "no credentials", noCreds: true, want: http.StatusUnauthorized},
		{name: "wrong password", user: testMetricsUser, pass: "wrong-password-123456", want: http.StatusUnauthorized},
		{name: "wrong user", user: "someone", pass: testMetricsPass, want: http.StatusUnauthorized},
		{name: "empty", user: "", pass: "", want: http.StatusUnauthorized},
		{name: "right", user: testMetricsUser, pass: testMetricsPass, want: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			res := do(t, h, http.MethodGet, testMetricsPath, func(r *http.Request) {
				if !tc.noCreds {
					r.SetBasicAuth(tc.user, tc.pass)
				}
			})
			if res.status != tc.want {
				t.Fatalf("status = %d, want %d", res.status, tc.want)
			}

			if tc.want == http.StatusUnauthorized {
				if got := res.header.Get("WWW-Authenticate"); got != `Basic realm="metrics"` {
					t.Errorf("WWW-Authenticate = %q", got)
				}

				if strings.Contains(res.body, metricsBody) {
					t.Error("401 leaked the metrics body")
				}

				return
			}

			if res.body != metricsBody {
				t.Errorf("body = %q, want %q", res.body, metricsBody)
			}
		})
	}
}

func TestMetrics_SeparateListenerNotOnMain(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.Metrics = true
	cfg.MetricsListen = "127.0.0.1:9090"
	main := New(cfg, newOAuth(t, cfg), fixed(mcpBody), fixed(metricsBody))
	auth := func(r *http.Request) { r.SetBasicAuth(testMetricsUser, testMetricsPass) }

	unknown := do(t, main, http.MethodGet, "/no-such-page", nil)
	onMain := do(t, main, http.MethodGet, testMetricsPath, auth)
	assertSame404(t, onMain, unknown)

	side := NewMetricsHandler(cfg, fixed(metricsBody))
	if res := do(t, side, http.MethodGet, testMetricsPath, auth); res.status != http.StatusOK || res.body != metricsBody {
		t.Errorf("metrics listener: status %d body %q", res.status, res.body)
	}

	if res := do(t, side, http.MethodGet, testMetricsPath, nil); res.status != http.StatusUnauthorized {
		t.Errorf("metrics listener without credentials: status %d, want 401", res.status)
	}

	assertSame404(t, do(t, side, http.MethodGet, "/", nil), unknown)
	assertSame404(t, do(t, side, http.MethodPost, "/mcp", nil), unknown)
}

func TestRoutes_MCPAndOAuthMounted(t *testing.T) {
	t.Parallel()

	h := newHandler(t, nil)
	if res := do(t, h, http.MethodPost, "/mcp", nil); res.status != http.StatusOK || res.body != mcpBody {
		t.Errorf("POST /mcp: status %d body %q", res.status, res.body)
	}

	res := do(t, h, http.MethodGet, "/.well-known/oauth-authorization-server", nil)
	if res.status != http.StatusOK || !strings.Contains(res.body, `"issuer":"`+testBaseURL+`"`) {
		t.Errorf("AS metadata: status %d body %q", res.status, res.body)
	}
}

func TestRootRedirectsToSiteURL(t *testing.T) {
	t.Parallel()

	h := newHandler(t, nil)

	res := do(t, h, http.MethodGet, "/", nil)
	if res.status != http.StatusFound {
		t.Fatalf("GET /: status %d, want 302", res.status)
	}

	if got := res.header.Get("Location"); got != testSiteURL {
		t.Errorf("Location = %q, want %q", got, testSiteURL)
	}

	// The root route must not swallow unknown paths.
	if res := do(t, h, http.MethodGet, "/no-such-page", nil); res.status != http.StatusNotFound {
		t.Errorf("GET /no-such-page: status %d, want 404", res.status)
	}
}

func TestPrivacy_RouteRemoved(t *testing.T) {
	t.Parallel()

	h := newHandler(t, nil)

	if res := do(t, h, http.MethodGet, "/privacy", nil); res.status != http.StatusNotFound {
		t.Errorf("GET /privacy: status %d, want 404", res.status)
	}
}

func TestNew_NilSiteURLPanicsClearly(t *testing.T) {
	t.Parallel()

	cfg := testConfig(t)
	cfg.SiteURL = nil

	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "SiteURL is required") {
			t.Errorf("panic = %q, want a message naming SiteURL", msg)
		}
	}()

	New(cfg, nil, fixed(mcpBody), fixed(metricsBody))
}
