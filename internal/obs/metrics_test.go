package obs

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// metricPrefix is the required prefix of every metric name.
const metricPrefix = "trakt_mcp_"

func exercise(m *Metrics) {
	m.ToolCall("list_trending", "ok", 30*time.Millisecond)
	m.TraktCall("movies", 200, 20*time.Millisecond)
	m.RateLimited("write")
	m.Grant("authorization_code")
}

func TestMetrics_NoUserLabels(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	exercise(NewMetrics(reg))

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	if len(families) == 0 {
		t.Fatal("no metric gathered")
	}

	allowed := map[string]bool{"tool": true, "status": true, "endpoint": true, "code": true, "kind": true}

	for _, f := range families {
		if !strings.HasPrefix(f.GetName(), metricPrefix) {
			t.Errorf("metric %q lacks prefix %q", f.GetName(), metricPrefix)
		}

		for _, metric := range f.GetMetric() {
			for _, l := range metric.GetLabel() {
				name := strings.ToLower(l.GetName())
				if strings.HasPrefix(name, "user") || strings.HasPrefix(name, "client") {
					t.Errorf("metric %q has identifying label %q", f.GetName(), l.GetName())
				}

				if !allowed[l.GetName()] {
					t.Errorf("metric %q has unexpected label %q", f.GetName(), l.GetName())
				}
			}
		}
	}
}

func TestMetrics_Values(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	exercise(m)
	m.TraktCall("movies", 0, time.Millisecond)

	if got := testutil.ToFloat64(m.toolCalls.WithLabelValues("list_trending", "ok")); got != 1 {
		t.Errorf("tool calls = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.traktCalls.WithLabelValues("movies", "200")); got != 1 {
		t.Errorf("trakt calls 200 = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.traktCalls.WithLabelValues("movies", "0")); got != 1 {
		t.Errorf("trakt calls 0 = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.rateLimited.WithLabelValues("write")); got != 1 {
		t.Errorf("rate limited = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.grants.WithLabelValues("authorization_code")); got != 1 {
		t.Errorf("grants = %v, want 1", got)
	}
}

func TestMetrics_NilIsNoop(t *testing.T) {
	t.Parallel()

	var m *Metrics
	exercise(m)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	ctx, status := WithCallRecorder(t.Context())
	doGet(ctx, t, &http.Client{Transport: m.Transport(nil)}, srv.URL+"/movies/x")

	if got := status(); got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 recorded without metrics", got)
	}
}

func TestCallRecorder(t *testing.T) {
	t.Parallel()

	// Outside a tool call, recording must not panic.
	RecordUpstreamStatus(context.Background(), 500)

	ctx, status := WithCallRecorder(context.Background())
	if got := status(); got != 0 {
		t.Fatalf("status before any call = %d, want 0", got)
	}

	RecordUpstreamStatus(ctx, 200)
	RecordUpstreamStatus(ctx, 429)

	if got := status(); got != 429 {
		t.Fatalf("status = %d, want the last one, 429", got)
	}
}

func TestTransportCountsByEndpointGroup(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/shows/") {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	client := &http.Client{Transport: m.Transport(http.DefaultTransport)}

	ctx, status := WithCallRecorder(t.Context())
	doGet(ctx, t, client, srv.URL+"/movies/tt0903747")
	doGet(ctx, t, client, srv.URL+"/shows/breaking-bad")
	doGet(ctx, t, client, srv.URL+"/secret-path/abc")

	// The last request answered 200.
	if got := status(); got != http.StatusOK {
		t.Errorf("recorded status = %d, want the last one, 200", got)
	}

	checks := map[[2]string]float64{
		{"movies", "200"}:      1,
		{"shows", "429"}:       1,
		{endpointOther, "200"}: 1,
	}
	for labels, want := range checks {
		if got := testutil.ToFloat64(m.traktCalls.WithLabelValues(labels[0], labels[1])); got != want {
			t.Errorf("trakt calls %v = %v, want %v", labels, got, want)
		}
	}
}

func TestTransportRecordsZeroOnFailure(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	client := &http.Client{Transport: m.Transport(failingTransport{})}

	ctx, status := WithCallRecorder(t.Context())
	RecordUpstreamStatus(ctx, http.StatusOK)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://trakt.invalid/movies/x", nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Do(req)
	if err == nil {
		closeBody(t, resp)
		t.Fatal("want a transport error")
	}

	if got := status(); got != 0 {
		t.Errorf("status = %d, want 0 without a response", got)
	}

	if got := testutil.ToFloat64(m.traktCalls.WithLabelValues("movies", "0")); got != 1 {
		t.Errorf("trakt calls movies/0 = %v, want 1", got)
	}
}

func TestEndpointGroup(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"/movies/trending":        "movies",
		"/sync/history":           "sync",
		"/smart-lists":            "smart-lists",
		"/":                       "other",
		"":                        "other",
		"/tt0903747/movies":       "other",
		"/oauth/token":            "oauth",
		"/users/me/history/shows": "users",
	}
	for path, want := range cases {
		if got := EndpointGroup(path); got != want {
			t.Errorf("EndpointGroup(%q) = %q, want %q", path, got, want)
		}
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

func doGet(ctx context.Context, t *testing.T, client *http.Client, url string) {
	t.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	closeBody(t, resp)
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()

	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}
}
