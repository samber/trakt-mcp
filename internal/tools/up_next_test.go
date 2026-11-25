package tools

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const (
	toolFetchUpNext         = "list_up_next"
	toolFetchHiddenProgress = "list_hidden_progress"
	toolHideFromProgress    = "hide_from_progress"
	toolUnhideFromProgress  = "unhide_from_progress"

	routeUpNext             = "GET /sync/progress/up_next"
	routeHiddenProgress     = "GET /users/hidden/progress_watched"
	routeHideFromProgress   = "POST /users/hidden/progress_watched"
	routeUnhideFromProgress = "POST /users/hidden/progress_watched/remove"

	argIncludeStats = "include_stats"

	// placeholderPageCount stands for the page_count Trakt sends with up next:
	// a value that says there is nothing after page 1.
	placeholderPageCount = "1"
	headerPageCountName  = "X-Pagination-Page-Count"
	headerItemCountName  = "X-Pagination-Item-Count"
	testSortLastWatched  = "last_watched"
)

func init() {
	accountAccess[toolFetchUpNext] = Read
	accountAccess[toolFetchHiddenProgress] = Read
	accountAccess[toolHideFromProgress] = Write
	accountAccess[toolUnhideFromProgress] = Write

	accountCaseSets = append(accountCaseSets, upNextCases)
}

var upNextCases = []accountCase{
	{
		// limit 2 matches the fixture's two entries, so one request fills the call.
		tool: toolFetchUpNext, args: map[string]any{argIncludeStats: true, fieldLimit: 2}, route: routeUpNext, fixture: "up_next",
		want: map[string]any{
			pathFirstShowTitle: testShowTitle, "items.0.aired": 62, "items.0.completed": 7,
			"items.0.last_watched_at": testWatchedAt, "items.0.next_episode.season": 2, "items.0.next_episode.number": 1,
			"items.0.next_episode.title": "Seven Thirty-Seven", "items.0.last_episode.number": 7,
			"items.0.stats.minutes_left": 2475, "items.0.stats.play_count": 7,
			"items.1.show.ids.trakt": 59660, "items.1.reset_at": "2024-02-01T00:00:00.000Z",
		},
		absent: []string{"items.0.reset_at", "items.1.next_episode", "items.1.last_episode", "items.1.stats", "items.0.show.homepage", "items.0.next_episode.images"},
	},
	{
		tool: toolFetchHiddenProgress, args: map[string]any{}, route: routeHiddenProgress, fixture: "hidden_progress",
		want: map[string]any{
			pathFirstItemType: itemShow, "items.0.hidden_at": testWatchedAt, pathFirstShowTitle: testShowTitle,
			"items.1.show.ids.trakt": 59660,
		},
		absent: []string{"items.1.hidden_at", "items.0.show.homepage", "items.0.season"},
	},
	{
		tool: toolHideFromProgress, args: itemsArg(itemArg(itemShow, testShowSlug), itemArg(itemSeason, testSeasonTraktID), itemArg(itemShow, testUnknownIMDb)),
		route: routeHideFromProgress, status: http.StatusCreated, fixture: "hidden_progress_add", object: true,
		want:   map[string]any{keyAdded: 2, keyDeleted: 0, "not_found.0.type": itemShow},
		absent: []string{"not_found.1"},
	},
	{
		tool: toolUnhideFromProgress, args: itemsArg(itemArg(itemShow, testShowSlug), itemArg(itemSeason, testSeasonTraktID)),
		route: routeUnhideFromProgress, fixture: "hidden_progress_remove", object: true,
		want:   map[string]any{keyDeleted: 2, keyAdded: 0, keyNotFound: []any{}},
		absent: []string{"not_found.0"},
	},
}

// TestUpNextIgnoresPlaceholderPageCount checks that auto-paging follows pages
// until an empty one although Trakt claims the list has a single page.
func TestUpNextIgnoresPlaceholderPageCount(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	var (
		mu    sync.Mutex
		pages []string
	)

	headers := map[string]string{headerPageCountName: placeholderPageCount, headerItemCountName: placeholderPageCount}
	full := fixture(t, "up_next")

	h.trakt.handle(routeUpNext, func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get(fieldPage)

		mu.Lock()

		pages = append(pages, page)

		mu.Unlock()

		body := emptyList
		if page == "1" || page == "2" {
			body = full
		}

		for k, v := range headers {
			w.Header().Set(k, v)
		}

		w.Header().Set("Content-Type", "application/json")

		if _, err := w.Write([]byte(body)); err != nil {
			panic(err)
		}
	})

	var out Listing[UpNextEntry]

	h.ok(toolFetchUpNext, map[string]any{}, &out)

	if len(out.Items) != 4 {
		t.Errorf("items = %d, want 4 (two full pages)", len(out.Items))
	}

	mu.Lock()
	defer mu.Unlock()

	if strings.Join(pages, ",") != "1,2,3" {
		t.Errorf("pages requested = %v, want 1,2,3 (stop on the empty page)", pages)
	}
}

// TestUpNextAutoPagingIsBounded checks a Trakt that never sends an empty page
// costs at most maxAutoPages requests.
func TestUpNextAutoPagingIsBounded(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond(routeUpNext, http.StatusOK, fixture(t, "up_next"), nil)

	var out Listing[UpNextEntry]

	h.ok(toolFetchUpNext, map[string]any{fieldLimit: maxLimit}, &out)

	if got := len(h.trakt.seen()); got != maxAutoPages {
		t.Errorf("Trakt requests = %d, want %d", got, maxAutoPages)
	}
}

// TestUpNextSinglePageReportsUnknownTotals checks the placeholder totals are
// never returned to the caller.
func TestUpNextSinglePageReportsUnknownTotals(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond(routeUpNext, http.StatusOK, fixture(t, "up_next"),
		map[string]string{headerPageCountName: placeholderPageCount, headerItemCountName: placeholderPageCount})

	var out Listing[UpNextEntry]

	h.ok(toolFetchUpNext, map[string]any{fieldPage: 2, fieldLimit: 5}, &out)

	if out.Page == nil || out.Page.Page != 2 || out.Page.Limit != 5 || out.Page.PageCount != 0 || out.Page.ItemCount != 0 {
		t.Errorf("page = %+v, want page 2 limit 5 and totals 0", out.Page)
	}

	if len(out.Items) != 2 {
		t.Errorf("items = %d, want 2: a page past the placeholder count is still returned", len(out.Items))
	}
}

func TestUpNextArguments(t *testing.T) {
	t.Parallel()

	runArgCases(t, []argCase{
		{
			name: "defaults", tool: toolFetchUpNext, args: map[string]any{}, route: routeUpNext, body: emptyList,
			wantQuery: map[string]string{fieldLimit: strconv.Itoa(defaultLimit), queryExtended: extendedFull},
			noQuery:   []string{argSortBy, argSortHow, argIncludeStats},
		},
		{
			name: "all options", tool: toolFetchUpNext,
			args:  map[string]any{argSort: testSortLastWatched, argSortHow: sortHowDesc, argIncludeStats: true},
			route: routeUpNext, body: emptyList,
			wantQuery: map[string]string{argSortBy: testSortLastWatched, argSortHow: sortHowDesc, argIncludeStats: queryTrue},
		},
		{
			name: "include stats false is sent", tool: toolFetchUpNext, args: map[string]any{argIncludeStats: false},
			route: routeUpNext, body: emptyList, wantQuery: map[string]string{argIncludeStats: queryFalse},
		},
		{name: "sort x is passed through", tool: toolFetchUpNext, args: map[string]any{argSort: "x"}, route: routeUpNext, body: emptyList, wantQuery: map[string]string{argSortBy: "x"}},
		{name: "sort injection", tool: toolFetchUpNext, args: map[string]any{argSort: "a&b=1"}, wantErr: invalid(fieldSort)},
		{name: "sort uppercase", tool: toolFetchUpNext, args: map[string]any{argSort: "Added"}, wantErr: invalid(fieldSort)},
		{name: "sort path traversal", tool: toolFetchUpNext, args: map[string]any{argSort: traversalID}, wantErr: invalid(fieldSort)},
		{name: "sort at max length", tool: toolFetchUpNext, args: map[string]any{argSort: strings.Repeat("a", maxSortByLen)}, route: routeUpNext, body: emptyList},
		{name: "sort over max length", tool: toolFetchUpNext, args: map[string]any{argSort: strings.Repeat("a", maxSortByLen+1)}, wantErr: invalid(fieldSort)},
		{name: "sort_how unknown", tool: toolFetchUpNext, args: map[string]any{argSortHow: "up"}, wantErr: invalid(fieldSortHow)},
		{name: "sort_how wrong case", tool: toolFetchUpNext, args: map[string]any{argSortHow: "DESC"}, wantErr: invalid(fieldSortHow)},
		{name: "limit 101", tool: toolFetchUpNext, args: map[string]any{fieldLimit: maxLimit + 1}, wantErr: invalid(fieldLimit)},
		{name: "page 0", tool: toolFetchUpNext, args: map[string]any{fieldPage: 0}, wantErr: invalid(fieldPage)},
	})
}

func TestHiddenProgressArguments(t *testing.T) {
	t.Parallel()

	runArgCases(t, []argCase{
		{
			name: "default type", tool: toolFetchHiddenProgress, args: map[string]any{}, route: routeHiddenProgress, body: emptyList,
			wantQuery: map[string]string{fieldType: itemShow, fieldLimit: strconv.Itoa(defaultLimit)},
		},
		{name: "type show", tool: toolFetchHiddenProgress, args: map[string]any{argType: itemShow}, route: routeHiddenProgress, body: emptyList, wantQuery: map[string]string{fieldType: itemShow}},
		{name: "type season is not offered", tool: toolFetchHiddenProgress, args: map[string]any{argType: itemSeason}, wantErr: invalid(fieldType)},
		{name: "type movie", tool: toolFetchHiddenProgress, args: map[string]any{argType: itemMovie}, wantErr: invalid(fieldType)},
		{name: "limit 101", tool: toolFetchHiddenProgress, args: map[string]any{fieldLimit: maxLimit + 1}, wantErr: invalid(fieldLimit)},
		{name: "page 0", tool: toolFetchHiddenProgress, args: map[string]any{fieldPage: 0}, wantErr: invalid(fieldPage)},
	})
}

func manyShows(n int) map[string]any {
	items := make([]map[string]any, 0, n)
	for i := range n {
		items = append(items, itemArg(itemShow, strconv.Itoa(i+1)))
	}

	return itemsArg(items...)
}

func TestProgressItemsArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{}

	for _, tt := range []struct{ tool, route string }{
		{toolHideFromProgress, routeHideFromProgress},
		{toolUnhideFromProgress, routeUnhideFromProgress},
	} {
		cases = append(cases,
			argCase{name: tt.tool + " movie", tool: tt.tool, args: itemsArg(itemArg(itemMovie, testMovieSlug)), wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " episode", tool: tt.tool, args: itemsArg(itemArg(itemEpisode, testEpisodeTraktID)), wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " show then movie", tool: tt.tool, args: itemsArg(itemArg(itemShow, testShowSlug), itemArg(itemMovie, testMovieSlug)), wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " season slug", tool: tt.tool, args: itemsArg(itemArg(itemSeason, "breaking-bad-season-1")), wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " empty id", tool: tt.tool, args: itemsArg(itemArg(itemShow, "")), wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " traversal id", tool: tt.tool, args: itemsArg(itemArg(itemShow, traversalID)), wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " 0 items", tool: tt.tool, args: map[string]any{argItems: []any{}}, wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " 51 items", tool: tt.tool, args: manyShows(maxBatchItems + 1), wantErr: invalid(fieldItems)},
			argCase{name: tt.tool + " 50 items", tool: tt.tool, args: manyShows(maxBatchItems), route: tt.route, body: emptyObject},
			argCase{name: tt.tool + " show and season", tool: tt.tool, args: itemsArg(itemArg(itemShow, testShowSlug), itemArg(itemSeason, testSeasonTraktID)), route: tt.route, body: emptyObject},
		)
	}

	runArgCases(t, cases)
}

// TestProgressWritesSendShowsAndSeasons checks the request bodies.
func TestProgressWritesSendShowsAndSeasons(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ tool, route string }{
		{toolHideFromProgress, routeHideFromProgress},
		{toolUnhideFromProgress, routeUnhideFromProgress},
	} {
		t.Run(tt.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			log := captureBodies(h, tt.route, http.StatusOK, emptyObject)

			h.ok(tt.tool, itemsArg(itemArg(itemShow, testShowSlug), itemArg(itemSeason, testSeasonTraktID)), nil)

			bodies := log.all()
			if len(bodies) != 1 {
				t.Fatalf("bodies = %d, want 1", len(bodies))
			}

			got := decodeSync(t, bodies[0])
			if len(got.Shows) != 1 || got.Shows[0].IDs.Slug != testShowSlug || len(got.Seasons) != 1 || got.Seasons[0].IDs.Trakt != 3572 ||
				len(got.Movies) != 0 || len(got.Episodes) != 0 {
				t.Errorf("body = %s, want one show and one season", bodies[0])
			}
		})
	}
}

func TestUpNextTraktFailures(t *testing.T) {
	t.Parallel()

	both := itemsArg(itemArg(itemShow, testShowSlug))

	runTraktFailureMatrix(t, []failureTool{
		{toolFetchUpNext, map[string]any{}, routeUpNext, true},
		{toolFetchHiddenProgress, map[string]any{}, routeHiddenProgress, true},
		{toolHideFromProgress, both, routeHideFromProgress, true},
		{toolUnhideFromProgress, both, routeUnhideFromProgress, true},
	})
}

func TestUpNextDescriptions(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for _, name := range []string{toolFetchUpNext, toolFetchHiddenProgress, toolHideFromProgress, toolUnhideFromProgress} {
		if d := tools[name].Description; !strings.Contains(d, "TV show") {
			t.Errorf("%s description %q does not say TV show", name, d)
		}
	}

	for _, name := range []string{toolHideFromProgress, toolUnhideFromProgress} {
		if d := tools[name].Description; !strings.Contains(d, "history") {
			t.Errorf("%s description %q does not say history is untouched", name, d)
		}
	}

	if d := tools[toolFetchUpNext].Description; !strings.Contains(d, "until items is empty") || !strings.Contains(d, "page.page_count and page.item_count are 0") {
		t.Errorf("list_up_next description %q does not say to page until empty", d)
	}
}
