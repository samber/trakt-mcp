package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// fakeWaiter records the deadline it was given and answers with err.
type fakeWaiter struct {
	deadline time.Time
	hasDL    bool
	canceled bool
	err      error
}

func (f *fakeWaiter) Wait(ctx context.Context) error {
	f.deadline, f.hasDL = ctx.Deadline()
	f.canceled = ctx.Err() != nil

	return f.err
}

func TestWaitRefreshes_DeadlineCoversRefreshTimeout(t *testing.T) {
	t.Parallel()

	// The serve ctx is already canceled at shutdown; the wait must not inherit that.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	w := &fakeWaiter{}
	start := time.Now()

	waitRefreshes(ctx, slog.New(slog.DiscardHandler), w)

	if !w.hasDL {
		t.Fatal("Wait got no deadline")
	}

	if w.canceled {
		t.Error("Wait got an already canceled ctx")
	}

	if got := w.deadline.Sub(start); got < trakt.RefreshTimeout {
		t.Errorf("Wait deadline = %v from start, want at least trakt.RefreshTimeout (%v)", got, trakt.RefreshTimeout)
	}
}

func TestWaitRefreshes_LogsTimeout(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer

	w := &fakeWaiter{err: context.DeadlineExceeded}
	waitRefreshes(context.Background(), slog.New(slog.NewTextHandler(&buf, nil)), w)

	if !strings.Contains(buf.String(), "level=ERROR") {
		t.Error("a Wait timeout was not logged")
	}
}
