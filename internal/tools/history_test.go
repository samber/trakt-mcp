package tools

import (
	"fmt"
	"net/http"
	"testing"
)

const (
	toolFetchUserWatched      = "fetch_user_watched"
	toolFetchHistory          = "fetch_history"
	toolFetchShowProgress     = "fetch_show_progress"
	toolFetchPlaybackProgress = "fetch_playback_progress"
	toolRemovePlaybackItem    = "remove_playback_item"

	argHistoryType   = "history_type"
	argItemID        = "item_id"
	argStartAt       = "start_at"
	argEndAt         = "end_at"
	argPlaybackType  = "playback_type"
	argPlaybackID    = "playback_id"
	argHidden        = "hidden"
	argSpecials      = "specials"
	argCountSpecials = "count_specials"
	argLastActivity  = "last_activity"
	argVerbose       = "verbose"

	routeShowProgress = "GET /shows/" + testShowSlug + "/progress/watched"
	testPlaybackID    = 13
)

func init() {
	accountCaseSets = append(accountCaseSets, historyCases)

	for _, name := range []string{toolFetchUserWatched, toolFetchHistory, toolFetchShowProgress, toolFetchPlaybackProgress} {
		accountAccess[name] = Read
	}

	accountAccess[toolRemovePlaybackItem] = Destroy
}

var historyCases = []accountCase{
	{
		tool: toolFetchUserWatched, args: map[string]any{argType: mediaMovies}, route: "GET /sync/watched/movies", fixture: "sync_watched_movies",
		want: map[string]any{
			"items.0.plays": 3, "items.0.last_watched_at": testWatchedAt, "items.0.last_updated_at": testWatchedAt,
			pathFirstMovieTitle: testMovieTitle, "items.0.movie.ids.slug": testMovieSlug, "items.0.movie.genres": []any{},
			"items.1.plays": 1, "items.1.movie.title": "Older",
		},
		absent: []string{pathFirstShow, "items.2"},
	},
	{
		tool: toolFetchHistory, args: merge(pagedArgs, nil), route: "GET " + routeSyncHistory, fixture: "sync_history", headers: pagedHeaders,
		want: map[string]any{
			"items.0.id": 1982346.0, "items.0.watched_at": testWatchedAt, "items.0.action": "scrobble", pathFirstItemType: "episode",
			"items.0.episode.season": 1, "items.0.episode.number": 1, "items.0.episode.title": testEpisodeTitle, "items.0.show.title": testShowTitle,
			pathSecondType: itemMovie, "items.1.movie.ids.trakt": 481, pathPageCount: 3,
		},
		absent: []string{"items.0.movie", "items.1.show", "items.1.episode"},
	},
	{
		tool: toolFetchShowProgress, args: showShow(testShowSlug), route: routeShowProgress, fixture: "show_progress", object: true,
		want: map[string]any{
			pathAired: 8, "completed": 6, "percentage": 75, "last_watched_at": testWatchedAt,
			"next_episode.season": 1, "next_episode.number": 7, "next_episode.title": "A No-Rough-Stuff-Type Deal",
			"last_episode.number": 6, "seasons.0.number": 1, "seasons.0.aired": 7, "seasons.0.completed": 6, "seasons.0.percentage": 85.7,
			"seasons.1.number": 2, "seasons.1.percentage": 0, "hidden_seasons": []any{5},
		},
		absent: []string{"seasons.0.episodes", "reset_at"},
	},
	{
		tool: toolFetchPlaybackProgress, args: map[string]any{}, route: "GET /sync/playback", fixture: "sync_playback",
		want: map[string]any{
			"items.0.playback_id": testPlaybackID, "items.0.progress": 10.5, "items.0.paused_at": testWatchedAt,
			pathFirstItemType: itemMovie, pathFirstMovieTitle: testMovieTitle,
			"items.1.playback_id": 37, pathSecondType: itemEpisode, "items.1.episode.number": 1, "items.1.show.title": testShowTitle,
		},
		absent: []string{"items.0.id", "items.0.episode"},
	},
	{
		tool: toolRemovePlaybackItem, args: map[string]any{argPlaybackID: testPlaybackID}, route: "DELETE /sync/playback/13", status: http.StatusNoContent, object: true,
		want: map[string]any{"removed": true, argPlaybackID: testPlaybackID},
	},
}

func TestHistoryUsesShortPathWhenUnfiltered(t *testing.T) {
	t.Parallel()

	runArgCases(t, []argCase{
		{name: "no filter", tool: toolFetchHistory, args: map[string]any{}, route: "GET " + routeSyncHistory, body: emptyList, wantPath: routeSyncHistory},
		{
			name: "type", tool: toolFetchHistory, args: map[string]any{argHistoryType: mediaMovies},
			route: "GET /sync/history/movies", body: emptyList, wantPath: "/sync/history/movies",
		},
		{
			name: "type and id", tool: toolFetchHistory, args: map[string]any{argHistoryType: mediaEpisodes, argItemID: testEpisodeTraktID},
			route: "GET /sync/history/episodes/" + testEpisodeTraktID, body: emptyList, wantPath: "/sync/history/episodes/" + testEpisodeTraktID,
		},
		{name: "id without type", tool: toolFetchHistory, args: map[string]any{argItemID: testMovieTraktID}, wantErr: invalid(fieldItemID)},
		{name: "slug id", tool: toolFetchHistory, args: map[string]any{argHistoryType: mediaMovies, argItemID: testMovieSlug}, wantErr: invalid(fieldItemID)},
		{name: "traversal id", tool: toolFetchHistory, args: map[string]any{argHistoryType: mediaMovies, argItemID: traversalID}, wantErr: invalid(fieldItemID)},
		{name: "bad type", tool: toolFetchHistory, args: map[string]any{argHistoryType: "movie"}, wantErr: invalid(fieldHistoryType)},
		{name: "all is not a type", tool: toolFetchHistory, args: map[string]any{argHistoryType: mediaAll}, wantErr: invalid(fieldHistoryType)},
		{name: "limit over max", tool: toolFetchHistory, args: map[string]any{fieldLimit: maxLimit + 1}, wantErr: invalid(fieldLimit)},
	})
}

// Expected start_at and end_at values sent to Trakt.
const (
	testJan1Start   = "2024-01-01T00:00:00.000Z"
	testDateEnd     = "2024-01-31T23:59:59.999Z"
	testDateEvening = "2024-01-31T20:00:00.000Z"
)

func TestHistoryDateRange(t *testing.T) {
	t.Parallel()

	route := "GET " + routeSyncHistory

	runArgCases(t, []argCase{
		{
			name: "iso range", tool: toolFetchHistory, args: map[string]any{argStartAt: testJan1Start, argEndAt: "2024-01-31T23:00:00+01:00"},
			route: route, body: emptyList, wantQuery: map[string]string{argStartAt: testJan1Start, argEndAt: "2024-01-31T22:00:00.000Z"},
		},
		{
			// A date-only start is the start of that day and a date-only end
			// its last millisecond, so the end day's plays are included.
			name: "dates", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-01-01", argEndAt: testDate},
			route: route, body: emptyList, wantQuery: map[string]string{argStartAt: testJan1Start, argEndAt: testDateEnd},
		},
		{
			name: "same date", tool: toolFetchHistory, args: map[string]any{argStartAt: testDate, argEndAt: testDate},
			route: route, body: emptyList, wantQuery: map[string]string{argStartAt: "2024-01-31T00:00:00.000Z", argEndAt: testDateEnd},
		},
		{
			name: "timestamp start, end date of the same day", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-01-31T20:00:00Z", argEndAt: testDate},
			route: route, body: emptyList, wantQuery: map[string]string{argStartAt: testDateEvening, argEndAt: testDateEnd},
		},
		{
			name: "timestamps within one day", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-01-31T08:00:00Z", argEndAt: "2024-01-31T09:30:00Z"},
			route: route, body: emptyList, wantQuery: map[string]string{argStartAt: "2024-01-31T08:00:00.000Z", argEndAt: "2024-01-31T09:30:00.000Z"},
		},
		{
			name: "start only", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-01-01"}, route: route, body: emptyList,
			wantQuery: map[string]string{argStartAt: testJan1Start}, noQuery: []string{argEndAt},
		},
		{
			name: "end date only", tool: toolFetchHistory, args: map[string]any{argEndAt: testDate}, route: route, body: emptyList,
			wantQuery: map[string]string{argEndAt: testDateEnd}, noQuery: []string{argStartAt},
		},
		{name: "timestamps reversed within one day", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-01-31T09:30:00Z", argEndAt: "2024-01-31T08:00:00Z"}, wantErr: invalid(fieldEndAt)},
		{name: "end date before timestamp start", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-02-01T00:00:00Z", argEndAt: testDate}, wantErr: invalid(fieldEndAt)},
		{name: "no range", tool: toolFetchHistory, args: map[string]any{}, route: route, body: emptyList, noQuery: []string{argStartAt, argEndAt}},
		{name: "end before start", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-02-01", argEndAt: testDate}, wantErr: invalid(fieldEndAt)},
		{name: "garbage start", tool: toolFetchHistory, args: map[string]any{argStartAt: "last week"}, wantErr: invalid(fieldStartAt)},
		{name: "garbage end", tool: toolFetchHistory, args: map[string]any{argEndAt: "2024-13-01"}, wantErr: invalid(fieldEndAt)},
		{name: "query injection", tool: toolFetchHistory, args: map[string]any{argStartAt: "2024-01-01&limit=1000"}, wantErr: invalid(fieldStartAt)},
	})
}

func TestShowProgressFlags(t *testing.T) {
	t.Parallel()

	runArgCases(t, []argCase{
		{
			name: "flags", tool: toolFetchShowProgress,
			args:  merge(showShow(testShowSlug), map[string]any{argHidden: true, argSpecials: true, argCountSpecials: false, argLastActivity: lastActivityWatched}),
			route: routeShowProgress, body: emptyObject,
			wantQuery: map[string]string{argHidden: queryTrue, argSpecials: queryTrue, argCountSpecials: queryFalse, argLastActivity: lastActivityWatched},
		},
		{
			name: "defaults", tool: toolFetchShowProgress, args: showShow(testShowSlug), route: routeShowProgress, body: emptyObject,
			noQuery: []string{argHidden, argSpecials, argCountSpecials, argLastActivity},
		},
		{
			name: "aired", tool: toolFetchShowProgress, args: merge(showShow(testShowSlug), map[string]any{argLastActivity: lastActivityAired}),
			route: routeShowProgress, body: emptyObject, wantQuery: map[string]string{argLastActivity: lastActivityAired},
		},
		{name: "bad activity", tool: toolFetchShowProgress, args: merge(showShow(testShowSlug), map[string]any{argLastActivity: "collected"}), wantErr: invalid(fieldLastActivity)},
		{name: "bad show", tool: toolFetchShowProgress, args: showShow(traversalID), wantErr: invalid(fieldShowID)},
		{name: "person id", tool: toolFetchShowProgress, args: showShow(personIMDb), wantErr: invalid(fieldShowID)},
	})

	t.Run("verbose", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(routeShowProgress, http.StatusOK, fixture(t, "show_progress"), nil)

		var out any

		h.ok(toolFetchShowProgress, merge(showShow(testShowSlug), map[string]any{argVerbose: true}), &out)

		want := map[string]any{
			"seasons.0.episodes.0.number": 1, "seasons.0.episodes.0.completed": true,
			"seasons.0.episodes.0.last_watched_at": "2024-02-01T20:00:00.000Z", "seasons.0.episodes.6.completed": false,
			"seasons.1.episodes": []any{},
		}
		for path, w := range want {
			if got, ok := lookup(out, path); !ok || fmt.Sprint(got) != fmt.Sprint(w) {
				t.Errorf("%s = %v (present %v), want %v", path, got, ok, w)
			}
		}

		if _, ok := lookup(out, "seasons.0.episodes.6.last_watched_at"); ok {
			t.Error("unwatched episode has a last_watched_at")
		}
	})

	t.Run("next episode not an object", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(routeShowProgress, http.StatusOK, `{"aired": 0, "completed": 0, "next_episode": "soon", "last_episode": null, "seasons": null}`, nil)

		var out any

		h.ok(toolFetchShowProgress, showShow(testShowSlug), &out)

		for _, path := range []string{"next_episode", "last_episode"} {
			if _, ok := lookup(out, path); ok {
				t.Errorf("%s present, want absent", path)
			}
		}

		if got, _ := lookup(out, "percentage"); fmt.Sprint(got) != "0" {
			t.Errorf("percentage with nothing aired = %v, want 0", got)
		}
	})
}

func TestPlaybackArguments(t *testing.T) {
	t.Parallel()

	runArgCases(t, []argCase{
		{name: "all", tool: toolFetchPlaybackProgress, args: map[string]any{}, route: "GET /sync/playback", body: emptyList, wantPath: "/sync/playback"},
		{
			name: "movies", tool: toolFetchPlaybackProgress, args: map[string]any{argPlaybackType: mediaMovies},
			route: "GET /sync/playback/movies", body: emptyList, wantPath: "/sync/playback/movies",
		},
		{
			name: "episodes", tool: toolFetchPlaybackProgress, args: map[string]any{argPlaybackType: mediaEpisodes},
			route: "GET /sync/playback/episodes", body: emptyList, wantPath: "/sync/playback/episodes",
		},
		{name: "shows", tool: toolFetchPlaybackProgress, args: map[string]any{argPlaybackType: mediaShows}, wantErr: invalid(fieldPlaybackType)},
		{name: "zero id", tool: toolRemovePlaybackItem, args: map[string]any{argPlaybackID: 0}, wantErr: invalid(fieldPlaybackID)},
		{name: "negative id", tool: toolRemovePlaybackItem, args: map[string]any{argPlaybackID: -1}, wantErr: invalid(fieldPlaybackID)},
		{name: "watched bad type", tool: toolFetchUserWatched, args: map[string]any{argType: mediaEpisodes}, wantErr: invalid(fieldType)},
		{name: "watched empty type", tool: toolFetchUserWatched, args: map[string]any{argType: ""}, wantErr: invalid(fieldType)},
		{
			name: "watched shows drop seasons", tool: toolFetchUserWatched, args: map[string]any{argType: mediaShows},
			route: "GET /sync/watched/shows", body: emptyList, wantQuery: map[string]string{queryExtended: extendedNoSeasons},
		},
	})
}

// TestUserWatchedPagesLocally checks that the unpaginated watched list is
// sorted by last watch, cut to limit and paged by the tool.
func TestUserWatchedPagesLocally(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET /sync/watched/movies", http.StatusOK, fixture(t, "sync_watched_movies"), nil)

	var out Listing[WatchedItem]

	h.ok(toolFetchUserWatched, map[string]any{argType: mediaMovies, fieldLimit: 1, fieldPage: 2}, &out)

	if len(out.Items) != 1 || out.Items[0].Movie == nil || out.Items[0].Movie.Title != "Older" {
		t.Fatalf("page 2 = %+v, want the older movie", out.Items)
	}

	if out.Page == nil || out.Page.PageCount != 2 || out.Page.ItemCount != 2 {
		t.Errorf("page = %+v, want 2 pages of 2 items", out.Page)
	}
}
