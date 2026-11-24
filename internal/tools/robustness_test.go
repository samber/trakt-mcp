package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/limits"
	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// toolTestPanic is a Write tool, registered by tests only, that panics
	// the way its mode argument asks.
	toolTestPanic = "add_test_panic"
	// panicSecret must never reach the client or the logs.
	panicSecret = "PANIC-SECRET-VALUE"
	// panicCalls is how many panicking calls the test makes before a good one.
	panicCalls = 3
	// panicCallTimeout fails the test instead of hanging when a panic left
	// the write lock held.
	panicCallTimeout = 5 * time.Second
)

// panicArgs selects how toolTestPanic panics; "" returns normally.
type panicArgs struct {
	Mode string `json:"mode"`
}

type panicTarget struct{ name string }

func testPanicTools(s *Server) {
	Register(s, Spec{Name: toolTestPanic, Title: "Add test panic", Description: "Test panic.", Access: Write},
		func(_ context.Context, _ *trakt.UserClient, in panicArgs) (Listing[string], error) {
			switch in.Mode {
			case "string":
				panic(panicSecret)
			case "error":
				panic(errors.New(panicSecret))
			case "nil":
				var p *panicTarget

				return Listing[string]{Items: []string{p.name}}, nil
			}

			return Listing[string]{}, nil
		})
}

func TestHandlerPanicReturnsInternalError(t *testing.T) {
	t.Parallel()

	// panicCalls panics plus one good call spend the whole write budget, and
	// the global one: the next call is refused only if every panicking call
	// was charged.
	budget := panicCalls + 1
	h := newHarness(t, withTools(testPanicTools), withLimiter(limits.New(testRateBudget, budget, limits.WithGlobal(budget))))

	for _, mode := range []string{"string", "error", "nil"} {
		res := callWithTimeout(t, h, toolTestPanic, panicArgs{Mode: mode})
		if !res.IsError || resultText(res) != msgInternal {
			t.Fatalf("panic %s: result = %q (isError %v), want %q", mode, resultText(res), res.IsError, msgInternal)
		}
	}

	// The server keeps serving, and the write lock was released.
	if res := callWithTimeout(t, h, toolTestPanic, panicArgs{}); res.IsError {
		t.Fatalf("call after panics = %q, want success", resultText(res))
	}

	res := callWithTimeout(t, h, toolTestPanic, panicArgs{})
	if !res.IsError || !strings.HasPrefix(resultText(res), msgRateLimitedPrefix) {
		t.Fatalf("call over budget = %q, want rate limited: panicking calls must be charged", resultText(res))
	}

	checkPanicLogs(t, h)

	if got := metricValue(t, h.reg, "trakt_mcp_tool_calls_total", map[string]string{"tool": toolTestPanic, keyStatus: statusError}); got != panicCalls {
		t.Errorf("tool_calls{error} = %v, want %d", got, panicCalls)
	}
}

// checkPanicLogs requires one ERROR line with a stack per panicking call and
// no trace of the panic value anywhere in the logs.
func checkPanicLogs(t *testing.T, h *harness) {
	t.Helper()

	panics := 0

	for _, rec := range h.logs.lines(t, msgToolCallLog) {
		if rec[keyErrorClass] == errorClassPanic {
			panics++

			if rec[keyStatus] != statusError || rec["level"] != "ERROR" {
				t.Errorf("panic line = %v, want ERROR with status error", rec)
			}

			if stack, ok := rec["stack"].(string); !ok || !strings.Contains(stack, "testPanicTools") {
				t.Errorf("panic line has no stack of the handler: %v", rec)
			}
		}
	}

	if panics != panicCalls {
		t.Errorf("panic lines = %d, want %d:\n%s", panics, panicCalls, h.logs.String())
	}

	if strings.Contains(h.logs.String(), panicSecret) {
		t.Errorf("logs contain the panic value:\n%s", h.logs.String())
	}
}

func callWithTimeout(t *testing.T, h *harness, name string, args any) *mcp.CallToolResult {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), panicCallTimeout)
	defer cancel()

	res, err := h.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}

	return res
}

func TestRegisterDuplicateNamePanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil || !strings.Contains(fmt.Sprint(r), toolTrending) {
			t.Errorf("recover() = %v, want a panic naming %s", r, toolTrending)
		}
	}()

	newServer(offlineDeps(), []func(*Server){registerMovieTools, registerMovieTools})
}

func TestContextErrorsMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err         error
		want        string
		unreachable bool
		canceled    bool
	}{
		{context.DeadlineExceeded, msgUnreachable, true, false},
		{fmt.Errorf("page 2: %w", context.DeadlineExceeded), msgUnreachable, true, false},
		{context.Canceled, msgCanceled, false, true},
	}
	for _, c := range cases {
		te, ok := errors.AsType[*toolError](mapError(c.err))
		if !ok || te.msg != c.want || te.unreachable != c.unreachable || te.canceled != c.canceled {
			t.Errorf("mapError(%v) = %+v, want %q unreachable %v canceled %v", c.err, te, c.want, c.unreachable, c.canceled)
		}
	}
}

func TestFailedCallLogsOneLine(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond(trendingRoute, http.StatusBadRequest, `{"error_description":"BODY-TEXT"}`, nil)
	h.fail(toolTrending, map[string]any{}, "Trakt rejected the request (status 400)")

	if n := len(h.logs.lines(t, "tool call failed")); n != 0 {
		t.Errorf("separate failure lines = %d, want 0", n)
	}

	rec := toolLine(t, h.logs, toolTrending)
	if rec["level"] != "ERROR" || rec[keyErrorClass] != errorClassTraktRejected {
		t.Errorf("line = %v, want ERROR with class %s", rec, errorClassTraktRejected)
	}

	if strings.Contains(h.logs.String(), "BODY-TEXT") {
		t.Errorf("logs contain the Trakt body:\n%s", h.logs.String())
	}
}

func TestInvalidInputIsCountedAndLogged(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	// limit must be an integer: the input schema refuses it before any handler.
	res := h.call(toolTrending, map[string]any{fieldLimit: "ten"})
	if !res.IsError {
		t.Fatalf("result = %q, want a schema rejection", resultText(res))
	}

	rec := toolLine(t, h.logs, toolTrending)
	if rec[keyStatus] != statusInvalidInput || rec["user_id"] != testUserID {
		t.Errorf("line = %v, want status %s for the caller", rec, statusInvalidInput)
	}

	if strings.Contains(h.logs.String(), "ten") {
		t.Errorf("logs contain the argument:\n%s", h.logs.String())
	}

	if got := metricValue(t, h.reg, "trakt_mcp_tool_calls_total", map[string]string{"tool": toolTrending, keyStatus: statusInvalidInput}); got != 1 {
		t.Errorf("tool_calls{invalid_input} = %v, want 1", got)
	}

	if n := len(h.logs.lines(t, "tool call")); n != 1 {
		t.Errorf("tool call lines = %d, want 1", n)
	}
}

func TestListOverviewIsShorter(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("word ", maxOverviewRunes)
	h := newHarness(t)
	h.trakt.respond(trendingRoute, http.StatusOK, `[{"watchers": 1, "movie": `+movieJSON("m", long)+`}]`, nil)

	var out Listing[TrendingMovie]
	h.ok(toolTrending, map[string]any{}, &out)

	if len(out.Items) != 1 {
		t.Fatalf("items = %+v", out.Items)
	}

	if n := len([]rune(out.Items[0].Movie.Overview)); n == 0 || n > maxListOverviewRunes {
		t.Errorf("list overview has %d runes, want 1..%d", n, maxListOverviewRunes)
	}
}
