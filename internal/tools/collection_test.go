package tools

import (
	"net/http"
	"testing"
	"time"
)

const (
	toolFetchCollection      = "fetch_collection"
	toolAddToCollection      = "add_to_collection"
	toolRemoveFromCollection = "remove_from_collection"

	routeSyncCollection       = "/sync/collection"
	routeSyncCollectionRemove = "/sync/collection/remove"

	argCollectedAt = "collected_at"

	// statusTraktAccountLimit is Trakt's status for a capped list,
	// watchlist or collection.
	statusTraktAccountLimit = 420
)

func init() {
	accountCaseSets = append(accountCaseSets, collectionCases)
	batchTools = append(batchTools,
		batchTool{tool: toolAddToCollection, route: "POST " + routeSyncCollection},
		batchTool{tool: toolRemoveFromCollection, route: "POST " + routeSyncCollectionRemove},
	)
	accountAccess[toolFetchCollection] = Read
	accountAccess[toolAddToCollection] = Write
	accountAccess[toolRemoveFromCollection] = Destroy
}

var collectionCases = []accountCase{
	{
		tool: toolFetchCollection, args: map[string]any{argType: mediaMovies}, route: "GET /sync/collection/movies", fixture: "sync_collection_movies",
		want: map[string]any{
			"items.0.collected_at": testWatchedAt, "items.0.updated_at": testWatchedAt, pathFirstMovieTitle: testMovieTitle,
			"items.0.movie.genres": []any{},
		},
		absent: []string{"items.0.collected_episodes", pathFirstShow, "items.1"},
	},
	{
		tool: toolFetchCollection, args: map[string]any{argType: mediaShows}, route: "GET /sync/collection/shows", fixture: "sync_collection_shows",
		want: map[string]any{
			"items.0.collected_at": testWatchedAt, "items.0.collected_episodes": 3, "items.0.show.title": testShowTitle,
		},
		absent: []string{"items.0.seasons", "items.0.movie"},
	},
	{
		tool: toolAddToCollection, args: itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemMovie, testUnknownIMDb)),
		route: "POST " + routeSyncCollection, status: http.StatusCreated, fixture: "sync_add", object: true,
		want: map[string]any{keyAdded: 3, "not_found.0.id": testUnknownIMDb},
	},
	{
		tool: toolRemoveFromCollection, args: itemsArg(itemArg(itemShow, testShowSlug)),
		route: "POST " + routeSyncCollectionRemove, fixture: fixtureSyncRemove, object: true,
		want: map[string]any{keyDeleted: 62},
	},
}

func TestCollectionArguments(t *testing.T) {
	t.Parallel()

	future := time.Now().Add(2 * maxFutureSkew).UTC().Format(time.RFC3339)

	runArgCases(t, []argCase{
		{name: "episodes", tool: toolFetchCollection, args: map[string]any{argType: mediaEpisodes}, route: "GET /sync/collection/episodes", body: emptyList},
		{name: "media", tool: toolFetchCollection, args: map[string]any{argType: mediaMedia}, route: "GET /sync/collection/media", body: emptyList},
		{
			name: "paged", tool: toolFetchCollection, args: merge(pagedArgs, map[string]any{argType: mediaMovies}),
			route: "GET /sync/collection/movies", body: emptyList, wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		},
		{name: "seasons", tool: toolFetchCollection, args: map[string]any{argType: mediaSeasons}, wantErr: invalid(fieldType)},
		{name: "empty type", tool: toolFetchCollection, args: map[string]any{argType: ""}, wantErr: invalid(fieldType)},
		{name: "traversal type", tool: toolFetchCollection, args: map[string]any{argType: "../movies"}, wantErr: invalid(fieldType)},
		{
			name: "future collected_at", tool: toolAddToCollection, args: merge(itemsArg(itemArg(itemMovie, testMovieSlug)), map[string]any{argCollectedAt: future}),
			wantErr: invalid(fieldCollectedAt),
		},
		{
			name: "unknown collected_at", tool: toolAddToCollection, args: merge(itemsArg(itemArg(itemMovie, testMovieSlug)), map[string]any{argCollectedAt: watchedUnknown}),
			wantErr: invalid(fieldCollectedAt),
		},
		{
			name: "season slug", tool: toolAddToCollection, args: itemsArg(itemArg(itemSeason, testShowSlug)),
			wantErr: invalid(fieldItems),
		},
	})
}

func TestCollectedAtSent(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	log := captureBodies(h, "POST "+routeSyncCollection, http.StatusCreated, emptyObject)

	h.ok(toolAddToCollection, merge(itemsArg(itemArg(itemEpisode, testEpisodeTraktID)), map[string]any{argCollectedAt: "2024-03-01T21:00:00+01:00"}), nil)

	got := decodeSync(t, log.all()[0])
	if len(got.Episodes) != 1 || got.Episodes[0].CollectedAt != testWatchedAt {
		t.Errorf("body = %+v, want collected_at %s", got, testWatchedAt)
	}
}
