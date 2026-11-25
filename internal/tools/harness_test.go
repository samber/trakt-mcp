package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/samber/trakt-mcp/internal/limits"
	"github.com/samber/trakt-mcp/internal/obs"
	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// successContentBlocks is the text blocks of a successful result: the
	// serialized structured output only.
	successContentBlocks = 1

	testUserID = "user-1"
	testToken  = "good-token"
	// testTraktToken is the Trakt access token the fake token source hands out.
	testTraktToken = "trakt-access-token" //nolint:gosec // G101: a fake token for the fake Trakt
	// testMetadataURL is what the 401 header must point to.
	testMetadataURL = "https://mcp.example.com/.well-known/oauth-protected-resource"
	// testRateBudget is a per-minute budget no test reaches by accident.
	testRateBudget = 1000

	// keyStatus is the structured-log and metric label key holding the call outcome.
	keyStatus = "status"

	toolTrending     = "fetch_trending_movies"
	toolMovieSummary = "fetch_movie_summary"
)

// fakeTrakt is an httptest Trakt API. Handlers are keyed by "METHOD /path";
// unknown routes answer 404.
type fakeTrakt struct {
	srv *httptest.Server

	mu       sync.Mutex
	routes   map[string]http.HandlerFunc
	requests []*http.Request
}

func newFakeTrakt(t *testing.T) *fakeTrakt {
	t.Helper()

	f := &fakeTrakt{routes: map[string]http.HandlerFunc{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeTrakt) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.Clone(context.Background()))
	h := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()

	if h == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	h(w, r)
}

func (f *fakeTrakt) handle(route string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.routes[route] = h
}

// respond makes route answer status with body and the given headers.
func (f *fakeTrakt) respond(route string, status int, body string, headers map[string]string) {
	f.handle(route, func(w http.ResponseWriter, _ *http.Request) {
		for k, v := range headers {
			w.Header().Set(k, v)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if _, err := w.Write([]byte(body)); err != nil {
			panic(err)
		}
	})
}

func (f *fakeTrakt) seen() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.requests)
}

// staticTokens is a token source whose token Trakt never accepts twice:
// a rejection means the grant is gone.
type staticTokens struct{}

func (staticTokens) AccessToken(context.Context) (string, error) { return testTraktToken, nil }

func (staticTokens) Rejected(context.Context, string) (string, error) {
	return "", trakt.ErrUpstreamAuth
}

// fakeClients hands out real Trakt clients pointed at the fake, for
// testUserID only.
type fakeClients struct {
	api *trakt.Client

	mu  sync.Mutex
	err error
}

func (f *fakeClients) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

func (f *fakeClients) Client(_ context.Context, userID string) (*trakt.UserClient, error) {
	f.mu.Lock()
	err := f.err
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}

	if userID != testUserID {
		return nil, fmt.Errorf("unexpected user %q", userID)
	}

	return f.api.For(staticTokens{}), nil
}

type fakeToucher struct {
	mu      sync.Mutex
	touched []string
}

func (f *fakeToucher) TouchUser(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.touched = append(f.touched, id)

	return nil
}

func (f *fakeToucher) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.touched)
}

// fakeVerifier accepts testToken only, with the uid Extra key oauth.UserID reads.
func fakeVerifier(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	if token != testToken {
		return nil, fmt.Errorf("%w: unknown token", auth.ErrInvalidToken)
	}

	return &auth.TokenInfo{
		Scopes:     []string{"trakt"},
		Expiration: time.Now().Add(time.Hour),
		UserID:     testUserID,
		Extra:      map[string]any{"uid": testUserID},
	}, nil
}

// syncBuffer is a goroutine-safe log sink: the MCP handler logs from server
// goroutines while the test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// lines decodes every JSON log record whose msg is msg.
func (b *syncBuffer) lines(t *testing.T, msg string) []map[string]any {
	t.Helper()

	var out []map[string]any

	for raw := range strings.SplitSeq(strings.TrimSpace(b.String()), "\n") {
		if raw == "" {
			continue
		}

		var rec map[string]any
		if err := json.Unmarshal([]byte(raw), &rec); err != nil {
			t.Fatalf("log line %q is not JSON: %v", raw, err)
		}

		if rec["msg"] == msg {
			out = append(out, rec)
		}
	}

	return out
}

// toolLine returns the "tool call" log line of tool.
func toolLine(t *testing.T, buf *syncBuffer, tool string) map[string]any {
	t.Helper()

	for _, rec := range buf.lines(t, "tool call") {
		if rec["tool"] == tool {
			return rec
		}
	}

	t.Fatalf("no tool call line for %s in:\n%s", tool, buf.String())

	return nil
}

type harness struct {
	t       *testing.T
	trakt   *fakeTrakt
	clients *fakeClients
	toucher *fakeToucher
	logs    *syncBuffer
	reg     *prometheus.Registry
	srv     *httptest.Server
	session *mcp.ClientSession
}

// harnessConfig is what options may change before the server is built.
type harnessConfig struct {
	deps Deps
	// extra tool groups registered after the catalog.
	extra []func(*Server)
	// wrap, when set, wraps the MCP handler.
	wrap     func(http.Handler) http.Handler
	logLevel slog.Level
	// apiBaseURL overrides the fake Trakt URL, to reach a closed port.
	apiBaseURL string
}

type harnessOption func(*harnessConfig)

func withLimiter(l *limits.Limiter) harnessOption {
	return func(c *harnessConfig) { c.deps.Limiter = l }
}

func withTools(groups ...func(*Server)) harnessOption {
	return func(c *harnessConfig) { c.extra = append(c.extra, groups...) }
}

// withOnlyTools registers just the named tools; see Deps.onlyTools.
func withOnlyTools(names ...string) harnessOption {
	return func(c *harnessConfig) {
		c.deps.onlyTools = map[string]struct{}{}
		for _, n := range names {
			c.deps.onlyTools[n] = struct{}{}
		}
	}
}

func withLogLevel(level slog.Level) harnessOption {
	return func(c *harnessConfig) { c.logLevel = level }
}

func withWrap(wrap func(http.Handler) http.Handler) harnessOption {
	return func(c *harnessConfig) { c.wrap = wrap }
}

func withAPIBaseURL(u string) harnessOption {
	return func(c *harnessConfig) { c.apiBaseURL = u }
}

// newHarness serves the MCP endpoint over httptest with a fake Trakt behind
// real Trakt clients, metrics on a private registry and JSON logs in a buffer.
func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()

	h := &harness{t: t, trakt: newFakeTrakt(t), toucher: &fakeToucher{}, logs: &syncBuffer{}, reg: prometheus.NewRegistry()}
	cfg := harnessConfig{logLevel: slog.LevelInfo, apiBaseURL: h.trakt.srv.URL}
	cfg.deps = Deps{
		Store:               h.toucher,
		Limiter:             limits.New(testRateBudget, testRateBudget),
		Verifier:            fakeVerifier,
		ResourceMetadataURL: testMetadataURL,
		Version:             "test",
		Metrics:             obs.NewMetrics(h.reg),
	}

	for _, o := range opts {
		o(&cfg)
	}

	cfg.deps.Logger = slog.New(slog.NewJSONHandler(h.logs, &slog.HandlerOptions{Level: cfg.logLevel}))
	h.clients = &fakeClients{api: trakt.New(trakt.Config{
		BaseURL: cfg.apiBaseURL,
		APIKey:  "test-client-id",
		HTTP:    &http.Client{Transport: cfg.deps.Metrics.Transport(nil)},
	})}
	cfg.deps.Trakt = h.clients

	handler := newHandler(cfg.deps, append(slices.Clone(catalog), cfg.extra...))
	if cfg.wrap != nil {
		handler = cfg.wrap(handler)
	}

	h.srv = httptest.NewServer(handler)
	t.Cleanup(h.srv.Close)
	h.session = h.connect("")

	return h
}

// bearerTransport adds the test bearer token to every request.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)

	return http.DefaultTransport.RoundTrip(r)
}

func (h *harness) connect(version string) *mcp.ClientSession {
	h.t.Helper()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:             h.srv.URL,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: testToken}},
		MaxRetries:           -1,
		DisableStandaloneSSE: true,
	}

	cs, err := client.Connect(context.Background(), transport, &mcp.ClientSessionOptions{ProtocolVersion: version})
	if err != nil {
		h.t.Fatalf("Connect(%q): %v", version, err)
	}

	h.t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			h.t.Logf("close session: %v", err)
		}
	})

	return cs
}

// call runs a tool and returns its result; protocol errors fail the test.
func (h *harness) call(name string, args any) *mcp.CallToolResult {
	h.t.Helper()

	res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("CallTool(%s): %v", name, err)
	}

	return res
}

// ok runs a tool, requires success and decodes its structured output.
func (h *harness) ok(name string, args, out any) *mcp.CallToolResult {
	h.t.Helper()

	res := h.call(name, args)
	if res.IsError {
		h.t.Fatalf("%s: tool error %q", name, resultText(res))
	}

	requireSerializedOutput(h.t, name, res)

	if out != nil {
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			h.t.Fatalf("marshal structured content: %v", err)
		}

		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("decode %s output %s: %v", name, raw, err)
		}
	}

	return res
}

// fail runs a tool and requires a tool error with exactly want.
func (h *harness) fail(name string, args any, want string) {
	h.t.Helper()

	res := h.call(name, args)
	if !res.IsError {
		h.t.Fatalf("%s: IsError = false, want %q", name, want)
	}

	if got := resultText(res); got != want {
		h.t.Fatalf("%s: error = %q, want %q", name, got, want)
	}
}

func resultText(res *mcp.CallToolResult) string {
	var parts []string

	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}

	return strings.Join(parts, "\n")
}

// requireSerializedOutput fails unless a successful result carries exactly one
// text block holding the JSON of its structured content, the only part of a
// result some clients hand to the model.
func requireSerializedOutput(t *testing.T, name string, res *mcp.CallToolResult) {
	t.Helper()

	if len(res.Content) != successContentBlocks {
		t.Fatalf("%s: %d content blocks, want %d", name, len(res.Content), successContentBlocks)
	}

	var fromText, fromStructured any
	if err := json.Unmarshal([]byte(resultText(res)), &fromText); err != nil {
		t.Fatalf("%s: content JSON %q: %v", name, resultText(res), err)
	}

	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: marshal structured content: %v", name, err)
	}

	if err := json.Unmarshal(raw, &fromStructured); err != nil {
		t.Fatalf("%s: decode structured content %s: %v", name, raw, err)
	}

	if !reflect.DeepEqual(fromText, fromStructured) {
		t.Errorf("%s: content JSON %s differs from structured content %s", name, resultText(res), raw)
	}
}

// metricValue returns the counter value of the first series of the named
// family whose labels match, or 0 when none does.
func metricValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	for _, f := range families {
		if f.GetName() != name {
			continue
		}

	next:
		for _, metric := range f.GetMetric() {
			for _, l := range metric.GetLabel() {
				if labels[l.GetName()] != l.GetValue() {
					continue next
				}
			}

			return metric.GetCounter().GetValue()
		}
	}

	return 0
}

// listTools returns every tool the server built from deps advertises, over
// an in-memory transport.
func listTools(t *testing.T, srv *Server) map[string]*mcp.Tool {
	t.Helper()

	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()

	ss, err := srv.MCP().Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	t.Cleanup(func() {
		if err := ss.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	t.Cleanup(func() {
		if err := cs.Close(); err != nil {
			t.Logf("close: %v", err)
		}
	})

	got := map[string]*mcp.Tool{}

	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("Tools: %v", err)
		}

		got[tool.Name] = tool
	}

	return got
}

// offlineDeps are deps for a server that is only listed, never called.
func offlineDeps() Deps {
	return Deps{
		Trakt:   &fakeClients{api: trakt.New(trakt.Config{BaseURL: "http://127.0.0.1:1"})},
		Store:   &fakeToucher{},
		Limiter: limits.New(testRateBudget, testRateBudget),
		Version: "test",
	}
}
