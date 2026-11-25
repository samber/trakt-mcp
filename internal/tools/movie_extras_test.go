package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolMovieAliases  = "fetch_movie_aliases"
	toolMovieStudios  = "fetch_movie_studios"
	toolMovieReleases = "fetch_movie_releases"

	// Output paths of the first item, named to keep the literals unique.
	pathFirstCountry  = "items.0.country"
	pathFirstTagline  = "items.0.tagline"
	pathFirstIDTMDB   = "items.0.ids.tmdb"
	pathFirstOverview = "items.0.overview"

	// pathMovieListsAll is the default list type and sort of list_item_lists for a movie.
	pathMovieListsAll = "/lists/all/popular"
	// routeMovieReleasesFR is the Trakt route of the French releases.
	routeMovieReleasesFR = "GET " + routeMovieSlug + "/releases/fr"
)

func init() { catalogCaseSets = append(catalogCaseSets, movieExtraCases) }

func movieRoute(suffix string) string { return "GET " + routeMovieSlug + suffix }

func movieArgs(extra map[string]any) map[string]any { return merge(movieRef(testMovieSlug), extra) }

var movieExtraCases = []catalogCase{
	{tool: toolGetStats, args: movieItem(testMovieSlug), route: movieRoute("/stats"), fixture: "movie_stats", object: true, want: sharedStatsWant, absent: []string{"extra"}},
	{tool: toolListWatching, args: movieItem(testMovieSlug), route: movieRoute("/watching"), fixture: "movie_watching", want: sharedWatchingWant, absent: []string{"users.1"}},
	{
		tool: toolListTranslations, args: merge(movieItem(testMovieSlug), map[string]any{argLanguage: "fr"}), route: movieRoute("/translations"), fixture: "movie_translations",
		wantQuery: map[string]string{argLanguage: "fr"},
		want: map[string]any{
			"items.0.language": "fr", pathFirstCountry: "fr", pathFirstTitle: "Matrix", pathFirstOverview: "Un hacker decouvre la verite.", "items.1.language": "de",
		},
		absent: []string{pathFirstTagline, pathSecondTitle},
	},
	{
		tool: toolMovieAliases, args: movieArgs(nil), route: movieRoute("/aliases"), fixture: "movie_aliases",
		want:   map[string]any{pathFirstTitle: "Matrix", pathFirstCountry: "fr", "items.1.title": "The Matrix Reloaded"},
		absent: []string{"items.0.extra", "items.1.country"},
	},
	{
		tool: toolMovieStudios, args: movieArgs(nil), route: movieRoute("/studios"), fixture: "movie_studios",
		want: map[string]any{
			pathFirstItemName: "Warner Bros.", pathFirstCountry: "us", pathFirstIDTrakt: 12, pathFirstIDSlug: "warner-bros", pathFirstIDTMDB: 174,
			"items.1.name": "Village Roadshow",
		},
		absent: []string{"items.0.updated_at", "items.1.country", "items.1.ids.tmdb"},
	},
	{
		tool: toolListItemLists, args: merge(movieItem(testMovieSlug), pagedArgs), route: movieRoute(pathMovieListsAll), fixture: "movie_lists", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want:      merge(sharedListsWant, map[string]any{pathPageCount: 3}),
	},
	{
		tool: toolMovieReleases, args: movieArgs(map[string]any{fieldCountry: testCountry}), route: routeMovieReleasesFR, fixture: "movie_releases",
		want: map[string]any{
			pathFirstCountry: "fr", "items.0.certification": "U", "items.0.release_date": "1999-06-23", "items.0.release_type": "theatrical",
			"items.1.release_date": "2000-02-01", "items.1.release_type": "digital", "items.1.note": "Director's cut",
		},
		absent: []string{"items.0.extra", "items.0.note", "items.1.certification"},
	},
}

func TestMovieExtraToolArguments(t *testing.T) {
	t.Parallel()

	releases := func(country string) map[string]any { return movieArgs(map[string]any{fieldCountry: country}) }

	cases := []argCase{
		{name: "movie id traversal", tool: toolGetStats, args: movieItem(traversalID), wantErr: invalid(fieldID)},
		{name: "movie id empty", tool: toolListWatching, args: movieItem(""), wantErr: invalid(fieldID)},
		{name: "movie id person imdb", tool: toolMovieStudios, args: movieRef(personIMDb), wantErr: invalid(fieldMovieID)},
		{name: "movie id too long", tool: toolMovieAliases, args: movieRef(strings.Repeat("a", 129)), wantErr: invalid(fieldMovieID)},
		{name: "movie id upper case", tool: toolListItemLists, args: movieItem("The-Matrix"), wantErr: invalid(fieldID)},
		{name: "movie id numeric", tool: toolGetStats, args: movieItem("481"), route: "GET /movies/481/stats", body: emptyObject},
		{name: "language fr", tool: toolListTranslations, args: merge(movieItem(testMovieSlug), map[string]any{argLanguage: "fr"}), route: movieRoute("/translations"), body: emptyList, wantQuery: map[string]string{argLanguage: "fr"}},
		{name: "language omitted means all", tool: toolListTranslations, args: movieItem(testMovieSlug), route: movieRoute("/translations"), body: emptyList, noQuery: []string{argLanguage}},
		{name: "language three letters", tool: toolListTranslations, args: merge(movieItem(testMovieSlug), map[string]any{argLanguage: "fra"}), wantErr: invalid(fieldLanguage)},
		{name: "language injection", tool: toolListTranslations, args: merge(movieItem(testMovieSlug), map[string]any{argLanguage: "fr&x=1"}), wantErr: invalid(fieldLanguage)},
		{name: "country fr", tool: toolMovieReleases, args: releases("fr"), route: routeMovieReleasesFR, body: emptyList},
		{name: "country three letters", tool: toolMovieReleases, args: releases("FRA"), wantErr: invalid(fieldCountry)},
		{name: "country upper case", tool: toolMovieReleases, args: releases("FR"), wantErr: invalid(fieldCountry)},
		{name: "country empty", tool: toolMovieReleases, args: releases(""), wantErr: invalid(fieldCountry)},
		{name: "country traversal", tool: toolMovieReleases, args: releases("../"), wantErr: invalid(fieldCountry)},
		{name: "lists defaults", tool: toolListItemLists, args: movieItem(testMovieSlug), route: movieRoute(pathMovieListsAll), body: emptyList},
		{name: "lists official by likes", tool: toolListItemLists, args: merge(movieItem(testMovieSlug), map[string]any{argListType: listTypeOfficial, argSort: listSortLikes}), route: movieRoute("/lists/official/likes"), body: emptyList},
		{name: "lists type unknown", tool: toolListItemLists, args: merge(movieItem(testMovieSlug), map[string]any{argListType: "x"}), wantErr: invalid(fieldListType)},
		{name: "lists sort unknown", tool: toolListItemLists, args: merge(movieItem(testMovieSlug), map[string]any{argSort: "x"}), wantErr: invalid(fieldSort)},
		{name: "lists page 0", tool: toolListItemLists, args: merge(movieItem(testMovieSlug), map[string]any{fieldPage: 0}), wantErr: invalid(fieldPage)},
		{name: "lists limit 0", tool: toolListItemLists, args: merge(movieItem(testMovieSlug), map[string]any{fieldLimit: 0}), route: movieRoute(pathMovieListsAll), body: emptyList},
		{name: "lists limit 101", tool: toolListItemLists, args: merge(movieItem(testMovieSlug), map[string]any{fieldLimit: 101}), wantErr: invalid(fieldLimit)},
	}

	runArgCases(t, cases)
}

// TestMovieExtraTraktFailures covers every endpoint of the group: each Trakt
// failure maps to its own message and never echoes Trakt's text.
func TestMovieExtraTraktFailures(t *testing.T) {
	t.Parallel()

	const (
		notAList   = `{"not":"a list"}`
		notAnArray = `[1]`
	)

	runUpstreamFailures(t, toolGetStats, movieItem(testMovieSlug), movieRoute("/stats"), notAnArray)
	runUpstreamFailures(t, toolListWatching, movieItem(testMovieSlug), movieRoute("/watching"), notAList)
	runUpstreamFailures(t, toolListTranslations, movieItem(testMovieSlug), movieRoute("/translations"), notAList)
	runUpstreamFailures(t, toolMovieAliases, movieArgs(nil), movieRoute("/aliases"), notAList)
	runUpstreamFailures(t, toolMovieStudios, movieArgs(nil), movieRoute("/studios"), notAList)
	runUpstreamFailures(t, toolListItemLists, movieItem(testMovieSlug), movieRoute(pathMovieListsAll), notAList)
	runUpstreamFailures(t, toolMovieReleases, movieArgs(map[string]any{fieldCountry: testCountry}), routeMovieReleasesFR, notAList)
}

// TestMovieExtraTextIsBounded requires third-party text (list names, notes,
// studio and alias names) to be cut.
func TestMovieExtraTextIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 5*maxReleaseNoteRunes)

	h := newHarness(t)
	h.trakt.respond(routeMovieReleasesFR, http.StatusOK,
		`[{"country":"fr","certification":"`+long+`","release_date":"2000-01-01","release_type":"`+long+`","note":"`+long+`"}]`, nil)

	var rel Listing[Release]

	h.ok(toolMovieReleases, movieArgs(map[string]any{fieldCountry: testCountry}), &rel)

	r := rel.Items[0]
	if len([]rune(r.Note)) > maxReleaseNoteRunes || len([]rune(r.Certification)) > maxCertificationRunes || len([]rune(r.ReleaseType)) > maxReleaseTypeRunes {
		t.Errorf("release = %+v, want bounded text", r)
	}

	h = newHarness(t)
	h.trakt.respond(movieRoute("/studios"), http.StatusOK, `[{"name":"`+long+`","country":"`+long+`","ids":{"trakt":1,"slug":"`+long+`"}}]`, nil)

	var studios Listing[Studio]

	h.ok(toolMovieStudios, movieArgs(nil), &studios)

	if s := studios.Items[0]; len([]rune(s.Name)) > maxTitleRunes || len([]rune(s.Country)) > maxCountryFieldRunes || len([]rune(s.IDs.Slug)) > maxSlugRunes {
		t.Errorf("studio = %+v, want bounded text", s)
	}

	h = newHarness(t)
	h.trakt.respond(movieRoute("/aliases"), http.StatusOK, `[{"title":"`+long+`","country":"`+long+`"}]`, nil)

	var aliases Listing[Alias]

	h.ok(toolMovieAliases, movieArgs(nil), &aliases)

	if a := aliases.Items[0]; len([]rune(a.Title)) > maxTitleRunes || len([]rune(a.Country)) > maxCountryFieldRunes {
		t.Errorf("alias = %+v, want bounded text", a)
	}

	h = newHarness(t)
	h.trakt.respond(movieRoute(pathMovieListsAll), http.StatusOK, `[{"name":"`+long+`","description":"`+long+`","ids":{"trakt":1,"slug":"s"},"user":{"username":"u","ids":{"slug":"u","trakt":1}}}]`, nil)

	var lists Listing[TraktList]

	h.ok(toolListItemLists, movieItem(testMovieSlug), &lists)

	if l := lists.Items[0]; len([]rune(l.Name)) > maxTitleRunes {
		t.Errorf("list name has %d runes, want at most %d", len([]rune(l.Name)), maxTitleRunes)
	}
}
