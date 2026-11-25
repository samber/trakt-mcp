package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
)

// movieJSON is a full Trakt movie object, with fields the tools drop.
func movieJSON(slug, overview string) string {
	m := map[string]any{
		"title": "The Matrix",
		"year":  1999,
		"ids": map[string]any{
			"trakt": 481, "slug": slug, "imdb": "tt0133093", "tmdb": 603,
			"plex": map[string]any{"guid": "plex-guid", "slug": "plex-slug"},
		},
		"tagline":       "dropped",
		"overview":      overview,
		"released":      "1999-03-31",
		"runtime":       136,
		"certification": "R",
		"rating":        8.7,
		"votes":         60000,
		"genres":        []string{"action", "science-fiction"},
		"homepage":      "https://dropped.example",
		"images":        map[string]any{"poster": []string{"dropped.jpg"}},
	}

	raw, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}

	return string(raw)
}

// trendingJSON has three entries whose genres are an array, null and
// omitted, so every shape of an empty list is exercised.
func trendingJSON() string {
	return `[
		{"watchers": 12, "movie": {"title": "A", "year": 2024, "ids": {"trakt": 1, "slug": "a", "imdb": "tt1", "tmdb": 11}, "genres": ["drama"]}},
		{"watchers": 7, "movie": {"title": "B", "year": 2023, "ids": {"trakt": 2, "slug": "b", "imdb": null, "tmdb": null}, "genres": null}},
		{"watchers": 3, "movie": {"title": "C", "ids": {"trakt": 3, "slug": "c"}}}
	]`
}

// TestPilotToolsAgainstFakeTrakt runs the two pilot tools through the real
// server, middleware and Trakt client against a fake Trakt.
func TestPilotToolsAgainstFakeTrakt(t *testing.T) {
	t.Parallel()

	for name, run := range map[string]func(*testing.T){
		"trending":                          pilotTrending,
		"trending null body":                pilotTrendingNullBody,
		"summary":                           pilotSummary,
		"summary not extended":              pilotSummaryNotExtended,
		"summary bad ids never reach trakt": pilotSummaryBadIDs,
		"summary not found":                 pilotSummaryNotFound,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			run(t)
		})
	}
}

func pilotTrending(t *testing.T) {
	t.Helper()

	h := newHarness(t)
	h.trakt.respond(trendingRoute, http.StatusOK, trendingJSON(), map[string]string{
		"X-Pagination-Page": "1", "X-Pagination-Page-Count": "4", "X-Pagination-Item-Count": "12", "X-Pagination-Limit": "3",
	})

	var out Listing[TrendingMovie]

	res := h.ok(toolTrending, map[string]any{fieldLimit: 3, fieldPage: 1}, &out)

	if len(out.Items) != 3 || out.Items[0].Watchers != 12 || out.Items[0].Movie.Title != "A" ||
		out.Items[0].Movie.IDs != (MovieIDs{Trakt: 1, Slug: "a", IMDb: "tt1", TMDB: 11}) {
		t.Fatalf("items = %+v", out.Items)
	}

	if out.Page == nil || out.Page.PageCount != 4 || out.Page.ItemCount != 12 {
		t.Errorf("page = %+v, want the header metadata", out.Page)
	}

	requireGenresArrays(t, resultText(res))
	requireTrendingRequest(t, h.trakt.seen()[0])
}

// requireGenresArrays fails unless every movie's genres in a trending
// output is a JSON array, never null, whatever Trakt sent.
func requireGenresArrays(t *testing.T, output string) {
	t.Helper()

	var wire struct {
		Items []struct {
			Movie map[string]json.RawMessage `json:"movie"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &wire); err != nil {
		t.Fatal(err)
	}

	for i, item := range wire.Items {
		if g := string(item.Movie["genres"]); !strings.HasPrefix(g, "[") {
			t.Errorf("item %d genres = %s, want an array", i, g)
		}
	}
}

// requireTrendingRequest checks the paging query and Trakt headers of a
// page-1, limit-3 trending request.
func requireTrendingRequest(t *testing.T, req *http.Request) {
	t.Helper()

	q := req.URL.Query()
	if q.Get("page") != "1" || q.Get("limit") != "3" || q.Get("extended") != extendedFull {
		t.Errorf("query = %s, want page, limit and extended=full", req.URL.RawQuery)
	}

	if req.Header.Get("Authorization") != "Bearer "+testTraktToken || req.Header.Get("trakt-api-version") != "2" {
		t.Errorf("headers = %v, want the user's token and API version 2", req.Header)
	}
}

func pilotTrendingNullBody(t *testing.T) {
	t.Helper()

	h := newHarness(t)
	h.trakt.respond(trendingRoute, http.StatusOK, `null`, nil)

	res := h.ok(toolTrending, map[string]any{}, nil)
	if got := resultText(res); got != `{"items":[]}` {
		t.Errorf("output = %s, want {\"items\":[]}", got)
	}
}

func pilotSummary(t *testing.T) {
	t.Helper()

	h := newHarness(t)
	long := strings.Repeat("word ", maxOverviewRunes)
	h.trakt.respond("GET /movies/tt0133093", http.StatusOK, movieJSON("the-matrix-1999", long), nil)

	var typedRes typedOut[Movie]

	res := h.ok(toolGetSummary, movieItem("tt0133093"), &typedRes)
	out := typedRes.Item

	if typedRes.Type != typeMovie {
		t.Errorf("type = %q, want %q", typedRes.Type, typeMovie)
	}

	want := Movie{
		Title: "The Matrix", Year: 1999,
		IDs:    MovieIDs{Trakt: 481, Slug: "the-matrix-1999", IMDb: "tt0133093", TMDB: 603},
		Rating: 8.7, Votes: 60000, Runtime: 136, Certification: "R", Released: "1999-03-31",
		Genres: []string{"action", "science-fiction"},
	}
	got := out
	got.Overview = ""

	if !moviesEqual(got, want) {
		t.Errorf("movie = %+v, want %+v", got, want)
	}

	if n := utf8.RuneCountInString(out.Overview); n == 0 || n > maxOverviewRunes {
		t.Errorf("overview has %d runes, want 1..%d", n, maxOverviewRunes)
	}

	for _, dropped := range []string{"tagline", "homepage", "images", "plex"} {
		if strings.Contains(resultText(res), dropped) {
			t.Errorf("output keeps %q: %s", dropped, resultText(res))
		}
	}

	if q := h.trakt.seen()[0].URL.Query(); q.Get("extended") != extendedFull {
		t.Errorf("query = %v, want extended=full by default", q)
	}
}

func pilotSummaryNotExtended(t *testing.T) {
	t.Helper()

	h := newHarness(t)
	h.trakt.respond("GET /movies/481", http.StatusOK, `{"title": "The Matrix", "year": 1999, "ids": {"trakt": 481, "slug": "the-matrix-1999"}}`, nil)

	var out typedOut[Movie]
	h.ok(toolGetSummary, merge(movieItem("481"), map[string]any{queryExtended: false}), &out)

	if out.Item.Genres == nil || len(out.Item.Genres) != 0 {
		t.Errorf("genres = %#v, want []", out.Item.Genres)
	}

	if q := h.trakt.seen()[0].URL.Query(); q.Has("extended") {
		t.Errorf("query = %v, want no extended", q)
	}
}

func pilotSummaryBadIDs(t *testing.T) {
	t.Helper()

	h := newHarness(t)
	for _, id := range []string{"../users/me", "a/b", "x?extended=full", "nm0186505", "", "Matrix"} {
		h.fail(toolGetSummary, movieItem(id), invalid(fieldID))
	}

	if got := len(h.trakt.seen()); got != 0 {
		t.Errorf("Trakt requests = %d, want 0", got)
	}
}

func pilotSummaryNotFound(t *testing.T) {
	t.Helper()

	h := newHarness(t)
	h.fail(toolGetSummary, movieItem("missing"), msgNotFound)
}

func moviesEqual(a, b Movie) bool {
	ra, errA := json.Marshal(a)
	rb, errB := json.Marshal(b)

	return errA == nil && errB == nil && string(ra) == string(rb)
}

// TestMovieToolsCapReplySize checks that both movie pilot tools read Trakt's
// answer through the shared maxReplyBytes cap: valid JSON padded past the cap
// must be refused, not read whole into memory.
func TestMovieToolsCapReplySize(t *testing.T) {
	t.Parallel()

	pad := strings.Repeat(" ", maxReplyBytes)

	h := newHarness(t)
	h.trakt.respond(trendingRoute, http.StatusOK, trendingJSON()+pad, nil)
	h.trakt.respond("GET /movies/481", http.StatusOK, `{"title": "The Matrix", "ids": {"trakt": 481, "slug": "the-matrix-1999"}}`+pad, nil)

	if res := h.call("fetch_trending_movies", map[string]any{fieldLimit: 3}); !res.IsError {
		t.Error("fetch_trending_movies: oversized answer accepted")
	}

	if res := h.call(toolGetSummary, movieItem("481")); !res.IsError {
		t.Error("get_summary: oversized answer accepted")
	}
}
