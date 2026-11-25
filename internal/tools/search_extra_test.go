package tools

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const (
	toolSearchByID = "search_by_id"
	toolSearch     = "search"

	// argExact is the argument selecting the exact title search.
	argExact = "exact"

	// routeSearchMovie and routeSearchShow are the Trakt text search routes.
	routeSearchMovie = "GET /search/movie"
	routeSearchShow  = "GET /search/show"

	pathFirstScore  = "items.0.score"
	routeSearchIMDb = "GET /search/imdb/" + testMovieIMDb
)

func init() { catalogCaseSets = append(catalogCaseSets, searchExtraCases) }

// searchArgs are the arguments of search for a text query.
func searchArgs(typ, query string) map[string]any {
	return map[string]any{argType: typ, fieldQuery: query}
}

// exactSearchArgs are the arguments of search for an exact title.
func exactSearchArgs(typ, query string) map[string]any {
	return merge(searchArgs(typ, query), map[string]any{argExact: true})
}

func lookupArgs(idType, id, typ string) map[string]any {
	args := map[string]any{fieldIDType: idType, fieldID: id}
	if typ != "" {
		args[fieldType] = typ
	}

	return args
}

var searchExtraCases = []catalogCase{
	{
		tool: toolSearchByID, args: lookupArgs("imdb", testMovieIMDb, itemMovie), route: routeSearchIMDb, fixture: "search_lookup",
		wantQuery: map[string]string{fieldType: itemMovie},
		want: map[string]any{
			pathFirstItemType: itemMovie, pathFirstTitle: testMovieTitle, pathFirstIDTrakt: 481,
			pathFirstIDSlug: testMovieSlug, "items.0.ids.imdb": testMovieIMDb, "items.0.ids.tmdb": 603,
			"items.1.type": itemEpisode, pathSecondTitle: "Cat's in the Bag...", "items.1.season": 1, "items.1.episode": 2,
			"items.1.ids.trakt": 62119, "items.1.ids.tvdb": 349233,
			"items.2.type": lookupPerson, "items.2.title": "Keanu Reeves", "items.2.ids.imdb": "nm0000206",
			"items.3.type": itemEpisode, "items.3.season": 2, "items.3.episode": 1,
		},
		absent: []string{"items.1.ids.imdb", "items.3.title", "items.2.year", "items.0.season"},
	},
	{
		tool: toolSearch, args: exactSearchArgs(typeMovie, "  matrix  "), route: "GET /search/movie/exact", fixture: "movie_search",
		wantQuery: map[string]string{fieldQuery: "matrix"},
		want: merge(map[string]any{argType: typeMovie}, inItem(map[string]any{
			pathFirstScore: 97.5, pathFirstTitle: testMovieTitle, pathFirstIDSlug: testMovieSlug,
			pathSecondTitle: "Untitled",
		})),
		absent: inItemPaths([]string{"items.1.year"}),
	},
	{
		tool: toolSearch, args: exactSearchArgs(typeShow, "breaking"), route: "GET /search/show/exact", fixture: "show_search",
		wantQuery: map[string]string{fieldQuery: "breaking"},
		want:      merge(map[string]any{argType: typeShow}, inItem(map[string]any{pathFirstScore: 97.5, pathFirstTitle: testShowTitle, pathFirstIDSlug: testShowSlug, pathSecondTitle: "Better Call Saul"})),
	},
}

func TestSearchExtraArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{
		{name: "id type x", tool: toolSearchByID, args: lookupArgs("x", "1", ""), wantErr: invalid(fieldIDType)},
		{name: "id type empty", tool: toolSearchByID, args: lookupArgs("", "1", ""), wantErr: invalid(fieldIDType)},
		{name: "id type upper case", tool: toolSearchByID, args: lookupArgs("IMDB", testMovieIMDb, ""), wantErr: invalid(fieldIDType)},
		{name: "type x", tool: toolSearchByID, args: lookupArgs("trakt", "1", "x"), wantErr: invalid(fieldType)},
		{name: "type list", tool: toolSearchByID, args: lookupArgs("trakt", "1", "list"), wantErr: invalid(fieldType)},
		{name: "empty id", tool: toolSearchByID, args: lookupArgs("trakt", "", ""), wantErr: invalid(fieldID)},
		{name: "id traversal", tool: toolSearchByID, args: lookupArgs("imdb", traversalID, ""), wantErr: invalid(fieldID)},
		{name: "id too long", tool: toolSearchByID, args: lookupArgs("tmdb", strings.Repeat("1", maxIDLength+1), ""), wantErr: invalid(fieldID)},
		{name: "tmdb id is numeric only", tool: toolSearchByID, args: lookupArgs("tmdb", "the-matrix", ""), wantErr: invalid(fieldID)},
		{name: "imdb id must be tt or nm", tool: toolSearchByID, args: lookupArgs("imdb", "603", ""), wantErr: invalid(fieldID)},
		{name: "imdb person id", tool: toolSearchByID, args: lookupArgs("imdb", personIMDb, lookupPerson), route: "GET /search/imdb/" + personIMDb, body: emptyList, wantQuery: map[string]string{fieldType: lookupPerson}},
		{name: "tvdb id no type", tool: toolSearchByID, args: lookupArgs("tvdb", "81189", ""), route: "GET /search/tvdb/81189", body: emptyList, noQuery: []string{fieldType}},
		{name: "trakt id", tool: toolSearchByID, args: lookupArgs("trakt", "1388", "show"), route: "GET /search/trakt/1388", body: emptyList},
		{name: "movies exact query empty", tool: toolSearch, args: exactSearchArgs(typeMovie, ""), wantErr: invalid(fieldQuery)},
		{name: "movies exact query blank", tool: toolSearch, args: exactSearchArgs(typeMovie, "   "), wantErr: invalid(fieldQuery)},
		{name: "shows exact query empty", tool: toolSearch, args: exactSearchArgs(typeShow, ""), wantErr: invalid(fieldQuery)},
		{name: "shows exact 200 runes", tool: toolSearch, args: exactSearchArgs(typeShow, strings.Repeat("é", maxQueryRunes)), route: "GET /search/show/exact", body: emptyList},
		{name: "shows exact 201 runes", tool: toolSearch, args: exactSearchArgs(typeShow, strings.Repeat("é", maxQueryRunes+1)), wantErr: invalid(fieldQuery)},
		{name: "exact false is the text search", tool: toolSearch, args: merge(searchArgs(typeMovie, "matrix"), map[string]any{argExact: false}), route: routeSearchMovie, body: emptyList},
		{name: "type person", tool: toolSearch, args: searchArgs(itemPerson, "keanu"), wantErr: invalid(fieldType)},
		{name: "type person exact", tool: toolSearch, args: exactSearchArgs(itemPerson, "keanu"), wantErr: invalid(fieldType)},
		{name: "type season", tool: toolSearch, args: searchArgs(typeSeason, "matrix"), wantErr: invalid(fieldType)},
		{name: "type episode", tool: toolSearch, args: searchArgs(typeEpisode, "matrix"), wantErr: invalid(fieldType)},
		{name: "type empty", tool: toolSearch, args: searchArgs("", "matrix"), wantErr: invalid(fieldType)},
		{name: "type upper case", tool: toolSearch, args: searchArgs(typeUpperCase, "matrix"), wantErr: invalid(fieldType)},
		{name: "type upper case show", tool: toolSearch, args: searchArgs(typeUpperCaseShow, "matrix"), wantErr: invalid(fieldType)},
	}

	runArgCases(t, cases)
}

// TestSearchExtraTraktFailures covers the lookup and exact search endpoints.
func TestSearchExtraTraktFailures(t *testing.T) {
	t.Parallel()

	const notAList = `{"not":"a list"}`

	runUpstreamFailures(t, toolSearchByID, lookupArgs("imdb", testMovieIMDb, itemMovie), routeSearchIMDb, notAList)
	runUpstreamFailures(t, toolSearch, exactSearchArgs(typeMovie, "matrix"), "GET /search/movie/exact", notAList)
	runUpstreamFailures(t, toolSearch, exactSearchArgs(typeShow, testShowTitle), "GET /search/show/exact", notAList)
}

// TestSearchByIDTextIsBounded requires slug and IMDb id from Trakt to be cut.
func TestSearchByIDTextIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", 2*maxIDLength)

	h := newHarness(t)
	h.trakt.respond(routeSearchIMDb, http.StatusOK, `[{"type":"movie","movie":{"title":"t","ids":{"trakt":1,"slug":"`+long+`","imdb":"`+long+`"}}}]`, nil)

	var out Listing[IDMatch]

	h.ok(toolSearchByID, lookupArgs("imdb", testMovieIMDb, ""), &out)

	if ids := out.Items[0].IDs; len([]rune(ids.Slug)) > maxIDLength || len([]rune(ids.IMDb)) > maxIDLength {
		t.Errorf("ids = %+v, want bounded text", ids)
	}
}

// TestSearchRoutesByTypeAndExact requires every type and exact setting to
// reach its own Trakt endpoint, and to answer under its own type.
func TestSearchRoutesByTypeAndExact(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		typ, route string
		exact      bool
	}{
		{typeMovie, routeSearchMovie, false},
		{typeMovie, "GET /search/movie/exact", true},
		{typeShow, routeSearchShow, false},
		{typeShow, "GET /search/show/exact", true},
	} {
		t.Run(tc.typ+"/exact="+strconv.FormatBool(tc.exact), func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tc.route, http.StatusOK, emptyList, nil)

			var out typedOut[Listing[SearchedMovie]]

			h.ok(toolSearch, merge(searchArgs(tc.typ, "matrix"), map[string]any{argExact: tc.exact}), &out)

			if out.Type != tc.typ {
				t.Errorf("type = %q, want %q", out.Type, tc.typ)
			}

			if got := len(h.trakt.seen()); got != 1 {
				t.Errorf("Trakt requests = %d, want 1", got)
			}
		})
	}
}

// TestSearchNotFound requires a Trakt 404 to give the not-found tool error,
// for each type and each endpoint.
func TestSearchNotFound(t *testing.T) {
	t.Parallel()

	for _, typ := range []string{typeMovie, typeShow} {
		for _, exact := range []bool{false, true} {
			t.Run(typ+"/exact="+strconv.FormatBool(exact), func(t *testing.T) {
				t.Parallel()

				newHarness(t).fail(toolSearch, merge(searchArgs(typ, "matrix"), map[string]any{argExact: exact}), msgNotFound)
			})
		}
	}
}

// TestLegacySearchToolsRemoved requires the per-type search tools to be gone.
func TestLegacySearchToolsRemoved(t *testing.T) {
	t.Parallel()

	requireToolsRemoved(t, "search_movies", "search_shows", "search_movies_exact", "search_shows_exact")
}
