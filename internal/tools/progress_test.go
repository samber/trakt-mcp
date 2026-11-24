package tools

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	toolFetchShowCollectionProgress = "fetch_show_collection_progress"
	toolResetShowProgress           = "reset_show_progress"
	toolUndoResetShowProgress       = "undo_reset_show_progress"

	argResetAt = "reset_at"
	// pathAired is the output path of a progress tool's aired count.
	pathAired = "aired"

	routeCollectionProgress = "GET /shows/" + testShowSlug + "/progress/collection"
	routeResetProgress      = "POST /shows/" + testShowSlug + "/progress/watched/reset"
	routeUndoResetProgress  = "DELETE /shows/" + testShowSlug + "/progress/watched/reset"

	// testResetOffset is a reset_at with a zone offset; Trakt gets it in UTC.
	testResetOffset = "2024-03-01T21:00:00+01:00"
	// testFarFuture is a reset_at Trakt would refuse: resets cannot start later than now.
	testFarFuture = "2999-01-01T00:00:00Z"
)

func init() {
	accountAccess[toolFetchShowCollectionProgress] = Read
	accountAccess[toolResetShowProgress] = Write
	accountAccess[toolUndoResetShowProgress] = Write

	accountCaseSets = append(accountCaseSets, progressCases)
}

var progressCases = []accountCase{
	{
		tool: toolFetchShowCollectionProgress, args: showShow(testShowSlug), route: routeCollectionProgress, fixture: "show_collection_progress", object: true,
		want: map[string]any{
			pathAired: 8, "completed": 6, "percentage": 75, "last_collected_at": testWatchedAt,
			"next_episode.season": 1, "next_episode.number": 7, "next_episode.title": "A No-Rough-Stuff-Type Deal",
			"last_episode.number": 6, "seasons.0.number": 1, "seasons.0.aired": 7, "seasons.0.completed": 6, "seasons.0.percentage": 85.7,
			"seasons.1.number": 2, "seasons.1.percentage": 0, "hidden_seasons": []any{5},
		},
		absent: []string{"seasons.0.episodes", "last_watched_at"},
	},
	{
		tool: toolResetShowProgress, args: showShow(testShowSlug), route: routeResetProgress, status: http.StatusCreated, fixture: "show_progress_reset", object: true,
		want: map[string]any{"reset": true, argResetAt: testWatchedAt},
	},
	{
		tool: toolUndoResetShowProgress, args: showShow(testShowSlug), route: routeUndoResetProgress, status: http.StatusNoContent, object: true,
		want: map[string]any{"undone": true},
	},
}

func TestCollectionProgressFlags(t *testing.T) {
	t.Parallel()

	runArgCases(t, []argCase{
		{
			name: "flags", tool: toolFetchShowCollectionProgress,
			args:  merge(showShow(testShowSlug), map[string]any{argHidden: true, argSpecials: true, argCountSpecials: false, argLastActivity: lastActivityCollected}),
			route: routeCollectionProgress, body: emptyObject,
			wantQuery: map[string]string{argHidden: queryTrue, argSpecials: queryTrue, argCountSpecials: queryFalse, argLastActivity: lastActivityCollected},
		},
		{
			name: "defaults", tool: toolFetchShowCollectionProgress, args: showShow(testShowSlug), route: routeCollectionProgress, body: emptyObject,
			noQuery: []string{argHidden, argSpecials, argCountSpecials, argLastActivity},
		},
		{
			name: "aired", tool: toolFetchShowCollectionProgress, args: merge(showShow(testShowSlug), map[string]any{argLastActivity: lastActivityAired}),
			route: routeCollectionProgress, body: emptyObject, wantQuery: map[string]string{argLastActivity: lastActivityAired},
		},
		{name: "watched is for watch progress", tool: toolFetchShowCollectionProgress, args: merge(showShow(testShowSlug), map[string]any{argLastActivity: lastActivityWatched}), wantErr: invalid(fieldLastActivity)},
		{name: "bad activity", tool: toolFetchShowCollectionProgress, args: merge(showShow(testShowSlug), map[string]any{argLastActivity: "Collected"}), wantErr: invalid(fieldLastActivity)},
		{name: "bad show", tool: toolFetchShowCollectionProgress, args: showShow(traversalID), wantErr: invalid(fieldShowID)},
		{name: "empty show", tool: toolFetchShowCollectionProgress, args: showShow(""), wantErr: invalid(fieldShowID)},
		{name: "person id", tool: toolFetchShowCollectionProgress, args: showShow(personIMDb), wantErr: invalid(fieldShowID)},
	})

	shapes := []struct {
		name string
		args map[string]any
		body string
		want map[string]any
		// absent lists output paths that must not exist.
		absent []string
	}{
		{
			name: "verbose lists collected episodes", args: merge(showShow(testShowSlug), map[string]any{argVerbose: true}), body: fixture(t, "show_collection_progress"),
			want: map[string]any{
				"seasons.0.episodes.0.number": 1, "seasons.0.episodes.0.collected": true,
				"seasons.0.episodes.0.collected_at": "2024-02-01T20:00:00.000Z", "seasons.0.episodes.6.collected": false,
				"seasons.1.episodes": []any{},
			},
			absent: []string{"seasons.0.episodes.6.collected_at"},
		},
		{
			name: "next episode not an object", args: showShow(testShowSlug),
			body:   `{"aired": 0, "completed": 0, "next_episode": "soon", "last_episode": null, "seasons": null}`,
			want:   map[string]any{"percentage": 0, "seasons": []any{}},
			absent: []string{"next_episode", "last_episode"},
		},
	}

	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(routeCollectionProgress, http.StatusOK, sh.body, nil)

			var out any

			h.ok(toolFetchShowCollectionProgress, sh.args, &out)

			for path, w := range sh.want {
				if got, ok := lookup(out, path); !ok || fmt.Sprint(got) != fmt.Sprint(w) {
					t.Errorf("%s = %v (present %v), want %v", path, got, ok, w)
				}
			}

			for _, path := range sh.absent {
				if got, ok := lookup(out, path); ok {
					t.Errorf("%s = %v, want absent", path, got)
				}
			}
		})
	}
}

func TestResetProgressArguments(t *testing.T) {
	t.Parallel()

	reset := func(at string) map[string]any { return merge(showShow(testShowSlug), map[string]any{argResetAt: at}) }

	runArgCases(t, []argCase{
		{name: "no reset_at", tool: toolResetShowProgress, args: showShow(testShowSlug), route: routeResetProgress, body: emptyObject},
		{name: "empty reset_at", tool: toolResetShowProgress, args: reset(""), route: routeResetProgress, body: emptyObject},
		{name: "timestamp", tool: toolResetShowProgress, args: reset(testWatchedAt), route: routeResetProgress, body: emptyObject},
		{name: "offset timestamp", tool: toolResetShowProgress, args: reset(testResetOffset), route: routeResetProgress, body: emptyObject},
		{name: "words", tool: toolResetShowProgress, args: reset("yesterday"), wantErr: invalid(fieldResetAt)},
		{name: "date only", tool: toolResetShowProgress, args: reset("2024-03-01"), wantErr: invalid(fieldResetAt)},
		{name: "blank", tool: toolResetShowProgress, args: reset(" "), wantErr: invalid(fieldResetAt)},
		{name: "future", tool: toolResetShowProgress, args: reset(testFarFuture), wantErr: invalid(fieldResetAt)},
		{name: "just inside tolerance", tool: toolResetShowProgress, args: reset(time.Now().Add(maxResetFutureSkew - time.Minute).UTC().Format(time.RFC3339)), route: routeResetProgress, body: emptyObject},
		{name: "just outside tolerance", tool: toolResetShowProgress, args: reset(time.Now().Add(maxResetFutureSkew + time.Minute).UTC().Format(time.RFC3339)), wantErr: invalid(fieldResetAt)},
		{name: "bad show", tool: toolResetShowProgress, args: merge(showShow(traversalID), map[string]any{argResetAt: testWatchedAt}), wantErr: invalid(fieldShowID)},
		{name: "empty show", tool: toolResetShowProgress, args: showShow(""), wantErr: invalid(fieldShowID)},
		{name: "undo bad show", tool: toolUndoResetShowProgress, args: showShow(traversalID), wantErr: invalid(fieldShowID)},
		{name: "undo empty show", tool: toolUndoResetShowProgress, args: showShow(""), wantErr: invalid(fieldShowID)},
		{name: "undo person id", tool: toolUndoResetShowProgress, args: showShow(personIMDb), wantErr: invalid(fieldShowID)},
	})
}

func TestResetProgressBodies(t *testing.T) {
	t.Parallel()

	t.Run("no body without reset_at", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		log := captureBodies(h, routeResetProgress, http.StatusCreated, fixture(t, "show_progress_reset"))

		h.ok(toolResetShowProgress, showShow(testShowSlug), nil)

		if got := log.all(); len(got) != 1 || strings.TrimSpace(got[0]) != "" {
			t.Errorf("bodies = %q, want one empty body", got)
		}
	})

	t.Run("reset_at in UTC", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolResetShowProgress, merge(showShow(testShowSlug), map[string]any{argResetAt: testResetOffset}),
			routeResetProgress, http.StatusCreated, fixture(t, "show_progress_reset"))
		if len(got) != 1 || got[argResetAt] != "2024-03-01T20:00:00.000Z" {
			t.Errorf("body = %v, want only reset_at in UTC", got)
		}
	})

	t.Run("empty answer still succeeds", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(routeResetProgress, http.StatusNoContent, "", nil)

		var out any

		h.ok(toolResetShowProgress, showShow(testShowSlug), &out)

		if got, _ := lookup(out, "reset"); fmt.Sprint(got) != "true" {
			t.Errorf("reset = %v, want true", got)
		}

		if _, ok := lookup(out, argResetAt); ok {
			t.Error("reset_at present without a Trakt answer")
		}
	})
}

func TestProgressTraktFailures(t *testing.T) {
	t.Parallel()

	runTraktFailureMatrix(t, []failureTool{
		{toolFetchShowCollectionProgress, showShow(testShowSlug), routeCollectionProgress, true},
		{toolResetShowProgress, showShow(testShowSlug), routeResetProgress, true},
		{toolUndoResetShowProgress, showShow(testShowSlug), routeUndoResetProgress, false},
	})
}

func TestResetDescriptions(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for _, name := range []string{toolResetShowProgress, toolUndoResetShowProgress} {
		d := tools[name].Description
		for _, want := range []string{"VIP", "TV show", "history"} {
			if !strings.Contains(d, want) {
				t.Errorf("%s description %q does not mention %q", name, d, want)
			}
		}
	}

	if d := tools[toolFetchShowCollectionProgress].Description; !strings.Contains(d, "TV show") {
		t.Errorf("collection progress description %q does not say TV show", d)
	}
}
