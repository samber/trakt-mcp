package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolSearchByID       = "search_by_id"
	toolSearchMoviesExct = "search_movies_exact"
	toolSearchShowsExct  = "search_shows_exact"

	pathFirstScore  = "items.0.score"
	routeSearchIMDb = "GET /search/imdb/" + testMovieIMDb
)

func init() { catalogCaseSets = append(catalogCaseSets, searchExtraCases) }

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
		tool: toolSearchMoviesExct, args: map[string]any{fieldQuery: "  matrix  "}, route: "GET /search/movie/exact", fixture: "movie_search",
		wantQuery: map[string]string{fieldQuery: "matrix"},
		want: map[string]any{
			pathFirstScore: 97.5, pathFirstTitle: testMovieTitle, pathFirstIDSlug: testMovieSlug,
			pathSecondTitle: "Untitled",
		},
		absent: []string{"items.1.year"},
	},
	{
		tool: toolSearchShowsExct, args: map[string]any{fieldQuery: "breaking"}, route: "GET /search/show/exact", fixture: "show_search",
		wantQuery: map[string]string{fieldQuery: "breaking"},
		want:      map[string]any{pathFirstScore: 97.5, pathFirstTitle: testShowTitle, pathFirstIDSlug: testShowSlug, pathSecondTitle: "Better Call Saul"},
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
		{name: "movies exact query empty", tool: toolSearchMoviesExct, args: map[string]any{fieldQuery: ""}, wantErr: invalid(fieldQuery)},
		{name: "movies exact query blank", tool: toolSearchMoviesExct, args: map[string]any{fieldQuery: "   "}, wantErr: invalid(fieldQuery)},
		{name: "shows exact query empty", tool: toolSearchShowsExct, args: map[string]any{fieldQuery: ""}, wantErr: invalid(fieldQuery)},
		{name: "shows exact 200 runes", tool: toolSearchShowsExct, args: map[string]any{fieldQuery: strings.Repeat("é", maxQueryRunes)}, route: "GET /search/show/exact", body: emptyList},
		{name: "shows exact 201 runes", tool: toolSearchShowsExct, args: map[string]any{fieldQuery: strings.Repeat("é", maxQueryRunes+1)}, wantErr: invalid(fieldQuery)},
	}

	runArgCases(t, cases)
}

// TestSearchExtraTraktFailures covers the lookup and exact search endpoints.
func TestSearchExtraTraktFailures(t *testing.T) {
	t.Parallel()

	const notAList = `{"not":"a list"}`

	runUpstreamFailures(t, toolSearchByID, lookupArgs("imdb", testMovieIMDb, itemMovie), routeSearchIMDb, notAList)
	runUpstreamFailures(t, toolSearchMoviesExct, map[string]any{fieldQuery: "matrix"}, "GET /search/movie/exact", notAList)
	runUpstreamFailures(t, toolSearchShowsExct, map[string]any{fieldQuery: testShowTitle}, "GET /search/show/exact", notAList)
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
