package tools

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	toolGetRatings       = "get_ratings"
	toolGetStats         = "get_stats"
	toolListWatching     = "list_watching"
	toolListTranslations = "list_translations"
	toolListPeople       = "list_people"
	toolListVideos       = "list_videos"
	toolListItemLists    = "list_item_lists"
)

// itemInfoTools are the tools taking an ItemRef whose result is the same
// for every type.
var itemInfoTools = []string{toolGetRatings, toolGetStats, toolListWatching, toolListTranslations}

// itemInfoSuffixes are the Trakt endpoint suffixes of the tools.
var itemInfoSuffixes = map[string]string{
	toolGetRatings: "/ratings", toolGetStats: "/stats", toolListWatching: "/watching", toolListTranslations: "/translations",
}

func TestItemInfoToolArguments(t *testing.T) {
	t.Parallel()

	var cases []argCase

	for _, tool := range itemInfoTools {
		withType := func(typ string) map[string]any { return merge(movieItem(testMovieSlug), map[string]any{argType: typ}) }

		cases = append(cases,
			argCase{name: tool + " type empty", tool: tool, args: withType(""), wantErr: invalid(fieldType)},
			argCase{name: tool + " type upper case", tool: tool, args: withType("Movie"), wantErr: invalid(fieldType)},
			argCase{name: tool + " type person", tool: tool, args: withType(itemPerson), wantErr: invalid(fieldType)},
			argCase{name: tool + " movie with season", tool: tool, args: merge(movieItem(testMovieSlug), map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
			argCase{name: tool + " show with season", tool: tool, args: merge(showItem(testShowSlug), map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
			argCase{name: tool + " season without season", tool: tool, args: merge(showItem(testShowSlug), map[string]any{argType: typeSeason}), wantErr: invalid(fieldSeason)},
			argCase{name: tool + " episode without season", tool: tool, args: map[string]any{argType: typeEpisode, argID: testShowSlug, fieldEpisode: 2}, wantErr: invalid(fieldSeason)},
			argCase{name: tool + " episode without episode", tool: tool, args: merge(seasonItem(testShowSlug, 1), map[string]any{argType: typeEpisode}), wantErr: invalid(fieldEpisode)},
			argCase{name: tool + " season with episode", tool: tool, args: merge(seasonItem(testShowSlug, 1), map[string]any{fieldEpisode: 2}), wantErr: invalid(fieldEpisode)},
			argCase{name: tool + " id person imdb", tool: tool, args: movieItem(personIMDb), wantErr: invalid(fieldID)},
		)
	}

	runArgCases(t, cases)
}

// TestItemInfoToolsRouteByType requires each type to reach its own Trakt
// endpoint, for every tool.
func TestItemInfoToolsRouteByType(t *testing.T) {
	t.Parallel()

	items := map[string]struct {
		args map[string]any
		base string
	}{
		typeMovie:   {movieItem(testMovieSlug), routeMovieSlug},
		typeShow:    {showItem(testShowSlug), routeShowSlug},
		typeSeason:  {seasonItem(testShowSlug, 1), seasonBase},
		typeEpisode: {episodeItem(testShowSlug, 1, 2), episodeBaseFor(2)},
	}

	for _, tool := range itemInfoTools {
		for typ, item := range items {
			t.Run(tool+"/"+typ, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				route := "GET " + item.base + itemInfoSuffixes[tool]
				body := emptyObject

				if tool == toolListWatching || tool == toolListTranslations {
					body = emptyList
				}

				h.trakt.respond(route, http.StatusOK, body, nil)
				h.ok(tool, item.args, nil)

				if got := len(h.trakt.seen()); got != 1 {
					t.Errorf("Trakt requests = %d, want 1", got)
				}
			})
		}
	}
}

// TestItemInfoToolsNotFound requires a Trakt 404 to give the same not-found
// tool error as the per-type tools gave.
func TestItemInfoToolsNotFound(t *testing.T) {
	t.Parallel()

	for _, tool := range itemInfoTools {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond("GET "+episodeBaseFor(2)+itemInfoSuffixes[tool], http.StatusNotFound, emptyObject, nil)
			h.fail(tool, episodeItem(testShowSlug, 1, 2), msgNotFound)
		})
	}
}

// TestLegacyItemInfoToolsRemoved requires the per-type tools to be gone.
func TestLegacyItemInfoToolsRemoved(t *testing.T) {
	t.Parallel()

	requireLegacyToolsRemoved(t, "ratings", "stats", "watching", "translations")
}

// requireLegacyToolsRemoved requires the fetch_<type>_<suffix> tools to be
// gone: not registered, so a client still calling them gets an unknown-tool
// error and no Trakt request is made.
func requireLegacyToolsRemoved(t *testing.T, suffixes ...string) {
	t.Helper()

	var legacy []string

	for _, typ := range []string{typeMovie, typeShow, typeSeason, typeEpisode} {
		for _, suffix := range suffixes {
			legacy = append(legacy, "fetch_"+typ+"_"+suffix)
		}
	}

	requireToolsRemoved(t, legacy...)
}

// requireToolsRemoved requires the named tools to be gone: not registered,
// so a client still calling them gets an unknown-tool error and no Trakt
// request is made.
func requireToolsRemoved(t *testing.T, legacy ...string) {
	t.Helper()

	for _, spec := range NewServer(offlineDeps()).Specs() {
		if slices.Contains(legacy, spec.Name) {
			t.Errorf("%s is still registered", spec.Name)
		}
	}

	h := newHarness(t)

	for _, name := range legacy {
		res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: showItem(testShowSlug)})
		if err == nil && (res == nil || !res.IsError) {
			t.Errorf("%s: call succeeded, want an unknown-tool error", name)
		}
	}

	if got := len(h.trakt.seen()); got != 0 {
		t.Errorf("Trakt requests = %d, want 0", got)
	}
}
