package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolShowAliases        = "fetch_show_aliases"
	toolShowStudios        = "fetch_show_studios"
	toolShowNextEpisode    = "fetch_show_next_episode"
	toolShowLastEpisode    = "fetch_show_last_episode"
	toolShowCertifications = "fetch_show_certifications"

	// Output paths and values reused across the cases, named to keep each
	// literal unique in the package.
	keyRuntime         = "runtime"
	pathEpisodeIMDb    = "ids.imdb"
	keyExtra           = "extra"
	pathSecondUser     = "users.1"
	titleMatrix        = "Matrix"
	pathFirstExtra     = "items.0.extra"
	pathSecondName     = "items.1.name"
	pathFirstUpdatedAt = "items.0.updated_at"
	pathFirstCertif    = "items.0.certification"
	languageFrench     = "fr"
	languageThreeChars = "fra"
	pathSecondCountry  = "items.1.country"

	// pathShowListsAll is the default list type and sort of list_item_lists for a show.
	pathShowListsAll = "/lists/all/popular"
)

func init() { catalogCaseSets = append(catalogCaseSets, showExtraCases) }

func showExtraRoute(suffix string) string { return "GET " + routeShowSlug + suffix }

var showExtraEpisodeWant = map[string]any{
	"season": 1, "number": 2, keyTitle: "Pilot", keyOverview: "Walter learns something.", keyRating: 8.6, keyVotes: 300, keyRuntime: 58,
	"first_aired": testFirstAired, "episode_type": "series_premiere", pathIDTrakt: 62118, pathEpisodeIMDb: "tt0959621",
}

var showExtraCases = []catalogCase{
	{tool: toolGetStats, args: showItem(testShowSlug), route: showExtraRoute("/stats"), fixture: "show_stats", object: true, want: sharedStatsWant, absent: []string{keyExtra}},
	{tool: toolListWatching, args: showItem(testShowSlug), route: showExtraRoute("/watching"), fixture: "show_watching", want: sharedWatchingWant, absent: []string{pathSecondUser}},
	{
		tool: toolListTranslations, args: merge(showItem(testShowSlug), map[string]any{argLanguage: languageFrench}), route: showExtraRoute("/translations"), fixture: "show_translations",
		wantQuery: map[string]string{argLanguage: languageFrench},
		want: map[string]any{
			"items.0.language": "fr", pathFirstCountry: "fr", pathFirstTitle: titleMatrix, pathFirstOverview: "Un hacker decouvre la verite.", "items.1.language": "de",
		},
		absent: []string{pathFirstTagline, pathSecondTitle},
	},
	{
		tool: toolShowAliases, args: showShow(testShowSlug), route: showExtraRoute("/aliases"), fixture: "show_aliases",
		want:   map[string]any{pathFirstTitle: titleMatrix, pathFirstCountry: "fr", pathSecondTitle: "The Matrix Reloaded"},
		absent: []string{pathFirstExtra, pathSecondCountry},
	},
	{
		tool: toolShowStudios, args: showShow(testShowSlug), route: showExtraRoute("/studios"), fixture: "show_studios",
		want: map[string]any{
			pathFirstItemName: "Warner Bros.", pathFirstCountry: "us", pathFirstIDTrakt: 12, pathFirstIDSlug: "warner-bros", pathFirstIDTMDB: 174,
			pathSecondName: "Village Roadshow",
		},
		absent: []string{pathFirstUpdatedAt, pathSecondCountry, "items.1.ids.tmdb"},
	},
	{
		tool: toolListItemLists, args: merge(showItem(testShowSlug), pagedArgs), route: showExtraRoute(pathShowListsAll), fixture: "show_lists", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want:      merge(sharedListsWant, map[string]any{pathPageCount: 3}),
	},
	{
		tool: toolShowNextEpisode, args: showShow(testShowSlug), route: showExtraRoute("/next_episode"), fixture: "show_episode", object: true,
		want: showExtraEpisodeWant, absent: []string{keyImages, "available_translations"},
	},
	{
		tool: toolShowLastEpisode, args: showShow(testShowSlug), route: showExtraRoute("/last_episode"), fixture: "show_episode", object: true,
		want: showExtraEpisodeWant, absent: []string{keyImages, "available_translations"},
	},
	{
		tool: toolShowCertifications, args: showShow(testShowSlug), route: showExtraRoute("/certifications"), fixture: "show_certifications",
		want:   map[string]any{pathFirstCountry: "us", pathFirstCertif: "TV-MA", pathSecondCountry: "gb", "items.1.certification": "18"},
		absent: []string{pathFirstExtra},
	},
}

func TestShowExtraToolArguments(t *testing.T) {
	t.Parallel()

	withShow := showShow

	cases := []argCase{
		{name: "show extra show id traversal", tool: toolGetStats, args: showItem(traversalID), wantErr: invalid(fieldID)},
		{name: "show id empty", tool: toolListWatching, args: showItem(""), wantErr: invalid(fieldID)},
		{name: "show extra show id person imdb", tool: toolShowStudios, args: withShow(personIMDb), wantErr: invalid(fieldShowID)},
		{name: "show id too long", tool: toolShowAliases, args: withShow(strings.Repeat("a", 129)), wantErr: invalid(fieldShowID)},
		{name: "show id upper case", tool: toolListItemLists, args: showItem("Breaking-Bad"), wantErr: invalid(fieldID)},
		{name: "show id empty next episode", tool: toolShowNextEpisode, args: withShow(""), wantErr: invalid(fieldShowID)},
		{name: "show id empty last episode", tool: toolShowLastEpisode, args: withShow(""), wantErr: invalid(fieldShowID)},
		{name: "show id empty certifications", tool: toolShowCertifications, args: withShow(""), wantErr: invalid(fieldShowID)},
		{name: "show id numeric", tool: toolGetStats, args: showItem("1388"), route: "GET /shows/1388/stats", body: emptyObject},
		{name: "show extra language fr", tool: toolListTranslations, args: merge(showItem(testShowSlug), map[string]any{argLanguage: languageFrench}), route: showExtraRoute("/translations"), body: emptyList, wantQuery: map[string]string{argLanguage: languageFrench}},
		{name: "show extra language omitted means all", tool: toolListTranslations, args: showItem(testShowSlug), route: showExtraRoute("/translations"), body: emptyList, noQuery: []string{argLanguage}},
		{name: "show extra language three letters", tool: toolListTranslations, args: merge(showItem(testShowSlug), map[string]any{argLanguage: languageThreeChars}), wantErr: invalid(fieldLanguage)},
		{name: "language injection", tool: toolListTranslations, args: merge(showItem(testShowSlug), map[string]any{argLanguage: "fr&x=1"}), wantErr: invalid(fieldLanguage)},
		{name: "show extra lists defaults", tool: toolListItemLists, args: showItem(testShowSlug), route: showExtraRoute(pathShowListsAll), body: emptyList},
		{name: "show extra lists official by likes", tool: toolListItemLists, args: merge(showItem(testShowSlug), map[string]any{argListType: listTypeOfficial, argSort: listSortLikes}), route: showExtraRoute("/lists/official/likes"), body: emptyList},
		{name: "show extra lists type unknown", tool: toolListItemLists, args: merge(showItem(testShowSlug), map[string]any{argListType: "x"}), wantErr: invalid(fieldListType)},
		{name: "show extra lists sort unknown", tool: toolListItemLists, args: merge(showItem(testShowSlug), map[string]any{argSort: "x"}), wantErr: invalid(fieldSort)},
		{name: "lists page 0", tool: toolListItemLists, args: merge(showItem(testShowSlug), map[string]any{fieldPage: 0}), wantErr: invalid(fieldPage)},
		{name: "lists limit 101", tool: toolListItemLists, args: merge(showItem(testShowSlug), map[string]any{fieldLimit: 101}), wantErr: invalid(fieldLimit)},
	}

	runArgCases(t, cases)
}

// TestShowEpisodeWithoutAnswer covers a show with no next or last episode:
// Trakt answers 204 with no body, and the tools return an empty object.
func TestShowEpisodeWithoutAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ tool, route string }{
		{toolShowNextEpisode, showExtraRoute("/next_episode")},
		{toolShowLastEpisode, showExtraRoute("/last_episode")},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tc.route, http.StatusNoContent, "", nil)

			var out map[string]any

			h.ok(tc.tool, showShow(testShowSlug), &out)

			if len(out) != 0 {
				t.Errorf("result = %v, want an empty object", out)
			}
		})
	}
}

// TestShowEpisodeEmptyOrSpecialBody covers a 200 that carries no episode
// (null, {}, no Trakt id: an empty object comes back) and a special, whose
// season 0 must be kept.
func TestShowEpisodeEmptyOrSpecialBody(t *testing.T) {
	t.Parallel()

	const special = `{"season":0,"number":3,"title":"Special","ids":{"trakt":77}}`

	for _, tool := range []struct{ name, route string }{
		{toolShowNextEpisode, showExtraRoute("/next_episode")},
		{toolShowLastEpisode, showExtraRoute("/last_episode")},
	} {
		for _, body := range []string{"null", emptyObject, `{"season":1,"number":2,"title":"No id"}`} {
			t.Run(tool.name+" "+body, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				h.trakt.respond(tool.route, http.StatusOK, body, nil)

				var out map[string]any

				h.ok(tool.name, showShow(testShowSlug), &out)

				if len(out) != 0 {
					t.Errorf("result = %v, want an empty object", out)
				}
			})
		}

		t.Run(tool.name+" special", func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tool.route, http.StatusOK, special, nil)

			var out map[string]any

			h.ok(tool.name, showShow(testShowSlug), &out)

			if s, ok := out["season"]; !ok || s != 0.0 || out["number"] != 3.0 {
				t.Errorf("result = %v, want season 0 and number 3", out)
			}
		})
	}
}

// TestShowExtraTraktFailures covers every endpoint of the group: each Trakt
// failure maps to its own message and never echoes Trakt's text.
func TestShowExtraTraktFailures(t *testing.T) {
	t.Parallel()

	const (
		notAList   = `{"not":"a list"}`
		notAnArray = `[1]`
	)

	args := showShow(testShowSlug)

	runUpstreamFailures(t, toolGetStats, showItem(testShowSlug), showExtraRoute("/stats"), notAnArray)
	runUpstreamFailures(t, toolListWatching, showItem(testShowSlug), showExtraRoute("/watching"), notAList)
	runUpstreamFailures(t, toolListTranslations, showItem(testShowSlug), showExtraRoute("/translations"), notAList)
	runUpstreamFailures(t, toolShowAliases, args, showExtraRoute("/aliases"), notAList)
	runUpstreamFailures(t, toolShowStudios, args, showExtraRoute("/studios"), notAList)
	runUpstreamFailures(t, toolListItemLists, showItem(testShowSlug), showExtraRoute(pathShowListsAll), notAList)
	runUpstreamFailures(t, toolShowNextEpisode, args, showExtraRoute("/next_episode"), notAnArray)
	runUpstreamFailures(t, toolShowLastEpisode, args, showExtraRoute("/last_episode"), notAnArray)
	runUpstreamFailures(t, toolShowCertifications, args, showExtraRoute("/certifications"), notAList)
}

// TestShowListOwnerIsBounded requires the owner name of a list to be cut.
func TestShowListOwnerIsBounded(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond(showExtraRoute(pathShowListsAll), http.StatusOK,
		`[{"name":"n","user":{"username":"`+strings.Repeat("x", 5*maxReleaseNoteRunes)+`"}}]`, nil)

	var lists Listing[TraktList]

	h.ok(toolListItemLists, showItem(testShowSlug), &lists)

	if o := lists.Items[0].Owner; len([]rune(o)) > maxTitleRunes {
		t.Errorf("owner has %d runes, want at most %d", len([]rune(o)), maxTitleRunes)
	}
}

// TestShowExtraTextIsBounded requires third-party text (list names, studio
// and alias names, certifications) to be cut.
func TestShowExtraTextIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 5*maxReleaseNoteRunes)

	h := newHarness(t)
	h.trakt.respond(showExtraRoute("/certifications"), http.StatusOK, `[{"certification":"`+long+`","country":"`+long+`"}]`, nil)

	var certs Listing[ShowCertification]

	h.ok(toolShowCertifications, showShow(testShowSlug), &certs)

	if c := certs.Items[0]; len([]rune(c.Certification)) > maxCertificationRunes || len([]rune(c.Country)) > maxCountryFieldRunes {
		t.Errorf("certification = %+v, want bounded text", c)
	}

	h = newHarness(t)
	h.trakt.respond(showExtraRoute("/studios"), http.StatusOK, `[{"name":"`+long+`","country":"`+long+`","ids":{"trakt":1,"slug":"`+long+`"}}]`, nil)

	var studios Listing[Studio]

	h.ok(toolShowStudios, showShow(testShowSlug), &studios)

	if s := studios.Items[0]; len([]rune(s.Name)) > maxTitleRunes || len([]rune(s.Country)) > maxCountryFieldRunes || len([]rune(s.IDs.Slug)) > maxSlugRunes {
		t.Errorf("studio = %+v, want bounded text", s)
	}

	h = newHarness(t)
	h.trakt.respond(showExtraRoute("/aliases"), http.StatusOK, `[{"title":"`+long+`","country":"`+long+`"}]`, nil)

	var aliases Listing[Alias]

	h.ok(toolShowAliases, showShow(testShowSlug), &aliases)

	if a := aliases.Items[0]; len([]rune(a.Title)) > maxTitleRunes || len([]rune(a.Country)) > maxCountryFieldRunes {
		t.Errorf("alias = %+v, want bounded text", a)
	}

	h = newHarness(t)
	h.trakt.respond(showExtraRoute(pathShowListsAll), http.StatusOK, `[{"name":"`+long+`","description":"`+long+`","ids":{"trakt":1,"slug":"s"},"user":{"username":"u","ids":{"slug":"u","trakt":1}}}]`, nil)

	var lists Listing[TraktList]

	h.ok(toolListItemLists, showItem(testShowSlug), &lists)

	if l := lists.Items[0]; len([]rune(l.Name)) > maxTitleRunes {
		t.Errorf("list name has %d runes, want at most %d", len([]rune(l.Name)), maxTitleRunes)
	}
}
