package tools

import (
	"net/http"
	"testing"
)

// itemExtraTools are the tools taking an ItemRef plus arguments of their own.
var itemExtraTools = []string{toolListComments, toolListPeople, toolListVideos, toolListItemLists}

// itemExtraSuffixes are the Trakt endpoint suffixes of the tools.
var itemExtraSuffixes = map[string]string{
	toolListComments: "/comments/newest", toolListPeople: "/people", toolListVideos: "/videos", toolListItemLists: testSeasonPathTail,
}

func TestItemExtraToolArguments(t *testing.T) {
	t.Parallel()

	var cases []argCase

	for _, tool := range itemExtraTools {
		withType := func(typ string) map[string]any { return merge(movieItem(testMovieSlug), map[string]any{argType: typ}) }

		cases = append(cases,
			argCase{name: tool + " type empty", tool: tool, args: withType(""), wantErr: invalid(fieldType)},
			argCase{name: tool + " type upper case", tool: tool, args: withType("Movie"), wantErr: invalid(fieldType)},
			argCase{name: tool + " type person", tool: tool, args: withType(itemPerson), wantErr: invalid(fieldType)},
			argCase{name: tool + " movie with season", tool: tool, args: merge(movieItem(testMovieSlug), map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
			argCase{name: tool + " season without season", tool: tool, args: merge(showItem(testShowSlug), map[string]any{argType: typeSeason}), wantErr: invalid(fieldSeason)},
			argCase{name: tool + " episode without season", tool: tool, args: map[string]any{argType: typeEpisode, argID: testShowSlug, fieldEpisode: 2}, wantErr: invalid(fieldSeason)},
			argCase{name: tool + " episode without episode", tool: tool, args: merge(seasonItem(testShowSlug, 1), map[string]any{argType: typeEpisode}), wantErr: invalid(fieldEpisode)},
			argCase{name: tool + " season with episode", tool: tool, args: merge(seasonItem(testShowSlug, 1), map[string]any{fieldEpisode: 2}), wantErr: invalid(fieldEpisode)},
			argCase{name: tool + " id person imdb", tool: tool, args: movieItem(personIMDb), wantErr: invalid(fieldID)},
		)
	}

	cases = append(cases,
		argCase{name: "people guest stars on a movie", tool: toolListPeople, args: merge(movieItem(testMovieSlug), map[string]any{argGuestStars: true}), wantErr: invalid(fieldIncludeGuestStars)},
		argCase{name: "people guest stars on a season", tool: toolListPeople, args: merge(seasonItem(testShowSlug, 1), map[string]any{argGuestStars: false}), wantErr: invalid(fieldIncludeGuestStars)},
		argCase{name: "people guest stars on an episode", tool: toolListPeople, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argGuestStars: true}), wantErr: invalid(fieldIncludeGuestStars)},
		argCase{name: "videos embed markdown on a season", tool: toolListVideos, args: merge(seasonItem(testShowSlug, 1), map[string]any{argEmbedMarkdown: false}), wantErr: invalid(fieldEmbedMarkdown)},
		argCase{name: "videos embed markdown on an episode", tool: toolListVideos, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argEmbedMarkdown: true}), wantErr: invalid(fieldEmbedMarkdown)},
	)

	runArgCases(t, cases)
}

// TestItemExtraToolsRouteByType requires each type to reach its own Trakt
// endpoint, for every tool.
func TestItemExtraToolsRouteByType(t *testing.T) {
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

	for _, tool := range itemExtraTools {
		for typ, item := range items {
			t.Run(tool+"/"+typ, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				body := emptyList

				if tool == toolListPeople {
					body = emptyObject
				}

				h.trakt.respond("GET "+item.base+itemExtraSuffixes[tool], http.StatusOK, body, nil)
				h.ok(tool, item.args, nil)

				if got := len(h.trakt.seen()); got != 1 {
					t.Errorf("Trakt requests = %d, want 1", got)
				}
			})
		}
	}
}

// TestItemExtraToolsNotFound requires a Trakt 404 to give the not-found
// tool error for every tool.
func TestItemExtraToolsNotFound(t *testing.T) {
	t.Parallel()

	for _, tool := range itemExtraTools {
		t.Run(tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond("GET "+episodeBaseFor(2)+itemExtraSuffixes[tool], http.StatusNotFound, emptyObject, nil)
			h.fail(tool, episodeItem(testShowSlug, 1, 2), msgNotFound)
		})
	}
}

// TestLegacyItemExtraToolsRemoved requires the per-type tools to be gone.
func TestLegacyItemExtraToolsRemoved(t *testing.T) {
	t.Parallel()

	requireLegacyToolsRemoved(t, "comments", "people", "videos", "lists")
}
