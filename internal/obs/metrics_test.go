package obs

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// metricPrefix is the required prefix of every metric name.
const metricPrefix = "gcmcp_"

func exercise(m *Metrics) {
	m.ToolCall("search_contacts", "ok", 30*time.Millisecond)
	m.GoogleCall("search", 200, 20*time.Millisecond)
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

	allowed := map[string]bool{"tool": true, "status": true, "method": true, "code": true, "kind": true}

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
	m.GoogleCall("search", 0, time.Millisecond)

	if got := testutil.ToFloat64(m.toolCalls.WithLabelValues("search_contacts", "ok")); got != 1 {
		t.Errorf("tool calls = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.googleCalls.WithLabelValues("search", "200")); got != 1 {
		t.Errorf("google calls 200 = %v, want 1", got)
	}

	if got := testutil.ToFloat64(m.googleCalls.WithLabelValues("search", "0")); got != 1 {
		t.Errorf("google calls 0 = %v, want 1", got)
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

	ctx, status := WithCallRecorder(context.Background())
	m.GoogleHook()(ctx, "get", 404, time.Millisecond)

	if got := status(); got != 404 {
		t.Fatalf("status = %d, want 404 recorded without metrics", got)
	}
}

func TestCallRecorder(t *testing.T) {
	t.Parallel()

	// Outside a tool call, recording must not panic.
	RecordGoogleStatus(context.Background(), 500)

	ctx, status := WithCallRecorder(context.Background())
	if got := status(); got != 0 {
		t.Fatalf("status before any call = %d, want 0", got)
	}

	reg := prometheus.NewRegistry()
	hook := NewMetrics(reg).GoogleHook()
	hook(ctx, "search warmup", 200, time.Millisecond)
	hook(ctx, "search", 429, time.Millisecond)

	if got := status(); got != 429 {
		t.Fatalf("status = %d, want the last one, 429", got)
	}
}
