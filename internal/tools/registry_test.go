package tools

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/limits"
	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// toolTestWrite is a Write tool registered by tests only.
	toolTestWrite = "add_test_item"
	// concurrentWrites is how many writes TestWriteToolsSerializedPerUser
	// starts at once; more than one is enough to overlap without the lock.
	concurrentWrites = 6
	// writeHold keeps each test write inside its handler long enough for the
	// others to arrive while it runs.
	writeHold = 30 * time.Millisecond
	// trendingRoute is the fake Trakt route of fetch_trending_movies.
	trendingRoute = "GET /movies/trending"
)

// destroyPrefixes are the tool name prefixes that must be Destroy (spec §8).
var destroyPrefixes = []string{"remove_", "cancel_", "delete_"}

// accessByPrefix pins every tool name prefix to its access level, so a new
// tool cannot be mislabeled (a fetch_ tool marked Write, an add_ tool marked
// Read) and a new prefix must be added here deliberately.
var accessByPrefix = map[string]Access{
	"fetch_": Read, "search_": Read,
	"add_": Write, "hide_": Write, "unhide_": Write, "checkin_": Write,
	"remove_": Destroy, "cancel_": Destroy, "delete_": Destroy,
}

// untrustedTextTools return text written by Trakt users (list names and
// descriptions), so their descriptions must carry untrustedNote.
var untrustedTextTools = []string{"fetch_season_lists", "fetch_episode_lists", "fetch_movie_lists", "fetch_show_lists", "search_by_id"}

func TestToolAnnotations(t *testing.T) {
	t.Parallel()

	srv := NewServer(offlineDeps())
	got := listTools(t, srv)

	if len(got) == 0 || len(got) != len(srv.Specs()) {
		t.Fatalf("listed %d tools, registry has %d", len(got), len(srv.Specs()))
	}

	for name, tool := range got {
		a := tool.Annotations
		if tool.Title == "" || a == nil || a.Title != tool.Title {
			t.Errorf("%s: want a title on the tool and in its annotations", name)
			continue
		}

		if a.DestructiveHint == nil || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s: want explicit destructiveHint and openWorldHint true", name)
		}

		if tool.Description == "" || tool.OutputSchema == nil || tool.InputSchema == nil {
			t.Errorf("%s: want a description, an input schema and an output schema", name)
		}
	}
}

func TestEveryToolHasReadWriteAnnotationsConsistent(t *testing.T) {
	t.Parallel()

	// The mapping itself, so the catalog check below is never vacuous.
	for access, want := range map[Access][2]bool{Read: {true, false}, Write: {false, false}, Destroy: {false, true}} {
		a := access.annotations("T")
		if a.ReadOnlyHint != want[0] || a.DestructiveHint == nil || *a.DestructiveHint != want[1] ||
			a.OpenWorldHint == nil || !*a.OpenWorldHint || a.Title != "T" {
			t.Errorf("access %d annotations = %+v, want readOnly %v destructive %v", access, a, want[0], want[1])
		}
	}

	srv := NewServer(offlineDeps())
	listed := listTools(t, srv)

	for _, spec := range srv.Specs() {
		checkSpecConsistent(t, spec, listed[spec.Name])
	}
}

// checkSpecConsistent requires the listed tool to match its spec's access:
// destroy-like names are Destroy, only Read tools are readOnly, and write
// tools warn that they change the real account.
func checkSpecConsistent(t *testing.T, spec Spec, tool *mcp.Tool) {
	t.Helper()

	if tool == nil {
		t.Errorf("%s: registered but not listed", spec.Name)
		return
	}

	for _, p := range destroyPrefixes {
		if strings.HasPrefix(spec.Name, p) && spec.Access != Destroy {
			t.Errorf("%s: name starts with %q, want Destroy access", spec.Name, p)
		}
	}

	checkAccessPrefix(t, spec)

	if slices.Contains(untrustedTextTools, spec.Name) && !strings.Contains(tool.Description, untrustedNote) {
		t.Errorf("%s: returns Trakt user text, description lacks untrustedNote", spec.Name)
	}

	if (spec.Access == Read) != tool.Annotations.ReadOnlyHint {
		t.Errorf("%s: access %d but readOnlyHint %v", spec.Name, spec.Access, tool.Annotations.ReadOnlyHint)
	}

	if spec.Access != Read && !strings.Contains(tool.Description, writeWarning) {
		t.Errorf("%s: write tool description lacks the account warning", spec.Name)
	}
}

// checkAccessPrefix requires the spec's name prefix to be listed in
// accessByPrefix with the spec's access level.
func checkAccessPrefix(t *testing.T, spec Spec) {
	t.Helper()

	matched := false

	for p, want := range accessByPrefix {
		if strings.HasPrefix(spec.Name, p) {
			matched = true

			if spec.Access != want {
				t.Errorf("%s: name starts with %q, access %d, want %d", spec.Name, p, spec.Access, want)
			}
		}
	}

	if !matched {
		t.Errorf("%s: name prefix not in accessByPrefix", spec.Name)
	}
}

func TestRegisterAddsWriteWarningOnce(t *testing.T) {
	t.Parallel()

	srv := newServer(offlineDeps(), []func(*Server){testWriteTools(nil)})
	tool := listTools(t, srv)[toolTestWrite]

	if tool == nil || strings.Count(tool.Description, writeWarning) != 1 {
		t.Fatalf("description = %q, want the warning exactly once", tool.Description)
	}
}

// testWriteTools registers toolTestWrite, a Write tool that holds for
// writeHold and records how many calls overlap.
func testWriteTools(maxActive *atomic.Int64) func(*Server) {
	var active atomic.Int64

	return func(s *Server) {
		Register(s, Spec{Name: toolTestWrite, Title: "Add test item", Description: "Test write.", Access: Write},
			func(context.Context, *trakt.UserClient, struct{}) (Listing[string], error) {
				n := active.Add(1)
				defer active.Add(-1)

				if maxActive != nil {
					for {
						cur := maxActive.Load()
						if n <= cur || maxActive.CompareAndSwap(cur, n) {
							break
						}
					}
				}

				time.Sleep(writeHold)

				return Listing[string]{}, nil
			})
	}
}

func TestWriteToolsSerializedPerUser(t *testing.T) {
	t.Parallel()

	var maxActive atomic.Int64

	h := newHarness(t, withTools(testWriteTools(&maxActive)))

	var wg sync.WaitGroup
	for range concurrentWrites {
		wg.Go(func() {
			res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: toolTestWrite, Arguments: map[string]any{}})
			if err != nil || res.IsError {
				t.Errorf("write: %v %v", err, res)
			}
		})
	}

	wg.Wait()

	if got := maxActive.Load(); got != 1 {
		t.Fatalf("max concurrent writes of one user = %d, want 1", got)
	}
}

func TestRateLimitedToolError(t *testing.T) {
	t.Parallel()

	t.Run("local bucket", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t, withLimiter(limits.New(1, testRateBudget)))
		h.trakt.respond(trendingRoute, http.StatusOK, `[]`, nil)
		h.ok(toolTrending, map[string]any{}, nil)

		res := h.call(toolTrending, map[string]any{})
		if !res.IsError || !strings.HasPrefix(resultText(res), "rate limited, retry after ") || !strings.HasSuffix(resultText(res), " s") {
			t.Fatalf("second read = %q, want rate limited with a delay in seconds", resultText(res))
		}

		if got := len(h.trakt.seen()); got != 1 {
			t.Errorf("Trakt requests = %d, want 1: a refused call must not reach Trakt", got)
		}

		if got := metricValue(t, h.reg, "trakt_mcp_rate_limited_total", map[string]string{"kind": "read"}); got != 1 {
			t.Errorf("rate_limited{read} = %v, want 1", got)
		}
	})

	t.Run("trakt 429", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(trendingRoute, http.StatusTooManyRequests, `{}`, map[string]string{"Retry-After": "7"})
		h.fail(toolTrending, map[string]any{}, "rate limited, retry after 7 s")

		if rec := toolLine(t, h.logs, toolTrending); rec[keyStatus] != statusRateLimited || rec["trakt_status"] != float64(http.StatusTooManyRequests) {
			t.Errorf("log line = %v, want rate_limited with trakt_status 429", rec)
		}

		if got := metricValue(t, h.reg, "trakt_mcp_rate_limited_total", map[string]string{"kind": "trakt"}); got != 1 {
			t.Errorf("rate_limited{trakt} = %v, want 1", got)
		}
	})
}

func TestGlobalBudgetSpentIsRateLimited(t *testing.T) {
	t.Parallel()

	h := newHarness(t, withLimiter(limits.New(testRateBudget, testRateBudget, limits.WithGlobal(1))))
	h.trakt.respond(trendingRoute, http.StatusOK, `[]`, nil)
	h.ok(toolTrending, map[string]any{}, nil)

	res := h.call(toolTrending, map[string]any{})
	if !res.IsError || !strings.HasPrefix(resultText(res), "rate limited, retry after ") {
		t.Fatalf("call over the global budget = %q, want rate limited", resultText(res))
	}

	if got := len(h.trakt.seen()); got != 1 {
		t.Errorf("Trakt requests = %d, want 1", got)
	}

	if got := metricValue(t, h.reg, "trakt_mcp_rate_limited_total", map[string]string{"kind": "global"}); got != 1 {
		t.Errorf("rate_limited{global} = %v, want 1", got)
	}
}

func TestUpstreamAuthToolError(t *testing.T) {
	t.Parallel()

	t.Run("no stored grant", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.clients.setErr(trakt.ErrUpstreamAuth)
		h.fail(toolTrending, map[string]any{}, msgUpstreamAuth)
	})

	t.Run("trakt refuses the token", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(trendingRoute, http.StatusUnauthorized, `{"error":"invalid_token"}`, nil)
		h.fail(toolTrending, map[string]any{}, msgUpstreamAuth)
	})
}

func TestErrorKindMapping(t *testing.T) {
	t.Parallel()

	cases := map[trakt.Kind]struct {
		err  *trakt.Error
		want string
	}{
		trakt.KindUnknown:      {&trakt.Error{Kind: trakt.KindUnknown, Status: http.StatusBadRequest, Message: "bad"}, "Trakt rejected the request (status 400)"},
		trakt.KindNotFound:     {&trakt.Error{Kind: trakt.KindNotFound, Status: http.StatusNotFound, Message: "x"}, msgNotFound},
		trakt.KindRateLimited:  {&trakt.Error{Kind: trakt.KindRateLimited, Status: http.StatusTooManyRequests, RetryAfter: 1500 * time.Millisecond}, "rate limited, retry after 2 s"},
		trakt.KindConflict:     {&trakt.Error{Kind: trakt.KindConflict, Status: http.StatusConflict, Message: "y"}, msgConflict},
		trakt.KindAccountLimit: {&trakt.Error{Kind: trakt.KindAccountLimit, Status: 420, Message: "list full, upgrade!"}, msgAccountLimit},
		trakt.KindLocked:       {&trakt.Error{Kind: trakt.KindLocked, Status: http.StatusLocked}, msgLocked},
		trakt.KindVIPRequired:  {&trakt.Error{Kind: trakt.KindVIPRequired, Status: http.StatusUpgradeRequired}, msgVIPRequired},
		trakt.KindUnauthorized: {&trakt.Error{Kind: trakt.KindUnauthorized, Status: http.StatusUnauthorized}, msgUpstreamAuth},
		trakt.KindServer:       {&trakt.Error{Kind: trakt.KindServer, Status: http.StatusBadGateway, Message: "z"}, "Trakt returned an error (status 502)"},
		trakt.KindUnreachable:  {&trakt.Error{Kind: trakt.KindUnreachable}, msgUnreachable},
		trakt.KindCanceled:     {&trakt.Error{Kind: trakt.KindCanceled}, msgCanceled},
	}

	// Every Kind the trakt package defines must have a case: a new Kind
	// without a mapping fails here.
	for k := trakt.KindUnknown; !strings.HasPrefix(k.String(), "kind("); k++ {
		if _, ok := cases[k]; !ok {
			t.Errorf("no mapping case for kind %s", k)
		}
	}

	for kind, c := range cases {
		got := mapError(c.err)
		if got.Error() != c.want {
			t.Errorf("%s: %q, want %q", kind, got.Error(), c.want)
		}
	}

	if got := mapError(&trakt.Error{Kind: trakt.KindRateLimited}).Error(); got != msgRateLimitedNoDelay {
		t.Errorf("429 without Retry-After = %q, want %q", got, msgRateLimitedNoDelay)
	}

	if got := mapError(errors.Join(errors.New("refresh"), trakt.ErrUpstreamAuth)).Error(); got != msgUpstreamAuth {
		t.Errorf("wrapped ErrUpstreamAuth = %q", got)
	}

	if got := mapError(errors.New("secret detail")).Error(); got != msgInternal {
		t.Errorf("unknown error = %q, want %q", got, msgInternal)
	}

	if got := mapError(invalidArgument("movie_id")).Error(); got != "invalid argument: movie_id" {
		t.Errorf("validation error = %q", got)
	}
}

func TestToolCallLogHasNoArguments(t *testing.T) {
	t.Parallel()

	const (
		secretSlug     = "secret-movie-slug"
		secretOverview = "SECRET-OVERVIEW-TEXT"
	)

	h := newHarness(t)
	h.trakt.respond("GET /movies/"+secretSlug, http.StatusOK, movieJSON(secretSlug, secretOverview), nil)
	h.ok(toolMovieSummary, map[string]any{fieldMovieID: secretSlug}, nil)

	rec := toolLine(t, h.logs, toolMovieSummary)
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}

	for _, key := range []string{"args", "result"} {
		if _, ok := rec[key]; ok {
			t.Errorf("info line has %q", key)
		}
	}

	want := map[string]any{
		"user_id":      testUserID,
		keyStatus:      statusOK,
		"trakt_status": float64(http.StatusOK),
		"result_count": float64(1),
	}
	for k, v := range want {
		if rec[k] != v {
			t.Errorf("%s = %v, want %v", k, rec[k], v)
		}
	}

	if _, ok := rec["duration_ms"].(float64); !ok {
		t.Errorf("duration_ms = %v, want a number", rec["duration_ms"])
	}

	logs := h.logs.String()
	if strings.Contains(logs, secretSlug) || strings.Contains(logs, secretOverview) || strings.Contains(logs, testTraktToken) {
		t.Errorf("info logs contain an argument, a result or a token:\n%s", logs)
	}

	if got := h.toucher.ids(); len(got) != 1 || got[0] != testUserID {
		t.Errorf("touched = %v, want the caller once", got)
	}
}

func TestToolCallLogDebugHasPayload(t *testing.T) {
	t.Parallel()

	h := newHarness(t, withLogLevel(slog.LevelDebug))
	h.trakt.respond("GET /movies/the-matrix-1999", http.StatusOK, movieJSON("the-matrix-1999", "debug overview"), nil)
	h.ok(toolMovieSummary, map[string]any{fieldMovieID: "the-matrix-1999"}, nil)

	rec := toolLine(t, h.logs, toolMovieSummary)

	args, ok := rec["args"].(map[string]any)
	if !ok || args["movie_id"] != "the-matrix-1999" {
		t.Errorf("args = %v, want movie_id", rec["args"])
	}

	if !strings.Contains(h.logs.String(), "debug overview") {
		t.Error("debug line lacks the result")
	}
}

func TestToolCallInternalErrorHidesCause(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	cause := errors.New("store: disk I/O error")
	h.clients.setErr(cause)
	h.fail(toolMovieSummary, map[string]any{fieldMovieID: "secret-arg"}, msgInternal)

	rec := toolLine(t, h.logs, toolMovieSummary)
	if rec[keyStatus] != statusError || rec["level"] != "ERROR" || rec[keyErrorClass] != errorClassInternal ||
		rec[keyErrorType] != "*errors.errorString" {
		t.Errorf("line = %v, want ERROR, class internal and the cause's type", rec)
	}

	if logs := h.logs.String(); strings.Contains(logs, "secret-arg") || strings.Contains(logs, cause.Error()) {
		t.Errorf("logs contain the tool argument or the raw cause:\n%s", logs)
	}

	if n := len(h.logs.lines(t, msgToolCallLog)); n != 1 {
		t.Errorf("log lines = %d, want 1", n)
	}
}

func TestToolCallUnreachableIsWarn(t *testing.T) {
	t.Parallel()

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	h := newHarness(t, withAPIBaseURL(closed.URL))
	h.fail(toolTrending, map[string]any{}, msgUnreachable)

	rec := toolLine(t, h.logs, toolTrending)
	if rec[keyStatus] != statusError || rec["trakt_status"] != float64(0) || rec["level"] != "WARN" ||
		rec[keyErrorClass] != errorClassRefused {
		t.Errorf("log line = %v, want WARN, error, trakt_status 0, class %s", rec, errorClassRefused)
	}

	if logs := h.logs.String(); strings.Contains(logs, "http://") || strings.Contains(logs, "/movies/trending") {
		t.Errorf("logs contain the request URL:\n%s", logs)
	}
}

func TestToolCallCanceledIsInfo(t *testing.T) {
	t.Parallel()

	buf := &syncBuffer{}
	deps := offlineDeps()
	deps.Logger = slog.New(slog.NewJSONHandler(buf, nil))

	err := mapError(&trakt.Error{Kind: trakt.KindCanceled})
	deps.finishCall(t.Context(), callRecord{tool: toolTrending, userID: testUserID, err: err})

	rec := toolLine(t, buf, toolTrending)
	if rec[keyStatus] != statusCanceled || rec["level"] != "INFO" {
		t.Errorf("line = %v, want INFO with status canceled", rec)
	}

	if _, ok := rec[keyErrorClass]; ok {
		t.Errorf("canceled call logged as a failure: %v", rec)
	}

	if got := mapError(context.Canceled).Error(); got != msgCanceled {
		t.Errorf("context.Canceled = %q", got)
	}
}

func TestToolCallMetrics(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond(trendingRoute, http.StatusOK, `[]`, nil)
	h.ok(toolTrending, map[string]any{}, nil)

	checks := []struct {
		name   string
		labels map[string]string
	}{
		{"trakt_mcp_tool_calls_total", map[string]string{"tool": toolTrending, keyStatus: statusOK}},
		{"trakt_mcp_trakt_calls_total", map[string]string{"endpoint": "movies", "code": "200"}},
	}
	for _, c := range checks {
		if got := metricValue(t, h.reg, c.name, c.labels); got != 1 {
			t.Errorf("%s%v = %v, want 1", c.name, c.labels, got)
		}
	}
}
