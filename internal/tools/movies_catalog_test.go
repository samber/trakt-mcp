package tools

import (
	"net/http"
	"slices"
	"strings"
	"testing"
)

const (
	testMovieSlug  = "the-matrix-1999"
	testMovieTitle = "The Matrix"
	testMovieIMDb  = "tt0133093"
	// routeMovieSlug is the Trakt path prefix of the test movie.
	routeMovieSlug = "/movies/" + testMovieSlug

	toolFetchPopularMovies     = "fetch_popular_movies"
	toolFetchAnticipatedMovies = "fetch_anticipated_movies"
	toolFetchFavoritedMovies   = "fetch_favorited_movies"
	toolFetchPlayedMovies      = "fetch_played_movies"
	toolFetchWatchedMovies     = "fetch_watched_movies"
	toolFetchBoxofficeMovies   = "fetch_boxoffice_movies"
	toolSearchMovies           = "search_movies"
	toolFetchMovieRatings      = "fetch_movie_ratings"
	toolFetchMovieVideos       = "fetch_movie_videos"
	toolFetchRelatedMovies     = "fetch_related_movies"
	toolFetchMoviePeople       = "fetch_movie_people"

	pathFirstMovieTitle   = "items.0.movie.title"
	pathFirstIDSlug       = "items.0.ids.slug"
	pathSecondMovieGenres = "items.1.movie.genres"
	pathFirstPlayCount    = "items.0.play_count"
	keyGuestStars         = "guest_stars"
	pathFirstIDTrakt      = "items.0.ids.trakt"
	pathFirstWatcherCount = "items.0.watcher_count"
)

func init() { catalogCaseSets = append(catalogCaseSets, movieCases) }

func movieRef(id string) map[string]any { return map[string]any{fieldMovieID: id} }

var movieCases = []catalogCase{
	{
		tool: toolFetchPopularMovies, args: merge(pagedArgs, nil), route: "GET /movies/popular", fixture: "movie_popular", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2", queryExtended: extendedFull},
		want: map[string]any{
			pathFirstTitle: testMovieTitle, "items.0.year": 1999, pathFirstIDTrakt: 481, pathFirstIDSlug: testMovieSlug,
			"items.0.ids.imdb": testMovieIMDb, "items.0.ids.tmdb": 603, "items.0.rating": 8.7, "items.0.votes": 60000,
			"items.0.overview": "A hacker learns the truth.", "items.0.runtime": 136, "items.0.certification": "R",
			"items.0.released": "1999-03-31", "items.0.genres": []any{"action", "science-fiction"},
			pathSecondGenres: []any{}, pathPageCount: 3, "page.item_count": 6,
		},
		absent: []string{"items.0.tagline", keyImages, "items.0.ids.plex"},
	},
	{
		tool: toolFetchAnticipatedMovies, args: map[string]any{}, route: "GET /movies/anticipated", fixture: "movie_anticipated",
		want: map[string]any{"items.0.list_count": 5000, pathFirstMovieTitle: testMovieTitle, pathSecondMovieGenres: []any{}},
	},
	{
		tool: toolFetchFavoritedMovies, args: map[string]any{fieldPeriod: periodMonthly}, route: "GET /movies/favorited/monthly", fixture: "movie_favorited",
		want: map[string]any{"items.0.user_count": 800, "items.0.movie.ids.slug": testMovieSlug, pathSecondMovieGenres: []any{}},
	},
	{
		tool: toolFetchPlayedMovies, args: map[string]any{fieldPeriod: periodDaily}, route: "GET /movies/played/daily", fixture: "movie_played",
		want: map[string]any{
			pathFirstWatcherCount: 70, pathFirstPlayCount: 140, "items.0.collected_count": 40, "items.0.collector_count": 30,
			pathFirstMovieTitle: testMovieTitle, pathSecondMovieGenres: []any{},
		},
	},
	{
		tool: toolFetchWatchedMovies, args: map[string]any{}, route: "GET /movies/watched/weekly", fixture: "movie_watched",
		want: map[string]any{pathFirstWatcherCount: 90, pathFirstPlayCount: 180, "items.0.movie.ids.imdb": testMovieIMDb, pathSecondMovieGenres: []any{}},
	},
	{
		tool: toolFetchBoxofficeMovies, args: map[string]any{}, route: "GET /movies/boxoffice", fixture: "movie_boxoffice",
		wantQuery: map[string]string{queryExtended: extendedFull},
		want:      map[string]any{"items.0.revenue": 123456789.0, pathFirstMovieTitle: testMovieTitle, "items.1.revenue": 5, pathSecondMovieGenres: []any{}},
		absent:    []string{fieldPage},
	},
	{
		tool: toolSearchMovies, args: map[string]any{fieldQuery: "  matrix  "}, route: "GET /search/movie", fixture: "movie_search",
		wantQuery: map[string]string{"query": "matrix"},
		want: map[string]any{
			"items.0.score": 97.5, pathFirstTitle: testMovieTitle, "items.0.year": 1999, pathFirstIDSlug: testMovieSlug,
			"items.1.score": 12.25, "items.1.ids.trakt": 2,
		},
	},
	{
		tool: toolFetchMovieRatings, args: movieRef(testMovieSlug), route: "GET " + routeMovieSlug + "/ratings", fixture: "movie_ratings", object: true,
		want: map[string]any{keyRating: 8.7, keyVotes: 1000, "distribution": []any{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}},
	},
	{
		tool: toolFetchMovieVideos, args: movieRef(testMovieSlug), route: "GET " + routeMovieSlug + "/videos", fixture: "movie_videos",
		want: map[string]any{
			pathFirstTitle: "Official [Trailer]", "items.0.type": "trailer", "items.0.site": "youtube",
			"items.0.url": "https://www.youtube.com/watch?v=abc", "items.0.published_at": "1999-01-01T00:00:00.000Z",
			"items.0.markdown": `[Official \[Trailer\]](https://www.youtube.com/watch?v=abc)`,
			pathSecondTitle:    "Clip (1)", "items.1.markdown": `[Clip (1)](https://vimeo.com/a%28b%29)`,
		},
		absent: []string{"items.2"},
	},
	{
		tool: toolFetchRelatedMovies, args: merge(movieRef(testMovieSlug), pagedArgs), route: "GET " + routeMovieSlug + "/related", fixture: "movie_related", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want:      map[string]any{pathFirstTitle: testMovieTitle, pathSecondGenres: []any{}, pathPageCount: 3},
	},
	{
		tool: toolFetchMoviePeople, args: movieRef(testMovieSlug), route: "GET " + routeMovieSlug + "/people", fixture: "movie_people", object: true,
		want: map[string]any{
			"cast.0.name": "Keanu Reeves", pathFirstCastChar: "Neo", "cast.0.ids.slug": "keanu-reeves",
			"crew.0.department": deptDirecting, "crew.0.members.0.name": "Lana Wachowski", "crew.0.members.0.job": jobDirector,
			"crew.1.department": "writing", "crew.1.members.0.job": "Writer",
		},
		absent: []string{keyGuestStars},
	},
}

func TestMovieToolArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{
		{name: "period default is weekly", tool: toolFetchFavoritedMovies, args: map[string]any{}, route: "GET /movies/favorited/weekly", body: emptyList},
		{name: "period yearly", tool: toolFetchPlayedMovies, args: map[string]any{fieldPeriod: periodYearly}, route: "GET /movies/played/yearly", body: emptyList},
		{name: "period all", tool: toolFetchWatchedMovies, args: map[string]any{fieldPeriod: periodAll}, route: "GET /movies/watched/all", body: emptyList},
		{name: "period unknown", tool: toolFetchPlayedMovies, args: map[string]any{fieldPeriod: "hourly"}, wantErr: invalid(fieldPeriod)},
		{name: "period wrong case", tool: toolFetchFavoritedMovies, args: map[string]any{fieldPeriod: "Weekly"}, wantErr: invalid(fieldPeriod)},
		{name: "period path injection", tool: toolFetchWatchedMovies, args: map[string]any{fieldPeriod: traversalID}, wantErr: invalid(fieldPeriod)},
		{name: "limit 101", tool: toolFetchPopularMovies, args: map[string]any{fieldLimit: 101}, wantErr: invalid(fieldLimit)},
		{name: "page 0", tool: toolFetchAnticipatedMovies, args: map[string]any{fieldPage: 0}, wantErr: invalid(fieldPage)},
		{name: "id slug", tool: toolFetchMovieRatings, args: movieRef(testMovieSlug), route: "GET " + routeMovieSlug + "/ratings", body: emptyObject, wantPath: routeMovieSlug + "/ratings"},
		{name: "id numeric", tool: toolFetchMovieVideos, args: movieRef("481"), route: "GET /movies/481/videos", body: emptyList, wantPath: "/movies/481/videos"},
		{name: "id imdb title", tool: toolFetchMoviePeople, args: movieRef(testMovieIMDb), route: "GET /movies/tt0133093/people", body: emptyObject, wantPath: "/movies/tt0133093/people"},
		{name: "id imdb person", tool: toolFetchMoviePeople, args: movieRef(personIMDb), wantErr: invalid(fieldMovieID)},
		{name: "id path traversal", tool: toolFetchMovieRatings, args: movieRef(traversalID), wantErr: invalid(fieldMovieID)},
		{name: "id slash", tool: toolFetchRelatedMovies, args: movieRef("a/b"), wantErr: invalid(fieldMovieID)},
		{name: "id query injection", tool: toolFetchMovieVideos, args: movieRef("x?extended=full"), wantErr: invalid(fieldMovieID)},
		{name: "id empty", tool: toolFetchMoviePeople, args: movieRef(""), wantErr: invalid(fieldMovieID)},
		{name: "id upper case", tool: toolFetchRelatedMovies, args: movieRef("The-Matrix"), wantErr: invalid(fieldMovieID)},
		{name: "query empty", tool: toolSearchMovies, args: map[string]any{fieldQuery: ""}, wantErr: invalid(fieldQuery)},
		{name: "query blank", tool: toolSearchMovies, args: map[string]any{fieldQuery: "   "}, wantErr: invalid(fieldQuery)},
		{name: "query 200 runes", tool: toolSearchMovies, args: map[string]any{fieldQuery: strings.Repeat("é", maxQueryRunes)}, route: "GET /search/movie", body: emptyList},
		{name: "query 201 runes", tool: toolSearchMovies, args: map[string]any{fieldQuery: strings.Repeat("é", maxQueryRunes+1)}, wantErr: invalid(fieldQuery)},
	}

	runArgCases(t, cases)
}

// TestSearchMoviesEncodesQuery requires a query holding path and query
// delimiters to arrive as one encoded value on the unchanged path.
func TestSearchMoviesEncodesQuery(t *testing.T) {
	t.Parallel()

	const query = "a/b?c=d&e"

	h := newHarness(t)
	h.trakt.respond("GET /search/movie", http.StatusOK, fixture(t, "movie_search"), nil)
	h.ok(toolSearchMovies, map[string]any{fieldQuery: query}, nil)

	seen := h.trakt.seen()
	if len(seen) != 1 {
		t.Fatalf("Trakt requests = %d, want 1", len(seen))
	}

	if seen[0].URL.Path != "/search/movie" {
		t.Errorf("path = %q, want /search/movie", seen[0].URL.Path)
	}

	if got := seen[0].URL.Query()["query"]; !slices.Equal(got, []string{query}) {
		t.Errorf("query values = %q, want exactly %q", got, query)
	}
}

func TestMovieVideosMarkdownOptOut(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET "+routeMovieSlug+"/videos", http.StatusOK, fixture(t, "movie_videos"), nil)

	var plain Listing[Video]

	h.ok(toolFetchMovieVideos, merge(movieRef(testMovieSlug), map[string]any{argEmbedMarkdown: false}), &plain)

	if len(plain.Items) != 2 {
		t.Fatalf("items = %+v, want 2 (the javascript: video is dropped)", plain.Items)
	}

	for _, v := range plain.Items {
		if v.Markdown != "" || !strings.HasPrefix(v.URL, "https://") {
			t.Errorf("video = %+v, want a plain https url and no markdown", v)
		}
	}
}

// TestMovieNotFoundIsToolError requires every movie tool to turn a Trakt 404
// into the "not found" tool error.
func TestMovieNotFoundIsToolError(t *testing.T) {
	t.Parallel()

	for _, tc := range movieCases {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			newHarness(t).fail(tc.tool, tc.args, msgNotFound)
		})
	}
}
