package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	testShowSlug = "breaking-bad"
	// fixtureDir holds one Trakt response per catalog tool.
	fixtureDir = "testdata/trakt"
	// emptyList and emptyObject are the bodies Trakt sends for "nothing".
	emptyList   = `[]`
	emptyObject = `{}`
	nullBody    = `null`
)

const (
	keyTitle             = "title"
	keyOverview          = "overview"
	keyImages            = "images"
	pathPageCount        = "page.page_count"
	traversalID          = "../x"
	personIMDb           = "nm0186505"
	testFirstAired       = "2008-01-20T02:00:00.000Z"
	keyRating            = "rating"
	keyVotes             = "votes"
	pathSecondTitle      = "items.1.title"
	testShowTitle        = "Breaking Bad"
	testShowIMDb         = "tt0903747"
	queryExtended        = "extended"
	keySlug              = "slug"
	keyTrakt             = "trakt"
	argEmbedMarkdown     = "embed_markdown"
	argGuestStars        = "include_guest_stars"
	pathSecondShowGenres = "items.1.show.genres"
	pathFirstTitle       = "items.0.title"
	pathSecondGenres     = "items.1.genres"
	pathFirstShowTitle   = "items.0.show.title"
	toolFetchShowSeasons = "list_seasons"
)

// catalogCase is one catalog tool run against its fixture: the request it
// must make and the output fields it must return.
type catalogCase struct {
	tool    string
	args    map[string]any
	route   string
	fixture string
	// headers are sent with the fixture, to give a paginated tool its
	// X-Pagination-* metadata.
	headers map[string]string
	// wantQuery holds query values the Trakt request must carry.
	wantQuery map[string]string
	// want maps dotted output paths (pathFirstShowTitle) to expected values,
	// compared by their printed form.
	want map[string]any
	// absent lists output paths that must not exist: fields Trakt sends that
	// the tool drops, and fields it omits when Trakt had no value.
	absent []string
	// object is true when the Trakt body is an object, not
	// lists, which picks the empty bodies the nil-array test sends.
	object bool
}

// catalogCaseSets collects the cases of each tool group; each _test.go file
// adds its own in init.
var catalogCaseSets [][]catalogCase

func allCatalogCases() []catalogCase {
	var all []catalogCase
	for _, set := range catalogCaseSets {
		all = append(all, set...)
	}

	return all
}

func init() { catalogCaseSets = append(catalogCaseSets, showCases) }

func fixture(t *testing.T, name string) string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(fixtureDir, name+".json")) //nolint:gosec // G304: name is a constant of the calling test
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

// pagedHeaders are the pagination headers every paged case sends.
var pagedHeaders = map[string]string{
	"X-Pagination-Page": "1", "X-Pagination-Page-Count": "3", "X-Pagination-Item-Count": "6", "X-Pagination-Limit": "2",
}

// pagedArgs asks for page 1 of 2 items so the page metadata is returned.
var pagedArgs = map[string]any{fieldLimit: 2, fieldPage: 1}

func merge(base, extra map[string]any) map[string]any {
	out := map[string]any{}

	maps.Copy(out, base)
	maps.Copy(out, extra)

	return out
}

// lookup follows a dotted path through decoded JSON; numeric segments index
// arrays.
func lookup(v any, path string) (any, bool) {
	for seg := range strings.SplitSeq(path, ".") {
		switch node := v.(type) {
		case map[string]any:
			next, ok := node[seg]
			if !ok {
				return nil, false
			}

			v = next
		case []any:
			i, err := strconv.Atoi(seg)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}

			v = node[i]
		default:
			return nil, false
		}
	}

	return v, true
}

// findNull returns the path of the first JSON null in v, or "".
func findNull(v any, path string) string {
	switch node := v.(type) {
	case nil:
		return path
	case map[string]any:
		for k, child := range node {
			if p := findNull(child, path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		for i, child := range node {
			if p := findNull(child, path+"."+strconv.Itoa(i)); p != "" {
				return p
			}
		}
	}

	return ""
}

func showShow(slug string) map[string]any { return map[string]any{fieldID: slug} }

var showCases = []catalogCase{
	{
		tool: toolTrending, args: merge(typeArg(typeShow), pagedArgs), route: "GET /shows/trending", fixture: "show_trending", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2", queryExtended: extendedFull},
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{
			"items.0.watchers": 12, pathFirstShowTitle: testShowTitle, "items.0.show.year": 2008,
			"items.0.show.ids.trakt": 1388, "items.0.show.ids.slug": testShowSlug, "items.0.show.ids.imdb": testShowIMDb,
			"items.0.show.ids.tmdb": 1396, "items.0.show.ids.tvdb": 81189, "items.0.show.rating": 9.3, "items.0.show.votes": 100000,
			"items.0.show.genres": []any{"drama", "crime"}, "items.0.show.network": "AMC", "items.0.show.status": "ended",
			"items.1.watchers": 3, pathSecondShowGenres: []any{}, "items.1.show.ids.slug": "better-call-saul",
			pathPageCount: 3, "page.item_count": 6,
		})),
	},
	{
		tool: toolListPopular, args: typeArg(typeShow), route: "GET /shows/popular", fixture: "show_popular",
		wantQuery: map[string]string{queryExtended: extendedFull},
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{
			pathFirstTitle: testShowTitle, "items.0.ids.slug": testShowSlug, "items.0.first_aired": testFirstAired,
			"items.0.runtime": 45, "items.0.certification": "TV-MA", "items.0.aired_episodes": 62, pathSecondGenres: []any{},
		})),
	},
	{
		tool: toolListAnticipated, args: typeArg(typeShow), route: "GET /shows/anticipated", fixture: "show_anticipated",
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{"items.0.list_count": 4000, pathFirstShowTitle: testShowTitle, pathSecondShowGenres: []any{}})),
	},
	{
		tool: toolListFavorited, args: merge(typeArg(typeShow), map[string]any{fieldPeriod: periodMonthly}), route: "GET /shows/favorited/monthly", fixture: "show_favorited",
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{"items.0.user_count": 900, "items.0.show.ids.slug": testShowSlug, pathSecondShowGenres: []any{}})),
	},
	{
		tool: toolListPlayed, args: merge(typeArg(typeShow), map[string]any{fieldPeriod: periodDaily}), route: "GET /shows/played/daily", fixture: "show_played",
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{
			"items.0.watcher_count": 70, "items.0.play_count": 500, "items.0.collected_count": 40, "items.0.collector_count": 30,
			pathFirstShowTitle: testShowTitle, pathSecondShowGenres: []any{},
		})),
	},
	{
		tool: toolListWatched, args: typeArg(typeShow), route: "GET /shows/watched/weekly", fixture: "show_watched",
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{"items.0.watcher_count": 80, "items.0.play_count": 600, "items.0.show.ids.imdb": testShowIMDb, pathSecondShowGenres: []any{}})),
	},
	{
		tool: toolSearch, args: merge(typeArg(typeShow), map[string]any{fieldQuery: "  breaking  "}), route: routeSearchShow, fixture: "show_search",
		wantQuery: map[string]string{"query": "breaking"},
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{
			"items.0.score": 97.5, pathFirstTitle: testShowTitle, "items.0.year": 2008, "items.0.ids.slug": testShowSlug,
			"items.1.score": 12.25, "items.1.ids.trakt": 59660,
		})),
	},
	{
		tool: toolGetRatings, args: showItem(testShowSlug), route: "GET /shows/breaking-bad/ratings", fixture: "show_ratings", object: true,
		want: map[string]any{keyRating: 9.3, keyVotes: 1000, "distribution": []any{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0}},
	},
	{
		tool: toolGetSummary, args: showItem(testShowSlug), route: "GET /shows/breaking-bad", fixture: "show_summary", object: true,
		wantQuery: map[string]string{queryExtended: extendedFull},
		want: inItem(map[string]any{
			keyTitle: testShowTitle, "year": 2008, "ids.trakt": 1388, "ids.imdb": testShowIMDb, keyOverview: "A chemistry teacher turns to crime.",
			"runtime": 45, "certification": "TV-MA", "network": "AMC", "status": "ended", "genres": []any{"drama", "crime"}, "aired_episodes": 62,
		}),
	},
	{
		tool: toolListVideos, args: showItem(testShowSlug), route: "GET /shows/breaking-bad/videos", fixture: "show_videos",
		want: map[string]any{
			pathFirstTitle: "Official [Trailer]", "items.0.type": "trailer", "items.0.site": "youtube",
			"items.0.url": "https://www.youtube.com/watch?v=abc", "items.0.published_at": "2008-01-01T00:00:00.000Z",
			"items.0.markdown": `[Official \[Trailer\]](https://www.youtube.com/watch?v=abc)`,
			pathSecondTitle:    "Clip (1)", "items.1.markdown": `[Clip (1)](https://vimeo.com/a%28b%29)`,
		},
	},
	{
		tool: toolListRelated, args: merge(showItem(testShowSlug), pagedArgs), route: "GET /shows/breaking-bad/related", fixture: "show_related", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want:      merge(map[string]any{argType: typeShow}, inItem(map[string]any{pathFirstTitle: "Better Call Saul", "items.0.genres": []any{}, pathSecondTitle: testShowTitle, pathPageCount: 3})),
	},
	{
		tool: toolFetchShowSeasons, args: showShow(testShowSlug), route: "GET /shows/breaking-bad/seasons", fixture: "show_seasons",
		want: map[string]any{
			"items.0.number": 0, pathFirstTitle: "Specials", "items.0.episode_count": 9, "items.0.ids.trakt": 3960,
			"items.1.number": 1, "items.1.overview": "The first season.", "items.1.rating": 8.9, "items.1.ids.tvdb": 30272, "items.1.first_aired": testFirstAired,
		},
	},
	{
		tool: toolListPeople, args: showItem(testShowSlug), route: "GET /shows/breaking-bad/people", fixture: "show_people", object: true,
		want: map[string]any{
			"cast.0.name": "Bryan Cranston", "cast.0.character": "Walter White", "cast.0.episode_count": 62, "cast.0.ids.slug": "bryan-cranston",
			"cast.1.name": "Aaron Paul", "crew.0.department": "directing", "crew.0.members.0.name": "Vince Gilligan",
			"crew.0.members.0.job": "Director", "crew.1.department": "writing", "crew.1.members.0.job": "Creator",
		},
	},
}

// TestShowToolsOutputs runs every catalog tool through the real server,
// middleware and client against its fixture.
func TestShowToolsOutputs(t *testing.T) {
	t.Parallel()

	for _, tc := range allCatalogCases() {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tc.route, http.StatusOK, fixture(t, tc.fixture), tc.headers)

			var out any

			res := h.ok(tc.tool, tc.args, &out)

			for path, want := range tc.want {
				got, ok := lookup(out, path)
				if !ok {
					t.Errorf("%s: no field %q in %s", tc.tool, path, resultText(res))
					continue
				}

				if fmt.Sprint(got) != fmt.Sprint(want) {
					t.Errorf("%s: %s = %v, want %v", tc.tool, path, got, want)
				}
			}

			for _, path := range tc.absent {
				if got, ok := lookup(out, path); ok {
					t.Errorf("%s: field %q = %v, want it absent in %s", tc.tool, path, got, resultText(res))
				}
			}

			if p := findNull(out, ""); p != "" {
				t.Errorf("%s: null at %s in %s", tc.tool, p, resultText(res))
			}

			requireQuery(t, tc, h.trakt.seen())
		})
	}
}

func requireQuery(t *testing.T, tc catalogCase, seen []*http.Request) {
	t.Helper()

	if len(seen) != 1 {
		t.Fatalf("%s: %d Trakt requests, want 1", tc.tool, len(seen))
	}

	q := seen[0].URL.Query()
	for k, want := range tc.wantQuery {
		if got := q.Get(k); got != want {
			t.Errorf("%s: query %s = %q, want %q", tc.tool, k, got, want)
		}
	}
}

// TestShowToolsNeverReturnNullArrays feeds every catalog tool the bodies
// Trakt sends for nothing and requires no JSON null in the output.
func TestShowToolsNeverReturnNullArrays(t *testing.T) {
	t.Parallel()

	for _, tc := range allCatalogCases() {
		bodies := []string{emptyList, nullBody}
		if tc.object {
			bodies = []string{emptyObject, nullBody}
		}

		for _, body := range bodies {
			t.Run(tc.tool+"/"+body, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				h.trakt.respond(tc.route, http.StatusOK, body, nil)

				var out any

				res := h.ok(tc.tool, tc.args, &out)

				if p := findNull(out, ""); p != "" {
					t.Errorf("%s: null at %s in %s", tc.tool, p, resultText(res))
				}
			})
		}
	}
}

// TestShowNotFoundIsToolError requires every catalog tool to turn a Trakt 404
// into the "not found" tool error.
func TestShowNotFoundIsToolError(t *testing.T) {
	t.Parallel()

	for _, tc := range allCatalogCases() {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.fail(tc.tool, tc.args, msgNotFound)
		})
	}
}

// TestShowToolsAreAllReadOnly requires every catalog tool to advertise the
// read-only, non-destructive, open-world annotations.
func TestShowToolsAreAllReadOnly(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for _, tc := range allCatalogCases() {
		tool, ok := tools[tc.tool]
		if !ok {
			t.Errorf("tool %s is not registered", tc.tool)
			continue
		}

		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || (a.DestructiveHint != nil && *a.DestructiveHint) || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s annotations = %+v, want read-only and open-world", tc.tool, a)
		}

		if tool.Title == "" || tool.Description == "" {
			t.Errorf("%s has an empty title or description", tc.tool)
		}
	}
}

// argCase is one argument-validation case: either an exact tool error, or a
// successful call whose Trakt request is checked.
type argCase struct {
	name, tool string
	args       map[string]any
	// wantErr is the exact tool error; empty means the call must succeed.
	wantErr string
	// route and body answer a call that must succeed.
	route, body string
	wantPath    string
	wantQuery   map[string]string
	// noQuery lists query keys the request must not carry.
	noQuery []string
}

func runArgCases(t *testing.T, cases []argCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, withOnlyTools(tc.tool))

			if tc.wantErr != "" {
				h.fail(tc.tool, tc.args, tc.wantErr)

				if got := len(h.trakt.seen()); got != 0 {
					t.Fatalf("Trakt requests = %d, want 0", got)
				}

				return
			}

			h.trakt.respond(tc.route, http.StatusOK, tc.body, nil)
			h.ok(tc.tool, tc.args, nil)

			seen := h.trakt.seen()
			if len(seen) != 1 {
				t.Fatalf("Trakt requests = %d, want 1", len(seen))
			}

			if tc.wantPath != "" && seen[0].URL.Path != tc.wantPath {
				t.Errorf("path = %q, want %q", seen[0].URL.Path, tc.wantPath)
			}

			q := seen[0].URL.Query()
			for k, want := range tc.wantQuery {
				if got := q.Get(k); got != want {
					t.Errorf("query %s = %q, want %q", k, got, want)
				}
			}

			for _, k := range tc.noQuery {
				if q.Has(k) {
					t.Errorf("query has %s = %q, want none", k, q.Get(k))
				}
			}
		})
	}
}

func invalid(field string) string { return msgInvalidArgumentPrefix + field }

func TestShowToolArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{
		{name: "period default is weekly", tool: toolListFavorited, args: typeArg(typeShow), route: "GET /shows/favorited/weekly", body: emptyList},
		{name: "period yearly", tool: toolListPlayed, args: merge(typeArg(typeShow), map[string]any{fieldPeriod: periodYearly}), route: "GET /shows/played/yearly", body: emptyList},
		{name: "period all", tool: toolListWatched, args: merge(typeArg(typeShow), map[string]any{fieldPeriod: periodAll}), route: "GET /shows/watched/all", body: emptyList},
		{name: "period unknown", tool: toolListPlayed, args: merge(typeArg(typeShow), map[string]any{fieldPeriod: "hourly"}), wantErr: invalid(fieldPeriod)},
		{name: "period wrong case", tool: toolListFavorited, args: merge(typeArg(typeShow), map[string]any{fieldPeriod: "Weekly"}), wantErr: invalid(fieldPeriod)},
		{name: "period path injection", tool: toolListWatched, args: merge(typeArg(typeShow), map[string]any{fieldPeriod: traversalID}), wantErr: invalid(fieldPeriod)},
		{name: "limit 0 is the default", tool: toolListPopular, args: merge(typeArg(typeShow), map[string]any{fieldLimit: 0}), route: "GET /shows/popular", body: emptyList, wantQuery: map[string]string{"limit": "10"}},
		{name: "limit 101", tool: toolListPopular, args: merge(typeArg(typeShow), map[string]any{fieldLimit: 101}), wantErr: invalid(fieldLimit)},
		{name: "limit negative", tool: toolListPopular, args: merge(typeArg(typeShow), map[string]any{fieldLimit: -1}), wantErr: invalid(fieldLimit)},
		{name: "page 0", tool: toolListPopular, args: merge(typeArg(typeShow), map[string]any{fieldPage: 0}), wantErr: invalid(fieldPage)},
		{name: "id slug", tool: toolGetSummary, args: showItem("breaking-bad"), route: "GET /shows/breaking-bad", body: emptyObject, wantPath: "/shows/breaking-bad"},
		{name: "id numeric", tool: toolGetSummary, args: showItem("1388"), route: "GET /shows/1388", body: emptyObject, wantPath: "/shows/1388"},
		{name: "id imdb", tool: toolGetSummary, args: showItem(testShowIMDb), route: "GET /shows/tt0903747", body: emptyObject, wantPath: "/shows/tt0903747"},
		{name: "id person imdb", tool: toolGetSummary, args: showItem(personIMDb), wantErr: invalid(fieldID)},
		{name: "id path traversal", tool: toolGetSummary, args: showItem(traversalID), wantErr: invalid(fieldID)},
		{name: "id slash", tool: toolGetRatings, args: showItem("a/b"), wantErr: invalid(fieldID)},
		{name: "id query injection", tool: toolListPeople, args: showItem("x?extended=full"), wantErr: invalid(fieldID)},
		{name: "id empty", tool: toolFetchShowSeasons, args: showShow(""), wantErr: invalid(fieldID)},
		{name: "id upper case", tool: toolListRelated, args: showItem("Breaking-Bad"), wantErr: invalid(fieldID)},
		{name: "summary not extended", tool: toolGetSummary, args: merge(showItem("1388"), map[string]any{queryExtended: false}), route: "GET /shows/1388", body: emptyObject, noQuery: []string{queryExtended}},
		{name: "query empty", tool: toolSearch, args: searchArgs(typeShow, ""), wantErr: invalid(fieldQuery)},
		{name: "query blank", tool: toolSearch, args: searchArgs(typeShow, "   "), wantErr: invalid(fieldQuery)},
		{name: "query 200 runes", tool: toolSearch, args: searchArgs(typeShow, strings.Repeat("é", maxQueryRunes)), route: routeSearchShow, body: emptyList},
		{name: "query 201 runes", tool: toolSearch, args: searchArgs(typeShow, strings.Repeat("é", maxQueryRunes+1)), wantErr: invalid(fieldQuery)},
		{name: "people guest stars on", tool: toolListPeople, args: merge(showItem("1388"), map[string]any{argGuestStars: true}), route: "GET /shows/1388/people", body: emptyObject},
	}

	runArgCases(t, cases)
}

// TestSearchShowsEncodesQuery requires a query holding path and query
// delimiters to arrive as one encoded value on the unchanged path.
func TestSearchShowsEncodesQuery(t *testing.T) {
	t.Parallel()

	const query = "a/b?c=d&e"

	h := newHarness(t)
	h.trakt.respond(routeSearchShow, http.StatusOK, fixture(t, "show_search"), nil)
	h.ok(toolSearch, searchArgs(typeShow, query), nil)

	seen := h.trakt.seen()
	if len(seen) != 1 {
		t.Fatalf("Trakt requests = %d, want 1", len(seen))
	}

	req := seen[0]
	if req.URL.Path != "/search/show" {
		t.Errorf("path = %q, want /search/show", req.URL.Path)
	}

	if got := req.URL.Query()["query"]; !slices.Equal(got, []string{query}) {
		t.Errorf("query values = %q, want exactly %q", got, query)
	}

	if want := "query=" + url.QueryEscape(query); !strings.Contains(req.URL.RawQuery, want) {
		t.Errorf("raw query = %q, want it to hold %q", req.URL.RawQuery, want)
	}
}

func TestShowVideosMarkdown(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET /shows/breaking-bad/videos", http.StatusOK, fixture(t, "show_videos"), nil)

	var plain Listing[Video]

	h.ok(toolListVideos, merge(showItem(testShowSlug), map[string]any{argEmbedMarkdown: false}), &plain)

	if len(plain.Items) != 2 {
		t.Fatalf("items = %+v, want 2 (the javascript: video is dropped)", plain.Items)
	}

	for _, v := range plain.Items {
		if v.Markdown != "" || !strings.HasPrefix(v.URL, "https://") {
			t.Errorf("video = %+v, want a plain https url and no markdown", v)
		}
	}
}

func TestShowPeopleGuestStars(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	full := fixture(t, "show_people")

	// Like Trakt, the fake sends guest_stars only for ?extended=guest_stars.
	h.trakt.handle("GET /shows/breaking-bad/people", func(w http.ResponseWriter, r *http.Request) {
		body := full

		if r.URL.Query().Get("extended") != extendedGuestStars {
			var m map[string]json.RawMessage
			if err := json.Unmarshal([]byte(full), &m); err != nil {
				t.Errorf("fixture: %v", err)
			}

			delete(m, "guest_stars")

			b, err := json.Marshal(m)
			if err != nil {
				t.Errorf("marshal: %v", err)
			}

			body = string(b)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	})

	var without, with map[string]json.RawMessage

	h.ok(toolListPeople, showItem(testShowSlug), &without)

	if _, ok := without["guest_stars"]; ok {
		t.Errorf("guest_stars present without include_guest_stars: %s", without["guest_stars"])
	}

	h.ok(toolListPeople, merge(showItem(testShowSlug), map[string]any{argGuestStars: true}), &with)

	if got := string(with["guest_stars"]); !strings.Contains(got, "Giancarlo Esposito") || !strings.Contains(got, "Gus Fring") {
		t.Errorf("guest_stars = %s", got)
	}
}

func TestShowPeopleAreBounded(t *testing.T) {
	t.Parallel()

	credit := func(i int) map[string]any {
		return map[string]any{
			"character": "c", "person": map[string]any{"name": "n" + strconv.Itoa(i), "ids": map[string]any{keyTrakt: i, keySlug: "s"}},
		}
	}

	var cast, crew []map[string]any

	for i := range maxCastMembers + 25 {
		cast = append(cast, credit(i))
		crew = append(crew, credit(i))
	}

	body, err := json.Marshal(map[string]any{"cast": cast, "crew": map[string]any{"art": crew}, "guest_stars": cast})
	if err != nil {
		t.Fatal(err)
	}

	h := newHarness(t)
	h.trakt.respond("GET /shows/breaking-bad/people", http.StatusOK, string(body), nil)

	var out People

	h.ok(toolListPeople, merge(showItem(testShowSlug), map[string]any{argGuestStars: true}), &out)

	if len(out.Cast) != maxCastMembers || len(out.Crew[0].Members) != maxCrewPerDepartment || out.GuestStars == nil || len(*out.GuestStars) != maxGuestStars {
		t.Errorf("cast %d, crew %d, guests %v, want %d, %d, %d", len(out.Cast), len(out.Crew[0].Members), out.GuestStars, maxCastMembers, maxCrewPerDepartment, maxGuestStars)
	}
}

func TestShowRatingsDistributionOrder(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET /shows/breaking-bad/ratings", http.StatusOK, `{"rating": 7, "votes": 3, "distribution": {"10": 3, "1": 0, "2": 0, "3": 0, "4": 0, "5": 0, "6": 0, "7": 0, "8": 0, "9": 0}}`, nil)

	var out Rating

	h.ok(toolGetRatings, showItem(testShowSlug), &out)

	if want := []int{0, 0, 0, 0, 0, 0, 0, 0, 0, 3}; !slices.Equal(out.Distribution, want) {
		t.Errorf("distribution = %v, want %v", out.Distribution, want)
	}
}
