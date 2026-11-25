package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolFetchFavorites      = "fetch_favorites"
	toolAddToFavorites      = "add_to_favorites"
	toolUpdateFavorite      = "update_favorite"
	toolReorderFavorites    = "reorder_favorites"
	toolRemoveFromFavorites = "remove_from_favorites"

	routeSyncFavorites       = "/sync/favorites"
	routeSyncFavoritesRemove = "/sync/favorites/remove"
	routeFavoritesDefault    = "GET /sync/favorites/all/rank/asc"
	routeFavoriteUpdate      = "PUT /sync/favorites/201"
	routeFavoritesReorder    = "POST /sync/favorites/reorder"

	// testFavoriteID is the id of the first item of the sync_favorites fixture.
	testFavoriteID = 201
	// testFavoriteRank is a rank sent to update_favorite.
	testFavoriteRank = 3

	// Output paths, result keys and case names this file repeats; named so
	// goconst does not count them against the other test files.
	pathSecondShowTitle = "items.1.show.title"
	keyItemCount        = "item_count"
	keyUpdatedCount     = "updated"
	keyUpdatedAt        = "updated_at"
	caseNamePaged       = "paged"
	caseNameEmpty       = "empty"
)

func init() {
	accountCaseSets = append(accountCaseSets, favoriteCases)
	batchTools = append(batchTools,
		batchTool{tool: toolAddToFavorites, route: "POST " + routeSyncFavorites},
		batchTool{tool: toolRemoveFromFavorites, route: "POST " + routeSyncFavoritesRemove},
	)
	accountAccess[toolFetchFavorites] = Read
	accountAccess[toolAddToFavorites] = Write
	accountAccess[toolUpdateFavorite] = Write
	accountAccess[toolReorderFavorites] = Write
	accountAccess[toolRemoveFromFavorites] = Destroy
}

var favoriteCases = []accountCase{
	{
		tool: toolFetchFavorites, args: map[string]any{}, route: routeFavoritesDefault, fixture: "sync_favorites",
		want: map[string]any{
			"items.0.rank": 1, "items.0.list_item_id": testFavoriteID, "items.0.notes": "Desert island pick",
			pathFirstItemType: itemMovie, pathFirstMovieTitle: testMovieTitle, pathSecondType: itemShow,
			pathSecondShowTitle: testShowTitle, "items.1.list_item_id": 202,
		},
		absent: []string{"items.1.notes"},
	},
	{
		tool: toolAddToFavorites, args: itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemShow, testShowSlug)),
		route: "POST " + routeSyncFavorites, status: http.StatusCreated, fixture: "sync_watchlist_add", object: true,
		want: map[string]any{keyAdded: 1, keyExisting: 1, keyDeleted: 0, keyNotFound: []any{}},
	},
	{
		tool: toolUpdateFavorite, args: map[string]any{argListItemID: testFavoriteID, argNotes: "Rewatch yearly"},
		route: routeFavoriteUpdate, status: http.StatusNoContent, object: true, want: map[string]any{"updated": true},
	},
	{
		tool: toolReorderFavorites, args: map[string]any{argRank: []int{3, 1, 2}},
		route: routeFavoritesReorder, status: http.StatusOK, fixture: "sync_watchlist_reorder", object: true,
		want: map[string]any{keyUpdatedCount: 3, "skipped_ids.0": 9, keyItemCount: 3, keyUpdatedAt: testWatchedAt},
	},
	{
		tool: toolRemoveFromFavorites, args: itemsArg(itemArg(itemShow, testShowSlug)),
		route: "POST " + routeSyncFavoritesRemove, fixture: fixtureSyncRemove, object: true,
		want: map[string]any{keyDeleted: 62, keyNotFound: []any{}},
	},
}

func TestFavoriteFetchArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{}

	for _, sort := range watchlistSorts {
		path := "/sync/favorites/all/" + sort + "/asc"
		cases = append(cases, argCase{
			name: "sort " + sort, tool: toolFetchFavorites, args: map[string]any{fieldSort: sort},
			route: "GET " + path, body: emptyList, wantPath: path,
		})
	}

	for _, typ := range []string{mediaMovies, mediaShows} {
		path := "/sync/favorites/" + typ + "/added/desc"
		cases = append(cases, argCase{
			name: "type " + typ, tool: toolFetchFavorites, args: map[string]any{argType: typ, fieldSort: "added", argSortHow: "desc"},
			route: "GET " + path, body: emptyList, wantPath: path,
		})
	}

	cases = append(cases,
		argCase{name: "seasons not allowed", tool: toolFetchFavorites, args: map[string]any{argType: mediaSeasons}, wantErr: invalid(fieldType)},
		argCase{name: "episodes not allowed", tool: toolFetchFavorites, args: map[string]any{argType: mediaEpisodes}, wantErr: invalid(fieldType)},
		argCase{name: "bad sort", tool: toolFetchFavorites, args: map[string]any{fieldSort: keyRating}, wantErr: invalid(fieldSort)},
		argCase{name: "sort traversal", tool: toolFetchFavorites, args: map[string]any{fieldSort: testSortTraversal}, wantErr: invalid(fieldSort)},
		argCase{name: "bad direction", tool: toolFetchFavorites, args: map[string]any{argSortHow: "up"}, wantErr: invalid(fieldSortHow)},
		argCase{
			name: caseNamePaged, tool: toolFetchFavorites, args: merge(pagedArgs, nil), route: routeFavoritesDefault, body: emptyList,
			wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		},
	)

	runArgCases(t, cases)
}

func TestFavoriteItemArguments(t *testing.T) {
	t.Parallel()

	route := "POST " + routeSyncFavorites
	removeRoute := "POST " + routeSyncFavoritesRemove
	notesAt := strings.Repeat("é", maxNotesRunes)
	notesOver := strings.Repeat("a", maxNotesRunes+1)
	one := itemsArg(itemArg(itemMovie, testMovieSlug))

	cases := []argCase{
		{name: "add season rejected", tool: toolAddToFavorites, args: itemsArg(itemArg(itemSeason, testSeasonTraktID)), wantErr: invalid(fieldItems)},
		{name: "add episode rejected", tool: toolAddToFavorites, args: itemsArg(itemArg(itemEpisode, testEpisodeTraktID)), wantErr: invalid(fieldItems)},
		{name: "add mixed with episode rejected", tool: toolAddToFavorites, args: itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemEpisode, testEpisodeTraktID)), wantErr: invalid(fieldItems)},
		{name: "remove season rejected", tool: toolRemoveFromFavorites, args: itemsArg(itemArg(itemSeason, testSeasonTraktID)), wantErr: invalid(fieldItems)},
		{name: "remove episode rejected", tool: toolRemoveFromFavorites, args: itemsArg(itemArg(itemEpisode, testEpisodeTraktID)), wantErr: invalid(fieldItems)},
		{name: "add zero items", tool: toolAddToFavorites, args: itemsArg(), wantErr: invalid(fieldItems)},
		{name: "add 51 items", tool: toolAddToFavorites, args: manyMovies(maxBatchItems + 1), wantErr: invalid(fieldItems)},
		{name: "add 50 items", tool: toolAddToFavorites, args: manyMovies(maxBatchItems), route: route, body: emptyObject},
		{name: "remove zero items", tool: toolRemoveFromFavorites, args: itemsArg(), wantErr: invalid(fieldItems)},
		{name: "remove 51 items", tool: toolRemoveFromFavorites, args: manyMovies(maxBatchItems + 1), wantErr: invalid(fieldItems)},
		{name: "remove 50 items", tool: toolRemoveFromFavorites, args: manyMovies(maxBatchItems), route: removeRoute, body: emptyObject},
		{name: "notes at limit", tool: toolAddToFavorites, args: merge(one, map[string]any{argNotes: notesAt}), route: route, body: emptyObject},
		{name: "notes over limit", tool: toolAddToFavorites, args: merge(one, map[string]any{argNotes: notesOver}), wantErr: invalid(fieldNotes)},
	}

	runArgCases(t, cases)
}

func TestFavoriteUpdateArguments(t *testing.T) {
	t.Parallel()

	update := func(id int, extra map[string]any) map[string]any {
		return merge(map[string]any{argListItemID: id}, extra)
	}

	cases := []argCase{
		{name: "neither notes nor rank", tool: toolUpdateFavorite, args: update(testFavoriteID, nil), wantErr: invalid(fieldNotesOrRank)},
		{name: "id zero", tool: toolUpdateFavorite, args: update(0, map[string]any{argNotes: "x"}), wantErr: invalid(fieldListItemID)},
		{name: "id negative", tool: toolUpdateFavorite, args: update(-1, map[string]any{argNotes: "x"}), wantErr: invalid(fieldListItemID)},
		{name: "notes over limit", tool: toolUpdateFavorite, args: update(testFavoriteID, map[string]any{argNotes: strings.Repeat("a", maxNotesRunes+1)}), wantErr: invalid(fieldNotes)},
		{name: "notes at limit", tool: toolUpdateFavorite, args: update(testFavoriteID, map[string]any{argNotes: strings.Repeat("é", maxNotesRunes)}), route: routeFavoriteUpdate, body: ""},
		{name: "rank zero", tool: toolUpdateFavorite, args: update(testFavoriteID, map[string]any{argRank: 0}), wantErr: invalid(fieldRank)},
		{name: "rank negative", tool: toolUpdateFavorite, args: update(testFavoriteID, map[string]any{argRank: -1}), wantErr: invalid(fieldRank)},
		{name: "rank only", tool: toolUpdateFavorite, args: update(testFavoriteID, map[string]any{argRank: testFavoriteRank}), route: routeFavoriteUpdate, body: ""},
		{name: "empty notes alone clear the note", tool: toolUpdateFavorite, args: update(testFavoriteID, map[string]any{argNotes: ""}), route: routeFavoriteUpdate, body: ""},
	}

	runArgCases(t, cases)
}

func TestFavoriteReorderArguments(t *testing.T) {
	t.Parallel()

	ranks := func(n int) []int {
		ids := make([]int, n)
		for i := range ids {
			ids[i] = i + 1
		}

		return ids
	}
	reorder := func(ids []int) map[string]any { return map[string]any{argRank: ids} }

	cases := []argCase{
		{name: caseNameEmpty, tool: toolReorderFavorites, args: reorder([]int{}), wantErr: invalid(fieldRank)},
		{name: "duplicate", tool: toolReorderFavorites, args: reorder([]int{3, 1, 3}), wantErr: invalid(fieldRank)},
		{name: "zero id", tool: toolReorderFavorites, args: reorder([]int{1, 0}), wantErr: invalid(fieldRank)},
		{name: "negative id", tool: toolReorderFavorites, args: reorder([]int{1, -2}), wantErr: invalid(fieldRank)},
		{name: "over limit", tool: toolReorderFavorites, args: reorder(ranks(maxReorderIDs + 1)), wantErr: invalid(fieldRank)},
		{name: "at limit", tool: toolReorderFavorites, args: reorder(ranks(maxReorderIDs)), route: routeFavoritesReorder, body: testReorderReply},
		{name: "one id", tool: toolReorderFavorites, args: reorder([]int{7}), route: routeFavoritesReorder, body: testReorderReply},
	}

	runArgCases(t, cases)
}

func TestFavoriteUpdateBodies(t *testing.T) {
	t.Parallel()

	t.Run("update sends only the given fields", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateFavorite, map[string]any{argListItemID: testFavoriteID, argRank: testFavoriteRank}, routeFavoriteUpdate, http.StatusNoContent, "")
		if len(got) != 1 || got[argRank] != float64(testFavoriteRank) {
			t.Errorf("body = %v, want only rank %d", got, testFavoriteRank)
		}
	})

	t.Run("update sends notes and rank", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateFavorite, map[string]any{argListItemID: testFavoriteID, argNotes: "n", argRank: testFavoriteRank}, routeFavoriteUpdate, http.StatusNoContent, "")
		if len(got) != 2 || got[argNotes] != "n" || got[argRank] != float64(testFavoriteRank) {
			t.Errorf("body = %v, want notes and rank", got)
		}
	})

	t.Run("empty notes are sent to clear", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateFavorite, map[string]any{argListItemID: testFavoriteID, argNotes: ""}, routeFavoriteUpdate, http.StatusNoContent, "")
		if v, ok := got[argNotes]; !ok || v != nil {
			t.Errorf("body = %v, want notes null sent", got)
		}
	})
}

func TestFavoriteReorderAndAddBodies(t *testing.T) {
	t.Parallel()

	t.Run("reorder sends the ids in order", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolReorderFavorites, map[string]any{argRank: []int{3, 1, 2}}, routeFavoritesReorder, http.StatusOK, testReorderReply)

		ids, ok := got[argRank].([]any)
		if !ok || len(got) != 1 || len(ids) != 3 || ids[0] != 3.0 || ids[1] != 1.0 || ids[2] != 2.0 {
			t.Errorf("body = %v, want rank [3 1 2]", got)
		}
	})

	t.Run("add sends notes on every item", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		log := captureBodies(h, "POST "+routeSyncFavorites, http.StatusCreated, emptyObject)

		h.ok(toolAddToFavorites, merge(itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemShow, testShowSlug)), map[string]any{argNotes: "top"}), nil)

		got := decodeSync(t, log.all()[0])
		if len(got.Movies) != 1 || got.Movies[0].Notes != "top" || len(got.Shows) != 1 || got.Shows[0].Notes != "top" {
			t.Errorf("body = %+v, want the notes on both items", got)
		}
	})
}

func TestFavoriteAccountLimit(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("POST "+routeSyncFavorites, statusTraktAccountLimit, `{"error_description": "limit"}`, nil)

	h.fail(toolAddToFavorites, itemsArg(itemArg(itemMovie, testMovieSlug)), msgAccountLimit)

	if got := len(h.trakt.seen()); got != 1 {
		t.Errorf("Trakt requests = %d, want 1 (no retry)", got)
	}
}

func TestFavoriteTraktFailures(t *testing.T) {
	t.Parallel()

	runTraktFailureMatrix(t, []failureTool{
		{toolFetchFavorites, map[string]any{}, routeFavoritesDefault, true},
		{toolAddToFavorites, itemsArg(itemArg(itemMovie, testMovieSlug)), "POST " + routeSyncFavorites, true},
		{toolUpdateFavorite, map[string]any{argListItemID: testFavoriteID, argNotes: "x"}, routeFavoriteUpdate, false},
		{toolReorderFavorites, map[string]any{argRank: []int{2, 1}}, routeFavoritesReorder, true},
		{toolRemoveFromFavorites, itemsArg(itemArg(itemMovie, testMovieSlug)), "POST " + routeSyncFavoritesRemove, true},
	})
}

func TestFavoriteDescriptions(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for _, name := range []string{toolFetchFavorites, toolAddToFavorites, toolUpdateFavorite, toolReorderFavorites, toolRemoveFromFavorites} {
		if d := tools[name].Description; !strings.Contains(d, "favorite") {
			t.Errorf("%s description %q does not mention favorites", name, d)
		}
	}

	if d := tools[toolFetchFavorites].Description; !strings.Contains(d, untrustedNote) {
		t.Error("fetch_favorites description lacks untrustedNote")
	}

	if d := tools[toolAddToFavorites].Description; !strings.Contains(d, "420") {
		t.Errorf("add_to_favorites description %q does not mention the 420 cap", d)
	}
}
