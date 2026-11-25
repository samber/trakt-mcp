package tools

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestGetSummaryArguments(t *testing.T) {
	t.Parallel()

	notExtended := map[string]any{queryExtended: false}

	cases := []argCase{
		{name: "type empty", tool: toolGetSummary, args: merge(movieItem(testMovieSlug), map[string]any{argType: ""}), wantErr: invalid(fieldType)},
		{name: "type upper case", tool: toolGetSummary, args: merge(movieItem(testMovieSlug), map[string]any{argType: "Movie"}), wantErr: invalid(fieldType)},
		{name: "type person", tool: toolGetSummary, args: merge(movieItem(testMovieSlug), map[string]any{argType: itemPerson}), wantErr: invalid(fieldType)},
		{name: "movie with season", tool: toolGetSummary, args: merge(movieItem(testMovieSlug), map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
		{name: "show with season", tool: toolGetSummary, args: merge(showItem(testShowSlug), map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
		{name: "season without season", tool: toolGetSummary, args: merge(showItem(testShowSlug), map[string]any{argType: typeSeason}), wantErr: invalid(fieldSeason)},
		{name: "episode without episode", tool: toolGetSummary, args: merge(seasonItem(testShowSlug, 1), map[string]any{argType: typeEpisode}), wantErr: invalid(fieldEpisode)},
		{name: "episode without season", tool: toolGetSummary, args: map[string]any{argType: typeEpisode, argID: testShowSlug, fieldEpisode: 2}, wantErr: invalid(fieldSeason)},
		{name: "season extended refused", tool: toolGetSummary, args: merge(seasonItem(testShowSlug, 1), notExtended), wantErr: invalid(fieldExtended)},
		{name: "episode extended refused", tool: toolGetSummary, args: merge(episodeItem(testShowSlug, 1, 2), notExtended), wantErr: invalid(fieldExtended)},
		{name: "movie not extended", tool: toolGetSummary, args: merge(movieItem("481"), notExtended), route: "GET /movies/481", body: emptyObject, noQuery: []string{queryExtended}},
		{name: "episode 9999", tool: toolGetSummary, args: episodeItem(testShowSlug, 1, maxEpisode), route: "GET " + episodeBaseFor(maxEpisode), body: emptyObject},
		{name: "season max", tool: toolGetSummary, args: seasonItem(testShowSlug, maxSeason), route: "GET " + seasonBaseFor(maxSeason) + "/info", body: emptyObject},
	}

	runArgCases(t, cases)
}

// TestGetSummaryTypeEchoed requires the result's type to repeat the type
// argument for every accepted type.
func TestGetSummaryTypeEchoed(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		args  map[string]any
		route string
	}{
		typeMovie:   {movieItem("481"), "GET /movies/481"},
		typeShow:    {showItem(testShowSlug), "GET /shows/breaking-bad"},
		typeSeason:  {seasonItem(testShowSlug, 1), seasonRoute("/info")},
		typeEpisode: {episodeItem(testShowSlug, 1, 2), episodeRoute("")},
	}

	for typ, tc := range cases {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tc.route, http.StatusOK, emptyObject, nil)

			var out typedOut[map[string]any]

			h.ok(toolGetSummary, tc.args, &out)

			if out.Type != typ || out.Item == nil {
				t.Errorf("result = %+v, want type %q and an item", out, typ)
			}
		})
	}
}

// TestLegacySummaryToolsRemoved requires the per-type summary tools to be
// gone: not registered, and an unknown tool to a client still calling them.
func TestLegacySummaryToolsRemoved(t *testing.T) {
	t.Parallel()

	legacy := []string{legacyToolPrefix + "movie_summary", legacyToolPrefix + "show_summary", legacyToolPrefix + "season_info", legacyToolPrefix + "episode_summary"}

	for _, spec := range NewServer(offlineDeps()).Specs() {
		if slices.Contains(legacy, spec.Name) {
			t.Errorf("%s is still registered", spec.Name)
		}
	}

	h := newHarness(t)

	for _, name := range legacy {
		res, err := h.session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{fieldMovieID: "481"}})
		if err == nil && (res == nil || !res.IsError) {
			t.Errorf("%s: call succeeded, want an unknown-tool error", name)
		}
	}

	if got := len(h.trakt.seen()); got != 0 {
		t.Errorf("Trakt requests = %d, want 0", got)
	}
}
