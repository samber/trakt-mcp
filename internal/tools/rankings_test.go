package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolListPopular       = "list_popular"
	toolListAnticipated   = "list_anticipated"
	toolListFavorited     = "list_favorited"
	toolListPlayed        = "list_played"
	toolListWatched       = "list_watched"
	routeMoviesPrefix     = "GET /movies/"
	routeShowsPrefix      = "GET /shows/"
	routeSuffixWeekly     = "/weekly"
	routeSegmentTrending  = "trending"
	routeSegmentPopular   = "popular"
	routeSegmentAnticipat = "anticipated"
	routeSegmentFavorited = "favorited"
	routeSegmentPlayed    = "played"
	routeSegmentWatched   = "watched"
)

// rankingTool is one ranking and the Trakt path segment it reads; periodic is
// true for the rankings that end the path with a period.
type rankingTool struct {
	tool, segment string
	periodic      bool
}

var rankingTools = []rankingTool{
	{tool: toolTrending, segment: routeSegmentTrending},
	{tool: toolListPopular, segment: routeSegmentPopular},
	{tool: toolListAnticipated, segment: routeSegmentAnticipat},
	{tool: toolListFavorited, segment: routeSegmentFavorited, periodic: true},
	{tool: toolListPlayed, segment: routeSegmentPlayed, periodic: true},
	{tool: toolListWatched, segment: routeSegmentWatched, periodic: true},
}

// route is the Trakt route the ranking reads for a type with the default
// period.
func (r rankingTool) route(typ string) string {
	base := routeMoviesPrefix
	if typ == typeShow {
		base = routeShowsPrefix
	}

	if r.periodic {
		return base + r.segment + routeSuffixWeekly
	}

	return base + r.segment
}

// TestRankingToolsRouteByType requires each type to reach its own Trakt
// endpoint, to answer under its own type and to make exactly one request.
func TestRankingToolsRouteByType(t *testing.T) {
	t.Parallel()

	for _, r := range rankingTools {
		for _, typ := range []string{typeMovie, typeShow} {
			t.Run(r.tool+"/"+typ, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				h.trakt.respond(r.route(typ), http.StatusOK, emptyList, nil)

				var out typedOut[Listing[any]]

				h.ok(r.tool, typeArg(typ), &out)

				if out.Type != typ {
					t.Errorf("type = %q, want %q", out.Type, typ)
				}

				seen := h.trakt.seen()
				if len(seen) != 1 || seen[0].URL.Path != r.route(typ)[len("GET "):] {
					t.Errorf("requests = %v, want one to %s", seen, r.route(typ))
				}
			})
		}
	}
}

// TestRankingToolsNotFound requires a Trakt 404 to give the not-found tool
// error for both types.
func TestRankingToolsNotFound(t *testing.T) {
	t.Parallel()

	for _, r := range rankingTools {
		for _, typ := range []string{typeMovie, typeShow} {
			t.Run(r.tool+"/"+typ, func(t *testing.T) {
				t.Parallel()

				newHarness(t).fail(r.tool, typeArg(typ), msgNotFound)
			})
		}
	}
}

// TestRankingToolsRefuseOtherTypes requires a type other than movie and show,
// an empty or upper-case one included, to fail before any Trakt call.
func TestRankingToolsRefuseOtherTypes(t *testing.T) {
	t.Parallel()

	var cases []argCase

	for _, r := range rankingTools {
		for _, typ := range []string{typeSeason, typeEpisode, itemPerson, "", typeUpperCase, typeUpperCaseShow} {
			cases = append(cases, argCase{name: r.tool + " type " + typ, tool: r.tool, args: typeArg(typ), wantErr: invalid(fieldType)})
		}
	}

	runArgCases(t, cases)
}

// TestLegacyRankingToolsRemoved requires the per-type rankings to be gone.
func TestLegacyRankingToolsRemoved(t *testing.T) {
	t.Parallel()

	var legacy []string

	for _, r := range rankingTools {
		for _, typ := range []string{mediaMovies, mediaShows} {
			legacy = append(legacy, "fetch_"+r.segment+"_"+typ)
		}
	}

	requireToolsRemoved(t, legacy...)
}

// TestBareTypeToolsRefuseSeasonAndEpisode pins what a season or an episode
// argument does on a tool that takes a type and no id: the input schema lists
// no such property and forbids additional ones, so the call fails before any
// handler or Trakt request runs, for a movie and for a TV show alike.
func TestBareTypeToolsRefuseSeasonAndEpisode(t *testing.T) {
	t.Parallel()

	const schemaRejection = "unexpected additional properties"

	bare := map[string]map[string]any{
		toolListRecommendations: {},
		toolSearch:              {fieldQuery: "matrix"},
		toolListPersonCredits:   {argID: testPersonSlug},
	}
	for _, r := range rankingTools {
		bare[r.tool] = map[string]any{}
	}

	for tool, base := range bare {
		for _, typ := range []string{typeMovie, typeShow} {
			for _, extra := range []string{fieldSeason, fieldEpisode} {
				t.Run(tool+"/"+typ+"/"+extra, func(t *testing.T) {
					t.Parallel()

					h := newHarness(t)
					res := h.call(tool, merge(merge(typeArg(typ), base), map[string]any{extra: 1}))

					if !res.IsError || !strings.Contains(resultText(res), schemaRejection) || !strings.Contains(resultText(res), extra) {
						t.Errorf("result = %v %q, want a schema rejection naming %s", res.IsError, resultText(res), extra)
					}

					if got := len(h.trakt.seen()); got != 0 {
						t.Errorf("Trakt requests = %d, want 0", got)
					}
				})
			}
		}
	}
}
