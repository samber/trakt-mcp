package tools

import (
	"net/http"
	"strconv"
	"testing"
)

const (
	// routeSuffixWatchNowFR is the where-to-watch path tail for the French
	// market, after a movie, TV show or episode base route.
	routeSuffixWatchNowFR = "/watchnow/fr"

	// routeRecommendationsMovies and routeRecommendationsShows are the
	// Trakt paths that hide a recommendation, without the id.
	routeRecommendationsMovies = "/recommendations/movies/"
	routeRecommendationsShows  = "/recommendations/shows/"

	// typeUpperCase and typeUpperCaseShow are types in the wrong case: the
	// match on type is exact.
	typeUpperCase     = "Movie"
	typeUpperCaseShow = "Show"

	// fixtureUnhide is the fixture Trakt answers a hidden-item removal with.
	fixtureUnhide = "hidden_recommendations_remove"
)

// movieShowCase is one call of a tool that takes a movie or a TV show: the
// arguments, the Trakt request they must make and the answer to give it.
type movieShowCase struct {
	tool, typ string
	args      map[string]any
	// method and path are the Trakt request the call must make.
	method, path string
	// status and body are the answer; a body equal to fixtureUnhide names
	// the fixture to read.
	status int
	body   string
}

func (c movieShowCase) route() string { return c.method + " " + c.path }

// movieShowCases covers every tool taking a movie or a TV show, for both
// types: each must reach its own Trakt endpoint.
func movieShowCases() []movieShowCase {
	var cases []movieShowCase

	for _, tc := range []struct {
		typ, base, recs string
		item            func(string) map[string]any
		id              string
	}{
		{typeMovie, routeMovieSlug, routeRecommendationsMovies, movieItem, testMovieSlug},
		{typeShow, routeShowSlug, routeRecommendationsShows, showItem, testShowSlug},
	} {
		for _, suffix := range []struct{ tool, tail string }{
			{toolListAliases, "/aliases"},
			{toolListStudios, "/studios"},
			{toolListRelated, "/related"},
		} {
			cases = append(cases, movieShowCase{
				tool: suffix.tool, typ: tc.typ, args: tc.item(tc.id), method: http.MethodGet, path: tc.base + suffix.tail,
				status: http.StatusOK, body: emptyList,
			})
		}

		cases = append(cases,
			movieShowCase{
				tool: toolGetWatchNow, typ: tc.typ, args: withCountry(tc.item(tc.id), testCountry), method: http.MethodGet,
				path: tc.base + routeSuffixWatchNowFR, status: http.StatusOK, body: emptyObject,
			},
			movieShowCase{
				tool: toolListRecommendations, typ: tc.typ, args: typeArg(tc.typ), method: http.MethodGet,
				path: "/recommendations/" + tc.typ + "s/", status: http.StatusOK, body: emptyList,
			},
			movieShowCase{
				tool: toolHideRecommendation, typ: tc.typ, args: tc.item(tc.id), method: http.MethodDelete,
				path: tc.recs + tc.id, status: http.StatusNoContent,
			},
			movieShowCase{
				tool: toolUnhideRecommendation, typ: tc.typ, args: tc.item(tc.id), method: http.MethodPost,
				path: pathHiddenRecsRemove, status: http.StatusOK, body: fixtureUnhide,
			},
		)
	}

	return append(cases, movieShowCase{
		tool: toolGetWatchNow, typ: typeEpisode, args: withCountry(episodeItem(testShowSlug, 1, 2), testCountry), method: http.MethodGet,
		path: episodeBaseFor(2) + routeSuffixWatchNowFR, status: http.StatusOK, body: emptyObject,
	})
}

// answer is the body the case's Trakt route sends.
func (c movieShowCase) answer(t *testing.T) string {
	t.Helper()

	if c.body == fixtureUnhide {
		return fixture(t, fixtureUnhide)
	}

	return c.body
}

// TestMovieShowToolsRouteByType requires each type to reach its own Trakt
// endpoint, and exactly one request to be made.
func TestMovieShowToolsRouteByType(t *testing.T) {
	t.Parallel()

	for _, tc := range movieShowCases() {
		t.Run(tc.tool+"/"+tc.typ, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tc.route(), tc.status, tc.answer(t), nil)
			h.ok(tc.tool, tc.args, nil)

			seen := h.trakt.seen()
			if len(seen) != 1 {
				t.Fatalf("Trakt requests = %d, want 1", len(seen))
			}

			if seen[0].Method != tc.method || seen[0].URL.Path != tc.path {
				t.Errorf("request = %s %s, want %s %s", seen[0].Method, seen[0].URL.Path, tc.method, tc.path)
			}
		})
	}
}

// TestMovieShowToolsNotFound requires a Trakt 404 to give the not-found tool
// error, for each type. Unhide is left out: it posts a body and reports the
// ids Trakt did not find instead of failing.
func TestMovieShowToolsNotFound(t *testing.T) {
	t.Parallel()

	for _, tc := range movieShowCases() {
		if tc.tool == toolUnhideRecommendation {
			continue
		}

		t.Run(tc.tool+"/"+tc.typ, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tc.route(), http.StatusNotFound, emptyObject, nil)
			h.fail(tc.tool, tc.args, msgNotFound)
		})
	}
}

// TestMovieShowToolsRefuseOtherTypes requires a type the tool does not
// accept, an empty or upper-case one included, to fail before any Trakt call.
func TestMovieShowToolsRefuseOtherTypes(t *testing.T) {
	t.Parallel()

	bases := map[string]map[string]any{
		toolListAliases:          movieItem(testMovieSlug),
		toolListStudios:          movieItem(testMovieSlug),
		toolListRelated:          movieItem(testMovieSlug),
		toolListRecommendations:  {},
		toolHideRecommendation:   movieItem(testMovieSlug),
		toolUnhideRecommendation: movieItem(testMovieSlug),
		toolGetWatchNow:          withCountry(movieItem(testMovieSlug), testCountry),
	}

	var cases []argCase

	for tool, base := range bases {
		types := []string{typeSeason, typeEpisode, "", typeUpperCase, typeUpperCaseShow, itemPerson}
		if tool == toolGetWatchNow {
			// An episode is a type get_watchnow accepts.
			types = []string{typeSeason, "", typeUpperCase, typeUpperCaseShow, itemPerson}
		}

		for _, typ := range types {
			cases = append(cases, argCase{
				name: tool + " type " + typ, tool: tool, args: merge(base, typeArg(typ)), wantErr: invalid(fieldType),
			})
		}
	}

	// A season or an episode reference never makes sense for a movie or a TV
	// show: refused, not ignored.
	for _, tool := range []string{toolListAliases, toolListStudios, toolListRelated, toolHideRecommendation, toolUnhideRecommendation, toolGetWatchNow} {
		base := map[string]any{}
		if tool == toolGetWatchNow {
			base = withCountry(base, testCountry)
		}

		cases = append(cases,
			argCase{name: tool + " movie with season", tool: tool, args: merge(merge(base, movieItem(testMovieSlug)), map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
			argCase{name: tool + " show with season", tool: tool, args: merge(merge(base, showItem(testShowSlug)), map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
			argCase{name: tool + " movie with episode", tool: tool, args: merge(merge(base, movieItem(testMovieSlug)), map[string]any{fieldEpisode: 1}), wantErr: invalid(fieldEpisode)},
			argCase{name: tool + " show with episode", tool: tool, args: merge(merge(base, showItem(testShowSlug)), map[string]any{fieldEpisode: 1}), wantErr: invalid(fieldEpisode)},
			argCase{name: tool + " id person imdb", tool: tool, args: merge(base, movieItem(personIMDb)), wantErr: invalid(fieldID)},
		)
	}

	runArgCases(t, cases)
}

// TestGetWatchNowEpisodeArguments requires an episode to name its season and
// its episode, in range.
func TestGetWatchNowEpisodeArguments(t *testing.T) {
	t.Parallel()

	episode := func(extra map[string]any) map[string]any {
		return withCountry(merge(map[string]any{argType: typeEpisode, argID: testShowSlug}, extra), testCountry)
	}

	cases := []argCase{
		{name: "episode without season and episode", tool: toolGetWatchNow, args: episode(nil), wantErr: invalid(fieldSeason)},
		{name: "episode without season", tool: toolGetWatchNow, args: episode(map[string]any{fieldEpisode: 2}), wantErr: invalid(fieldSeason)},
		{name: "episode without episode", tool: toolGetWatchNow, args: episode(map[string]any{fieldSeason: 1}), wantErr: invalid(fieldEpisode)},
		{name: "season below 0", tool: toolGetWatchNow, args: episode(map[string]any{fieldSeason: -1, fieldEpisode: 2}), wantErr: invalid(fieldSeason)},
		{name: "season above max", tool: toolGetWatchNow, args: episode(map[string]any{fieldSeason: maxSeason + 1, fieldEpisode: 2}), wantErr: invalid(fieldSeason)},
		{name: "episode below 1", tool: toolGetWatchNow, args: episode(map[string]any{fieldSeason: 1, fieldEpisode: 0}), wantErr: invalid(fieldEpisode)},
		{name: "episode above max", tool: toolGetWatchNow, args: episode(map[string]any{fieldSeason: 1, fieldEpisode: maxEpisode + 1}), wantErr: invalid(fieldEpisode)},
		{
			name: "episode at max", tool: toolGetWatchNow, args: episode(map[string]any{fieldSeason: maxSeason, fieldEpisode: maxEpisode}),
			route: "GET " + seasonBaseFor(maxSeason) + "/episodes/" + strconv.Itoa(maxEpisode) + routeSuffixWatchNowFR, body: emptyObject,
		},
	}

	runArgCases(t, cases)
}

// TestLegacyMovieShowToolsRemoved requires the per-type tools to be gone.
func TestLegacyMovieShowToolsRemoved(t *testing.T) {
	t.Parallel()

	requireLegacyToolsRemoved(t, "watchnow", "aliases", "studios", "recommendations")
	requireToolsRemoved(t,
		"fetch_related_movies", "fetch_related_shows",
		"hide_movie_recommendation", "hide_show_recommendation",
		"unhide_movie_recommendation", "unhide_show_recommendation",
	)
}
