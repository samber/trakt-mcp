// Package obs holds the Prometheus metrics of the server and the per-call
// recorder that carries the last Google HTTP status of a tool call to its log
// line. Metric labels never carry user or client identifiers (spec §12).
package obs

import (
	"context"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// namespace prefixes every metric name, so they group under one prefix
	// in a shared Prometheus.
	namespace = "gcmcp"

	// Label names; spec §12 forbids any user or client label.
	labelTool   = "tool"
	labelStatus = "status"
	labelMethod = "method"
	labelCode   = "code"
	labelKind   = "kind"
)

// Metrics records tool calls, Google API calls, rate-limit rejections and
// OAuth grants. Every method is a no-op on a nil *Metrics, so metrics stay
// optional for callers.
type Metrics struct {
	toolCalls      *prometheus.CounterVec
	toolDuration   *prometheus.HistogramVec
	googleCalls    *prometheus.CounterVec
	googleDuration *prometheus.HistogramVec
	rateLimited    *prometheus.CounterVec
	grants         *prometheus.CounterVec
}

// NewMetrics creates the metrics and registers them on reg. It panics when a
// metric is already registered on reg, which only a wiring bug can cause.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		toolCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "tool_calls_total",
			Help:      "MCP tool calls by tool and status (ok, error, rate_limited).",
		}, []string{labelTool, labelStatus}),
		toolDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "tool_call_duration_seconds",
			Help:      "MCP tool call latency by tool.",
			// Default buckets (5 ms to 10 s) cover one to a few People API
			// round trips.
			Buckets: prometheus.DefBuckets,
		}, []string{labelTool}),
		googleCalls: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "google_calls_total",
			Help:      "People API calls by method and HTTP status code (0 when no response was received).",
		}, []string{labelMethod, labelCode}),
		googleDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "google_call_duration_seconds",
			Help:      "People API call latency by method.",
			// Default buckets (5 ms to 10 s) match single Google round trips.
			Buckets: prometheus.DefBuckets,
		}, []string{labelMethod}),
		rateLimited: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "rate_limited_total",
			Help:      "Tool calls refused by a rate limit, by kind (read, write, google).",
		}, []string{labelKind}),
		grants: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "oauth_grants_total",
			Help:      "Access tokens issued to MCP clients, by grant type.",
		}, []string{labelKind}),
	}
	reg.MustRegister(m.toolCalls, m.toolDuration, m.googleCalls, m.googleDuration, m.rateLimited, m.grants)

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

// GoogleCall records one People API call; code is 0 without a response.
func (m *Metrics) GoogleCall(method string, code int, d time.Duration) {
	if m == nil {
		return
	}

	m.googleCalls.WithLabelValues(method, strconv.Itoa(code)).Inc()
	m.googleDuration.WithLabelValues(method).Observe(d.Seconds())
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

// GoogleHook returns the People API call hook: it counts the call and
// records its status for the enclosing tool call's log line. It works on a
// nil *Metrics too, so the log line keeps its Google status without metrics.
func (m *Metrics) GoogleHook() func(ctx context.Context, method string, code int, d time.Duration) {
	return func(ctx context.Context, method string, code int, d time.Duration) {
		m.GoogleCall(method, code, d)
		RecordGoogleStatus(ctx, code)
	}
}

// callRecorderKey keys the per-call recorder in a context.
type callRecorderKey struct{}

// callRecorder holds the HTTP status of the last Google call of one tool
// call. Atomic because a tool may call Google from several goroutines.
type callRecorder struct{ status atomic.Int64 }

// WithCallRecorder returns a context that records Google statuses, and a
// function reading the last recorded one (0 when Google was not called).
func WithCallRecorder(ctx context.Context) (context.Context, func() int) {
	rec := &callRecorder{}
	return context.WithValue(ctx, callRecorderKey{}, rec), func() int { return int(rec.status.Load()) }
}

// RecordGoogleStatus stores code as the last Google status of the tool call
// in ctx; it does nothing outside a tool call.
func RecordGoogleStatus(ctx context.Context, code int) {
	if rec, ok := ctx.Value(callRecorderKey{}).(*callRecorder); ok {
		rec.status.Store(int64(code))
	}
}
