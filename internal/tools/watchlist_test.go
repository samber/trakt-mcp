package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolFetchWatchlist      = "fetch_watchlist"
	toolAddToWatchlist      = "add_to_watchlist"
	toolRemoveFromWatchlist = "remove_from_watchlist"

	routeSyncWatchlist       = "/sync/watchlist"
	routeSyncWatchlistRemove = "/sync/watchlist/remove"
	routeWatchlistDefault    = "GET /sync/watchlist/all/rank/asc"

	argNotes   = "notes"
	argSortHow = "sort_how"
)

func init() {
	accountCaseSets = append(accountCaseSets, watchlistCases)
	batchTools = append(batchTools,
		batchTool{tool: toolAddToWatchlist, route: "POST " + routeSyncWatchlist},
		batchTool{tool: toolRemoveFromWatchlist, route: "POST " + routeSyncWatchlistRemove},
	)
	accountAccess[toolFetchWatchlist] = Read
	accountAccess[toolAddToWatchlist] = Write
	accountAccess[toolRemoveFromWatchlist] = Destroy
}

var watchlistCases = []accountCase{
	{
		tool: toolFetchWatchlist, args: map[string]any{}, route: routeWatchlistDefault, fixture: "sync_watchlist",
		want: map[string]any{
			"items.0.rank": 1, "items.0.list_item_id": 101, "items.1.list_item_id": 102, "items.0.listed_at": testWatchedAt, pathFirstItemType: itemMovie, "items.0.notes": "Watch with friends",
			pathFirstMovieTitle: testMovieTitle, pathSecondType: itemSeason, "items.1.season.number": 1, "items.1.show.title": testShowTitle,
			"items.2.episode.number": 1, "items.2.show.ids.slug": testShowSlug,
		},
		absent: []string{"items.1.notes", pathFirstShow},
	},
	{
		tool: toolAddToWatchlist, args: itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemShow, testShowSlug)),
		route: "POST " + routeSyncWatchlist, status: http.StatusCreated, fixture: "sync_watchlist_add", object: true,
		want: map[string]any{keyAdded: 1, keyExisting: 1, keyDeleted: 0, keyNotFound: []any{}},
	},
	{
		tool: toolRemoveFromWatchlist, args: itemsArg(itemArg(itemShow, testShowSlug)),
		route: "POST " + routeSyncWatchlistRemove, fixture: fixtureSyncRemove, object: true,
		want: map[string]any{keyDeleted: 62, keyNotFound: []any{}},
	},
}

func TestWatchlistSortEnumsValidated(t *testing.T) {
	t.Parallel()

	// The eight sorts of spec §8.4, in its order.
	if got := strings.Join(watchlistSorts, ","); got != "rank,added,title,released,runtime,popularity,percentage,votes" {
		t.Fatalf("watchlist sorts = %s", got)
	}

	cases := []argCase{}

	for _, sort := range watchlistSorts {
		path := "/sync/watchlist/all/" + sort + "/asc"
		cases = append(cases, argCase{
			name: "sort " + sort, tool: toolFetchWatchlist, args: map[string]any{fieldSort: sort},
			route: "GET " + path, body: emptyList, wantPath: path,
		})
	}

	for _, typ := range []string{mediaMovies, mediaShows, mediaSeasons, mediaEpisodes} {
		path := "/sync/watchlist/" + typ + "/added/desc"
		cases = append(cases, argCase{
			name: "type " + typ, tool: toolFetchWatchlist, args: map[string]any{argType: typ, fieldSort: "added", argSortHow: "desc"},
			route: "GET " + path, body: emptyList, wantPath: path,
		})
	}

	cases = append(cases,
		argCase{name: "bad sort", tool: toolFetchWatchlist, args: map[string]any{fieldSort: keyRating}, wantErr: invalid(fieldSort)},
		argCase{name: "sort traversal", tool: toolFetchWatchlist, args: map[string]any{fieldSort: "rank/../x"}, wantErr: invalid(fieldSort)},
		argCase{name: "bad type", tool: toolFetchWatchlist, args: map[string]any{argType: itemMovie}, wantErr: invalid(fieldType)},
		argCase{name: "bad direction", tool: toolFetchWatchlist, args: map[string]any{argSortHow: "up"}, wantErr: invalid(fieldSortHow)},
		argCase{name: "upper direction", tool: toolFetchWatchlist, args: map[string]any{argSortHow: "ASC"}, wantErr: invalid(fieldSortHow)},
		argCase{
			name: "paged", tool: toolFetchWatchlist, args: merge(pagedArgs, nil), route: routeWatchlistDefault, body: emptyList,
			wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		},
	)

	runArgCases(t, cases)
}

func TestWatchlistNotes(t *testing.T) {
	t.Parallel()

	route := "POST " + routeSyncWatchlist

	t.Run("sent per item", func(t *testing.T) {
		t.Parallel()

		notes := strings.Repeat("é", maxNotesRunes)
		h := newHarness(t)
		log := captureBodies(h, route, http.StatusCreated, emptyObject)

		h.ok(toolAddToWatchlist, merge(itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemShow, testShowSlug)), map[string]any{argNotes: notes}), nil)

		got := decodeSync(t, log.all()[0])
		if len(got.Movies) != 1 || got.Movies[0].Notes != notes || len(got.Shows) != 1 || got.Shows[0].Notes != notes {
			t.Errorf("body = %+v, want the notes on both items", got)
		}
	})

	t.Run("too long", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.fail(toolAddToWatchlist, merge(itemsArg(itemArg(itemMovie, testMovieSlug)), map[string]any{argNotes: strings.Repeat("a", maxNotesRunes+1)}), invalid(fieldNotes))

		if got := len(h.trakt.seen()); got != 0 {
			t.Errorf("Trakt requests = %d, want 0", got)
		}
	})

	t.Run("omitted", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		log := captureBodies(h, route, http.StatusCreated, emptyObject)

		h.ok(toolAddToWatchlist, itemsArg(itemArg(itemMovie, testMovieSlug)), nil)

		if strings.Contains(log.all()[0], argNotes) {
			t.Errorf("body %s carries notes", log.all()[0])
		}
	})
}

func TestWatchlistNotesNonVIPReturnsVIPError(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("POST "+routeSyncWatchlist, http.StatusUpgradeRequired, `{"error_description": "`+injectionMarker+`"}`, nil)

	h.fail(toolAddToWatchlist, merge(itemsArg(itemArg(itemMovie, testMovieSlug)), map[string]any{argNotes: "for later"}), msgVIPRequired)

	if got := len(h.trakt.seen()); got != 1 {
		t.Errorf("Trakt requests = %d, want 1 (no retry)", got)
	}

	if strings.Contains(h.logs.String(), injectionMarker) {
		t.Error("Trakt's message reached the logs")
	}
}

func TestAccountLimitError(t *testing.T) {
	t.Parallel()

	for _, bt := range []batchTool{
		{tool: toolAddToWatchlist, route: "POST " + routeSyncWatchlist},
		{tool: toolAddToCollection, route: "POST " + routeSyncCollection},
	} {
		t.Run(bt.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(bt.route, statusTraktAccountLimit, `{"error_description": "limit"}`, nil)

			h.fail(bt.tool, itemsArg(itemArg(itemMovie, testMovieSlug)), msgAccountLimit)

			if got := len(h.trakt.seen()); got != 1 {
				t.Errorf("Trakt requests = %d, want 1 (no retry)", got)
			}
		})
	}
}
