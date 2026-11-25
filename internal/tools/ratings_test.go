package tools

import (
	"net/http"
	"strconv"
	"testing"
)

const (
	toolFetchRatings  = "list_user_ratings"
	toolAddRatings    = "add_ratings"
	toolRemoveRatings = "remove_ratings"

	routeSyncRatings       = "/sync/ratings"
	routeSyncRatingsRemove = "/sync/ratings/remove"

	testRating = 8
)

func init() {
	accountCaseSets = append(accountCaseSets, ratingCases)
	batchTools = append(batchTools,
		batchTool{tool: toolAddRatings, route: "POST " + routeSyncRatings, extra: map[string]any{keyRating: testRating}},
		batchTool{tool: toolRemoveRatings, route: "POST " + routeSyncRatingsRemove},
	)
	accountAccess[toolFetchRatings] = Read
	accountAccess[toolAddRatings] = Write
	accountAccess[toolRemoveRatings] = Destroy
}

var ratingCases = []accountCase{
	{
		tool: toolFetchRatings, args: map[string]any{}, route: "GET " + routeSyncRatings, fixture: "sync_ratings",
		want: map[string]any{
			pathFirstRating: 10, "items.0.rated_at": testWatchedAt, pathFirstItemType: itemMovie, pathFirstMovieTitle: testMovieTitle,
			"items.1.rating": testRating, pathSecondType: itemEpisode, "items.1.episode.title": testEpisodeTitle, "items.1.show.title": testShowTitle,
		},
		absent: []string{pathFirstShow, "items.1.movie"},
	},
	{
		tool: toolAddRatings, args: merge(itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemMovie, testUnknownIMDb)), map[string]any{keyRating: testRating}),
		route: "POST " + routeSyncRatings, status: http.StatusCreated, fixture: "sync_add", object: true,
		want: map[string]any{keyAdded: 3, "not_found.0.type": itemMovie, "not_found.0.id": testUnknownIMDb},
	},
	{
		tool: toolRemoveRatings, args: itemsArg(itemArg(itemShow, testShowSlug)),
		route: "POST " + routeSyncRatingsRemove, fixture: fixtureSyncRemove, object: true,
		want: map[string]any{keyDeleted: 62, keyNotFound: []any{}},
	},
}

func TestRatingsUsesShortPathWhenUnfiltered(t *testing.T) {
	t.Parallel()

	cases := []argCase{
		{name: "no filter", tool: toolFetchRatings, args: map[string]any{}, route: "GET " + routeSyncRatings, body: emptyList, wantPath: routeSyncRatings},
		{
			name: "type", tool: toolFetchRatings, args: map[string]any{argType: mediaMovies},
			route: "GET /sync/ratings/movies", body: emptyList, wantPath: "/sync/ratings/movies",
		},
		{
			name: "explicit all", tool: toolFetchRatings, args: map[string]any{argType: mediaAll},
			route: "GET /sync/ratings/all", body: emptyList, wantPath: "/sync/ratings/all",
		},
		{
			name: "rating only", tool: toolFetchRatings, args: map[string]any{keyRating: maxRating},
			route: "GET /sync/ratings/all/10", body: emptyList, wantPath: "/sync/ratings/all/10",
		},
		{
			name: "type and rating", tool: toolFetchRatings, args: map[string]any{argType: mediaSeasons, keyRating: minRating},
			route: "GET /sync/ratings/seasons/1", body: emptyList, wantPath: "/sync/ratings/seasons/1",
		},
		{
			name: "paged", tool: toolFetchRatings, args: merge(pagedArgs, nil), route: "GET " + routeSyncRatings, body: emptyList,
			wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		},
		{name: "bad type", tool: toolFetchRatings, args: map[string]any{argType: itemMovie}, wantErr: invalid(fieldType)},
		{name: "rating 0", tool: toolFetchRatings, args: map[string]any{keyRating: 0}, wantErr: invalid(fieldRating)},
		{name: "rating 11", tool: toolFetchRatings, args: map[string]any{keyRating: maxRating + 1}, wantErr: invalid(fieldRating)},
	}

	runArgCases(t, cases)
}

func TestRatingRange(t *testing.T) {
	t.Parallel()

	for _, rating := range []int{minRating - 1, minRating, maxRating, maxRating + 1, -1} {
		t.Run(strconv.Itoa(rating), func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			args := merge(itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemEpisode, testEpisodeTraktID)), map[string]any{keyRating: rating})

			if rating < minRating || rating > maxRating {
				h.fail(toolAddRatings, args, invalid(fieldRating))

				if got := len(h.trakt.seen()); got != 0 {
					t.Errorf("Trakt requests = %d, want 0", got)
				}

				return
			}

			log := captureBodies(h, "POST "+routeSyncRatings, http.StatusCreated, emptyObject)
			h.ok(toolAddRatings, args, nil)

			got := decodeSync(t, log.all()[0])
			if len(got.Movies) != 1 || got.Movies[0].Rating == nil || *got.Movies[0].Rating != rating ||
				len(got.Episodes) != 1 || got.Episodes[0].Rating == nil || *got.Episodes[0].Rating != rating {
				t.Errorf("body = %+v, want rating %d on every item", got, rating)
			}
		})
	}

	t.Run("remove sends no rating", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		log := captureBodies(h, "POST "+routeSyncRatingsRemove, http.StatusOK, emptyObject)
		h.ok(toolRemoveRatings, itemsArg(itemArg(itemMovie, testMovieSlug)), nil)

		if got := decodeSync(t, log.all()[0]); len(got.Movies) != 1 || got.Movies[0].Rating != nil {
			t.Errorf("body = %+v, want one movie without rating", got)
		}
	})
}
