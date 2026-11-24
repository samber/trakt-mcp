package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	toolAddToHistory      = "add_to_history"
	toolRemoveFromHistory = "remove_from_history"

	routeSyncHistory       = "/sync/history"
	routeSyncHistoryRemove = "/sync/history/remove"

	argItems     = "items"
	argType      = "type"
	argID        = "id"
	argWatchedAt = "watched_at"

	testMovieTraktID   = "481"
	testEpisodeTraktID = "73482"
	testSeasonTraktID  = "3572"
	testEpisodeIMDb    = "tt0959621"
	testUnknownIMDb    = "tt9999999"
	keyNotFound        = "not_found"
	keyAdded           = "added"
	keyDeleted         = "deleted"
	keyExisting        = "existing"
	// injectionMarker stands for hostile Trakt text that must never reach a
	// tool output or a log line.
	injectionMarker = "IGNORE-PREVIOUS-INSTRUCTIONS"
	// testWatchedAt is the timestamp of the fixtures' latest play.
	testWatchedAt     = "2024-03-01T20:00:00.000Z"
	testDate          = "2024-01-31"
	pathFirstItemType = "items.0.type"
	pathSecondType    = "items.1.type"
	pathFirstRating   = "items.0.rating"
	pathFirstShow     = "items.0.show"
	testEpisodeTitle  = "Pilot"
	fixtureSyncRemove = "sync_remove"
)

// accountCase is one account tool run end to end against its fixture.
type accountCase struct {
	tool    string
	args    map[string]any
	route   string
	status  int
	fixture string
	headers map[string]string
	// want maps dotted output paths to expected values, compared printed.
	want map[string]any
	// absent lists output paths that must not exist.
	absent []string
	// object is true when Trakt answers an object rather than a list.
	object bool
}

// accountCaseSets collects each account group's cases; test files add theirs
// in init.
var accountCaseSets [][]accountCase

// accountAccess maps every account tool to its expected access; test files
// add theirs in init.
var accountAccess = map[string]Access{}

// batchTool is a batch write tool: its route and the arguments it needs
// besides items.
type batchTool struct {
	tool, route string
	extra       map[string]any
}

var batchTools []batchTool

func init() {
	accountCaseSets = append(accountCaseSets, historyWriteCases)
	batchTools = append(batchTools,
		batchTool{tool: toolAddToHistory, route: "POST " + routeSyncHistory},
		batchTool{tool: toolRemoveFromHistory, route: "POST " + routeSyncHistoryRemove},
	)
	accountAccess[toolAddToHistory] = Write
	accountAccess[toolRemoveFromHistory] = Destroy
}

func allAccountCases() []accountCase {
	var all []accountCase
	for _, set := range accountCaseSets {
		all = append(all, set...)
	}

	return all
}

func itemArg(typ, id string) map[string]any { return map[string]any{argType: typ, argID: id} }

func itemsArg(items ...map[string]any) map[string]any {
	list := make([]any, 0, len(items))
	for _, it := range items {
		list = append(list, it)
	}

	return map[string]any{argItems: list}
}

// manyMovies returns n distinct movie items by numeric id.
func manyMovies(n int) map[string]any {
	items := make([]map[string]any, 0, n)
	for i := range n {
		items = append(items, itemArg(itemMovie, strconv.Itoa(i+1)))
	}

	return itemsArg(items...)
}

var historyWriteCases = []accountCase{
	{
		tool: toolAddToHistory, args: itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemMovie, testUnknownIMDb)),
		route: "POST " + routeSyncHistory, status: http.StatusCreated, fixture: "sync_add", object: true,
		want:   map[string]any{keyAdded: 3, keyDeleted: 0, keyExisting: 0, "not_found.0.type": itemMovie, "not_found.0.id": testUnknownIMDb},
		absent: []string{"not_found.1"},
	},
	{
		tool: toolRemoveFromHistory, args: itemsArg(itemArg(itemShow, testShowSlug)),
		route: "POST " + routeSyncHistoryRemove, fixture: fixtureSyncRemove, object: true,
		want:   map[string]any{keyDeleted: 62, keyAdded: 0, keyNotFound: []any{}},
		absent: []string{"not_found.0"},
	},
}

// bodyLog records the request bodies a fake Trakt route received.
type bodyLog struct {
	mu     sync.Mutex
	bodies []string
}

func (b *bodyLog) all() []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return append([]string(nil), b.bodies...)
}

// captureBodies makes route answer status with resp and record each body.
func captureBodies(h *harness, route string, status int, resp string) *bodyLog {
	log := &bodyLog{}

	h.trakt.handle(route, func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}

		log.mu.Lock()
		log.bodies = append(log.bodies, string(raw))
		log.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if _, err := w.Write([]byte(resp)); err != nil {
			panic(err)
		}
	})

	return log
}

// decodeSync parses a recorded write body.
func decodeSync(t *testing.T, body string) SyncItems {
	t.Helper()

	var s SyncItems
	if err := json.Unmarshal([]byte(body), &s); err != nil {
		t.Fatalf("decode body %q: %v", body, err)
	}

	return s
}

func mustItems(t *testing.T, args ...ItemArg) []Item {
	t.Helper()

	items, err := parseItems(args)
	if err != nil {
		t.Fatalf("parseItems(%v): %v", args, err)
	}

	return items
}

func TestGroupItems(t *testing.T) {
	t.Parallel()

	items := mustItems(t,
		ItemArg{Type: itemMovie, ID: testMovieSlug},
		ItemArg{Type: itemMovie, ID: testMovieTraktID},
		ItemArg{Type: itemShow, ID: testShowIMDb},
		ItemArg{Type: itemShow, ID: testShowSlug},
		ItemArg{Type: itemSeason, ID: testSeasonTraktID},
		ItemArg{Type: itemEpisode, ID: testEpisodeTraktID},
		ItemArg{Type: itemEpisode, ID: testEpisodeIMDb},
	)

	got, err := GroupItems(items)
	if err != nil {
		t.Fatalf("GroupItems: %v", err)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"movies":[{"ids":{"slug":"the-matrix-1999"}},{"ids":{"trakt":481}}],` +
		`"shows":[{"ids":{"imdb":"tt0903747"}},{"ids":{"slug":"breaking-bad"}}],` +
		`"seasons":[{"ids":{"trakt":3572}}],` +
		`"episodes":[{"ids":{"trakt":73482}},{"ids":{"imdb":"tt0959621"}}]}`
	if string(raw) != want {
		t.Errorf("GroupItems =\n%s\nwant\n%s", raw, want)
	}

	t.Run("only present types", func(t *testing.T) {
		t.Parallel()

		got, err := GroupItems(mustItems(t, ItemArg{Type: itemSeason, ID: testSeasonTraktID}))
		if err != nil {
			t.Fatal(err)
		}

		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}

		if string(raw) != `{"seasons":[{"ids":{"trakt":3572}}]}` {
			t.Errorf("season only = %s", raw)
		}
	})

	rejected := []ItemArg{
		{Type: itemSeason, ID: "breaking-bad-season-1"},
		{Type: itemEpisode, ID: "pilot"},
		{Type: "person", ID: testMovieTraktID},
		{Type: "", ID: testMovieTraktID},
		{Type: "Movie", ID: testMovieTraktID},
		{Type: itemMovie, ID: personIMDb},
		{Type: itemMovie, ID: ""},
		{Type: itemMovie, ID: traversalID},
		{Type: itemMovie, ID: "0"},
		{Type: itemEpisode, ID: "99999999999999999999999"},
	}
	for _, arg := range rejected {
		t.Run("reject "+arg.Type+"/"+arg.ID, func(t *testing.T) {
			t.Parallel()

			_, err := parseItems([]ItemArg{arg})
			if err == nil || err.Error() != invalid(fieldItems) {
				t.Errorf("parseItems(%+v) error = %v, want %q", arg, err, invalid(fieldItems))
			}
		})
	}
}

func TestBatchBoundaries(t *testing.T) {
	t.Parallel()

	for _, n := range []int{0, 1, maxBatchItems, maxBatchItems + 1} {
		t.Run(strconv.Itoa(n), func(t *testing.T) {
			t.Parallel()

			items := make([]Item, 0, n)
			for i := range n {
				items = append(items, Item{Type: itemMovie, ID: ID(strconv.Itoa(i + 1))})
			}

			_, err := GroupItems(items)
			valid := n >= 1 && n <= maxBatchItems

			if valid && err != nil {
				t.Errorf("GroupItems(%d items): %v", n, err)
			}

			if !valid && (err == nil || err.Error() != invalid(fieldItems)) {
				t.Errorf("GroupItems(%d items) error = %v, want %q", n, err, invalid(fieldItems))
			}
		})
	}

	t.Run("tool", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.fail(toolAddToHistory, map[string]any{argItems: []any{}}, invalid(fieldItems))
		h.fail(toolAddToHistory, manyMovies(maxBatchItems+1), invalid(fieldItems))

		if got := len(h.trakt.seen()); got != 0 {
			t.Fatalf("Trakt requests = %d, want 0", got)
		}

		log := captureBodies(h, "POST "+routeSyncHistory, http.StatusCreated, `{}`)
		h.ok(toolAddToHistory, itemsArg(itemArg(itemMovie, testMovieTraktID)), nil)

		if got := decodeSync(t, log.all()[0]); len(got.Movies) != 1 {
			t.Errorf("one item sent as %+v", got)
		}
	})
}

func TestWatchedAtVariants(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(2 * maxFutureSkew).UTC().Format(time.RFC3339)
	soon := time.Now().Add(time.Hour).UTC()

	cases := []struct {
		name, value string
		items       map[string]any
		// want is the watched_at Trakt must receive; wantErr the tool error.
		want, wantErr string
	}{
		{name: "iso millis", value: "2024-01-31T20:00:00.000Z", want: "2024-01-31T20:00:00.000Z"},
		{name: "rfc3339 offset", value: "2024-01-31T21:00:00+01:00", want: "2024-01-31T20:00:00.000Z"},
		{name: "unknown", value: watchedUnknown, want: watchedUnknown},
		{name: "omitted", value: "", want: ""},
		{name: "released episode", value: watchedReleased, items: itemsArg(itemArg(itemEpisode, testEpisodeTraktID)), want: watchedReleased},
		{name: "released movie", value: watchedReleased, wantErr: invalid(fieldWatchedAt)},
		{name: "released mixed", value: watchedReleased, items: itemsArg(itemArg(itemEpisode, testEpisodeTraktID), itemArg(itemShow, testShowSlug)), wantErr: invalid(fieldWatchedAt)},
		{name: "within skew", value: soon.Format(time.RFC3339), want: soon.Truncate(time.Second).Format(traktTimeLayout)},
		{name: "far future", value: future, wantErr: invalid(fieldWatchedAt)},
		{name: "garbage", value: "yesterday", wantErr: invalid(fieldWatchedAt)},
		{name: "date only", value: testDate, wantErr: invalid(fieldWatchedAt)},
		{name: "upper case", value: "UNKNOWN", wantErr: invalid(fieldWatchedAt)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			items := tc.items
			if items == nil {
				items = itemsArg(itemArg(itemMovie, testMovieTraktID))
			}

			args := merge(items, nil)
			if tc.value != "" {
				args[argWatchedAt] = tc.value
			}

			h := newHarness(t)

			if tc.wantErr != "" {
				h.fail(toolAddToHistory, args, tc.wantErr)

				if got := len(h.trakt.seen()); got != 0 {
					t.Fatalf("Trakt requests = %d, want 0", got)
				}

				return
			}

			log := captureBodies(h, "POST "+routeSyncHistory, http.StatusCreated, `{}`)
			h.ok(toolAddToHistory, args, nil)

			var body map[string][]map[string]any
			if err := json.Unmarshal([]byte(log.all()[0]), &body); err != nil {
				t.Fatal(err)
			}

			for group, entries := range body {
				for _, e := range entries {
					got, has := e[argWatchedAt]
					if tc.want == "" && has {
						t.Errorf("%s entry has watched_at %v, want none", group, got)
					}

					if tc.want != "" && fmt.Sprint(got) != tc.want {
						t.Errorf("%s watched_at = %v, want %s", group, got, tc.want)
					}
				}
			}
		})
	}
}

func TestNotFoundSurfaced(t *testing.T) {
	t.Parallel()

	t.Run("partly found", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("POST "+routeSyncHistory, http.StatusCreated, `{
			"added": {"movies": 1, "episodes": 62},
			"not_found": {
				"movies": [{"ids": {"imdb": "`+testUnknownIMDb+`"}}, {"ids": {"trakt": 77}}],
				"shows": [], "seasons": [], "episodes": [{"ids": {"slug": "<b>`+injectionMarker+`</b>"}}],
				"people": []
			}}`, nil)

		var out WriteResult

		res := h.ok(toolAddToHistory, itemsArg(
			itemArg(itemMovie, testMovieTraktID), itemArg(itemMovie, testUnknownIMDb), itemArg(itemMovie, "77"), itemArg(itemShow, testShowSlug),
		), &out)

		if out.Added != 63 {
			t.Errorf("added = %d, want 63", out.Added)
		}

		want := []Item{{Type: itemMovie, ID: testUnknownIMDb}, {Type: itemMovie, ID: "77"}}
		if fmt.Sprint(out.NotFound) != fmt.Sprint(want) {
			t.Errorf("not_found = %+v, want %+v", out.NotFound, want)
		}

		if strings.Contains(resultText(res), injectionMarker) || strings.Contains(h.logs.String(), injectionMarker) {
			t.Errorf("hostile Trakt id reached the output or logs: %s", resultText(res))
		}
	})

	t.Run("nothing found is not an error", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("POST "+routeSyncHistory, http.StatusCreated,
			`{"added": {"movies": 0, "episodes": 0}, "not_found": {"movies": [{"ids": {"trakt": 1}}], "shows": [{"ids": {"slug": "breaking-bad"}}]}}`, nil)

		var out map[string]any

		h.ok(toolAddToHistory, itemsArg(itemArg(itemMovie, "1"), itemArg(itemShow, testShowSlug)), &out)

		if fmt.Sprint(out[keyAdded]) != "0" {
			t.Errorf("added = %v, want 0", out[keyAdded])
		}

		if got := fmt.Sprint(out[keyNotFound]); got != "[map[id:1 type:movie] map[id:breaking-bad type:show]]" {
			t.Errorf("not_found = %s", got)
		}
	})
}

func TestWriteToolsSendOneRequest(t *testing.T) {
	t.Parallel()

	for _, bt := range batchTools {
		t.Run(bt.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			log := captureBodies(h, bt.route, http.StatusCreated, `{}`)

			h.ok(bt.tool, merge(manyMovies(maxBatchItems), bt.extra), nil)

			bodies := log.all()
			if len(bodies) != 1 {
				t.Fatalf("%d Trakt requests for %d items, want 1", len(bodies), maxBatchItems)
			}

			if got := len(decodeSync(t, bodies[0]).Movies); got != maxBatchItems {
				t.Errorf("request carries %d movies, want %d", got, maxBatchItems)
			}

			h.fail(bt.tool, merge(manyMovies(maxBatchItems+1), bt.extra), invalid(fieldItems))

			if got := len(log.all()); got != 1 {
				t.Errorf("Trakt requests after a 51-item call = %d, want still 1", got)
			}
		})
	}
}

// TestAccountToolsOutputs runs every account tool against its fixture and
// checks exact output field names.
func TestAccountToolsOutputs(t *testing.T) {
	t.Parallel()

	for _, tc := range allAccountCases() {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)

			status := tc.status
			if status == 0 {
				status = http.StatusOK
			}

			body := ""
			if tc.fixture != "" {
				body = fixture(t, tc.fixture)
			}

			h.trakt.respond(tc.route, status, body, tc.headers)

			var out any

			res := h.ok(tc.tool, tc.args, &out)

			for path, want := range tc.want {
				got, ok := lookup(out, path)
				if !ok {
					t.Errorf("no field %q in %s", path, resultText(res))
					continue
				}

				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("%s = %v, want %v", path, got, want)
				}
			}

			for _, path := range tc.absent {
				if got, ok := lookup(out, path); ok {
					t.Errorf("field %q = %v, want it absent in %s", path, got, resultText(res))
				}
			}

			if p := findNull(out, ""); p != "" {
				t.Errorf("null at %s in %s", p, resultText(res))
			}

			if got := len(h.trakt.seen()); got != 1 {
				t.Errorf("Trakt requests = %d, want 1", got)
			}
		})
	}
}

// TestAccountToolsNeverReturnNullArrays feeds every account tool the bodies
// Trakt sends for nothing.
func TestAccountToolsNeverReturnNullArrays(t *testing.T) {
	t.Parallel()

	for _, tc := range allAccountCases() {
		if tc.status == http.StatusNoContent {
			// A 204 carries no body to vary.
			continue
		}

		bodies := []string{emptyList, nullBody}
		if tc.object {
			bodies = []string{emptyObject, nullBody}
		}

		for _, body := range bodies {
			t.Run(tc.tool+"/"+body, func(t *testing.T) {
				t.Parallel()

				status := tc.status
				if status == 0 {
					status = http.StatusOK
				}

				h := newHarness(t)
				h.trakt.respond(tc.route, status, body, nil)

				var out any

				res := h.ok(tc.tool, tc.args, &out)

				if p := findNull(out, ""); p != "" {
					t.Errorf("null at %s in %s", p, resultText(res))
				}
			})
		}
	}
}

// TestRemoveToolsAreDestructive checks every account tool's annotations and
// requires every remove_ and cancel_ tool to be destructive.
func TestRemoveToolsAreDestructive(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for name, tool := range tools {
		if (strings.HasPrefix(name, "remove_") || strings.HasPrefix(name, "cancel_")) &&
			(tool.Annotations.DestructiveHint == nil || !*tool.Annotations.DestructiveHint) {
			t.Errorf("%s is not destructive", name)
		}
	}

	for name, access := range accountAccess {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("tool %s is not registered", name)
			continue
		}

		a := tool.Annotations
		destructive := a.DestructiveHint != nil && *a.DestructiveHint

		if a.ReadOnlyHint != (access == Read) || destructive != (access == Destroy) || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s annotations = %+v, want access %d", name, a, access)
		}
	}
}

// TestAccountToolDescriptionsWarnWriteChangesAccount requires every account
// tool that writes to say it changes the user's real Trakt account.
func TestAccountToolDescriptionsWarnWriteChangesAccount(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for name, access := range accountAccess {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("tool %s is not registered", name)
			continue
		}

		warns := strings.Contains(tool.Description, writeWarning)
		if warns != (access != Read) {
			t.Errorf("%s (access %d) description %q: warning present = %v", name, access, tool.Description, warns)
		}
	}
}

// TestRegistryHas79Tools counts every registered tool: the 2 pilots, the 12
// movie, 3 people and 5 comment tools, the 30 show, season and episode tools,
// the 34 account tools of the history, watchlist, watchlist edit, collection, rating,
// check-in, recommendation, smart-list and custom list write groups, the 7 calendar tools and the 7
// where-to-watch and search-by-id tools, the 7 movie extras, the 9 show extras, the 5 custom list read tools
// the 3 show progress tools (counted in the account tools) and the 5 reference data tools.
// The name still says 79 until the last tool group lands.
func TestRegistryHas79Tools(t *testing.T) {
	t.Parallel()

	const accountTools = 7 + 3 + 3 + 3 + 3 + 6 + 2 + 5 + 2 + 3 + 3

	const want = 2 + 12 + 3 + 5 + 30 + accountTools + 7 + 7 + 7 + 9 + 5 + 2 + 5

	if got := len(NewServer(offlineDeps()).Specs()); got != want {
		t.Errorf("registered tools = %d, want %d", got, want)
	}

	if got := len(accountAccess); got != accountTools {
		t.Errorf("account tools under test = %d, want %d", got, accountTools)
	}
}
