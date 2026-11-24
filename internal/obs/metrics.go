// Package obs holds the Prometheus metrics of the server and the per-call
// recorder that carries the last Trakt HTTP status of a tool call to its log
// line. Metric labels never carry user or client identifiers (spec §12).
package obs

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// namespace prefixes every metric name, so they group under one prefix
	// in a shared Prometheus.
	namespace = "trakt_mcp"

	// Label names; spec §12 forbids any user or client label.
	labelTool     = "tool"
	labelStatus   = "status"
	labelEndpoint = "endpoint"
	labelCode     = "code"
	labelKind     = "kind"

	// endpointOther groups every path outside knownEndpoints, so an
	// unexpected path can never add a label value per id.
	endpointOther = "other"
)

// knownEndpoints are the first path segments of the Trakt API this server
// calls. The endpoint label takes one of them or endpointOther: ids live in
// later segments, so the label set stays bounded.
var knownEndpoints = map[string]bool{
	"checkin":         true,
	"comments":        true,
	"movies":          true,
	"oauth":           true,
	"people":          true,
	"recommendations": true,
	"search":          true,
	"shows":           true,
	"smart-lists":     true,
	"sync":            true,
	"users":           true,
}

// Metrics records tool calls, Trakt API calls, rate-limit rejections and
// OAuth grants. Every method is a no-op on a nil *Metrics, so metrics stay
// optional for callers.
type Metrics struct {
	toolCalls     *prometheus.CounterVec
	toolDuration  *prometheus.HistogramVec
	traktCalls    *prometheus.CounterVec
	traktDuration *prometheus.HistogramVec
	rateLimited   *prometheus.CounterVec
	grants        *prometheus.CounterVec
}

// NewMetrics creates the metrics and registers them on reg. It panics when a
// metric is already registered on reg, which only a wiring bug can cause.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		toolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "tool_calls_total",
			Help:      "MCP tool calls by tool and status (ok, error, rate_limited, canceled).",
		}, []string{labelTool, labelStatus}),
		toolDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "tool_call_duration_seconds",
			Help:      "MCP tool call latency by tool.",
			// Default buckets (5 ms to 10 s) cover one to a few Trakt round
			// trips, auto-paginated lists included.
			Buckets: prometheus.DefBuckets,
		}, []string{labelTool}),
		traktCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "trakt_calls_total",
			Help:      "Trakt API requests by endpoint group and HTTP status code (0 when no response was received).",
		}, []string{labelEndpoint, labelCode}),
		traktDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "trakt_call_duration_seconds",
			Help:      "Trakt API request latency by endpoint group.",
			// Default buckets (5 ms to 10 s) match single Trakt round trips.
			Buckets: prometheus.DefBuckets,
		}, []string{labelEndpoint}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limited_total",
			Help:      "Tool calls refused by a rate limit, by kind (read, write, global, trakt).",
		}, []string{labelKind}),
		grants: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "oauth_grants_total",
			Help:      "Access tokens issued to MCP clients, by grant type.",
		}, []string{labelKind}),
	}
	reg.MustRegister(m.toolCalls, m.toolDuration, m.traktCalls, m.traktDuration, m.rateLimited, m.grants)

	return m
}

// ToolCall records one finished tool call.
func (m *Metrics) ToolCall(tool, status string, d time.Duration) {
	if m == nil {
		return
	}

	m.toolCalls.WithLabelValues(tool, status).Inc()
	m.toolDuration.WithLabelValues(tool).Observe(d.Seconds())
}

// TraktCall records one Trakt API request; code is 0 without a response.
func (m *Metrics) TraktCall(endpoint string, code int, d time.Duration) {
	if m == nil {
		return
	}

	m.traktCalls.WithLabelValues(endpoint, strconv.Itoa(code)).Inc()
	m.traktDuration.WithLabelValues(endpoint).Observe(d.Seconds())
}

// RateLimited records one rate-limit refusal.
func (m *Metrics) RateLimited(kind string) {
	if m == nil {
		return
	}

	m.rateLimited.WithLabelValues(kind).Inc()
}

// Grant records one access token issued with the given grant type.
func (m *Metrics) Grant(kind string) {
	if m == nil {
		return
	}

	m.grants.WithLabelValues(kind).Inc()
}

// Transport wraps base (http.DefaultTransport when nil) so every Trakt round
// trip is counted by endpoint group and status, and its status is recorded
// for the enclosing tool call's log line. It works on a nil *Metrics too, so
// the log line keeps its Trakt status without metrics. Retries made by the
// Trakt client are separate round trips: the last one's status wins.
func (m *Metrics) Transport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}

	return &recordingTransport{base: base, metrics: m}
}

type recordingTransport struct {
	base    http.RoundTripper
	metrics *Metrics
}

// RoundTrip implements http.RoundTripper.
func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.base.RoundTrip(req)

	code := 0
	if err == nil {
		code = resp.StatusCode
	}

	t.metrics.TraktCall(EndpointGroup(req.URL.Path), code, time.Since(start))
	RecordUpstreamStatus(req.Context(), code)

	return resp, err //nolint:wrapcheck // a RoundTripper must return the base error unchanged
}

// EndpointGroup is the metric label of a Trakt request path: its first
// segment when known, endpointOther otherwise.
func EndpointGroup(path string) string {
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if knownEndpoints[first] {
		return first
	}

	return endpointOther
}

// callRecorderKey keys the per-call recorder in a context.
type callRecorderKey struct{}

// callRecorder holds the HTTP status of the last Trakt request of one tool
// call. Atomic because a tool may call Trakt from several goroutines.
type callRecorder struct{ status atomic.Int64 }

// WithCallRecorder returns a context that records Trakt statuses, and a
// function reading the last recorded one (0 when Trakt was not called).
func WithCallRecorder(ctx context.Context) (context.Context, func() int) {
	rec := &callRecorder{}
	return context.WithValue(ctx, callRecorderKey{}, rec), func() int { return int(rec.status.Load()) }
}

// RecordUpstreamStatus stores code as the last Trakt status of the tool call
// in ctx; it does nothing outside a tool call.
func RecordUpstreamStatus(ctx context.Context, code int) {
	if rec, ok := ctx.Value(callRecorderKey{}).(*callRecorder); ok {
		rec.status.Store(int64(code))
	}
}
