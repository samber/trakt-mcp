package tools

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const (
	toolMovieWatchNow   = "fetch_movie_watchnow"
	toolShowWatchNow    = "fetch_show_watchnow"
	toolEpisodeWatchNow = "fetch_episode_watchnow"
	toolWatchNowSources = "fetch_watchnow_sources"

	testCountry      = "fr"
	routeWatchNowSrc = "GET /watchnow/sources"
)

func init() { catalogCaseSets = append(catalogCaseSets, watchNowCases) }

func withCountry(args map[string]any, country string) map[string]any {
	return merge(args, map[string]any{fieldCountry: country})
}

var watchNowWant = map[string]any{
	fieldCountry:    testCountry,
	"free.0.source": "arte", "free.0.link": "https://example.com/arte/matrix", "free.0.uhd": false,
	"subscription.0.source": "netflix", "subscription.0.uhd": true,
	"purchase.0.source": "apple_tv", "purchase.0.buy_price": "9.99", "purchase.0.rent_price": "3.99", "purchase.0.currency": "EUR",
	"cable": []any{}, "cinema": []any{},
}

// watchNowAbsent lists fields Trakt sends that the tools drop, and fields
// omitted when empty; a link that is not http(s) is dropped, not passed on.
var watchNowAbsent = []string{"streaming_ranks", "free.0.currency", "free.0.rent_price", "subscription.0.link_android", "purchase.0.link"}

var watchNowCases = []catalogCase{
	{
		tool: toolMovieWatchNow, args: withCountry(movieRef(testMovieSlug), testCountry), route: "GET " + routeMovieSlug + "/watchnow/fr",
		fixture: "watchnow", object: true, want: watchNowWant, absent: watchNowAbsent,
	},
	{
		tool: toolShowWatchNow, args: withCountry(showShow(testShowSlug), testCountry), route: "GET /shows/breaking-bad/watchnow/fr",
		fixture: "watchnow", object: true, want: watchNowWant,
	},
	{
		tool: toolEpisodeWatchNow, args: withCountry(episodeArgs(1, 2), testCountry), route: "GET /shows/breaking-bad/seasons/1/episodes/2/watchnow/fr",
		fixture: "watchnow", object: true, want: watchNowWant,
	},
	{
		tool: toolWatchNowSources, args: map[string]any{}, route: routeWatchNowSrc, fixture: "watchnow_sources",
		want: map[string]any{
			"items.0.group": "fr", "items.0.source": "netflix", "items.0.name": "Netflix", pathFirstItemType: "subscription",
			"items.0.free": false, "items.0.link_count": 1200, "items.1.source": "arte", "items.1.free": true,
		},
		absent: []string{"items.0.images", "items.0.color", "items.1.type"},
	},
}

func TestWatchNowArguments(t *testing.T) {
	t.Parallel()

	movie := func(country string) map[string]any { return withCountry(movieRef(testMovieSlug), country) }

	cases := []argCase{
		{name: "country upper case", tool: toolMovieWatchNow, args: movie("FR"), wantErr: invalid(fieldCountry)},
		{name: "country three letters", tool: toolMovieWatchNow, args: movie("fra"), wantErr: invalid(fieldCountry)},
		{name: "country one letter", tool: toolMovieWatchNow, args: movie("f"), wantErr: invalid(fieldCountry)},
		{name: "country empty", tool: toolShowWatchNow, args: withCountry(showShow(testShowSlug), ""), wantErr: invalid(fieldCountry)},
		{name: "country traversal", tool: toolEpisodeWatchNow, args: withCountry(episodeArgs(1, 2), "../"), wantErr: invalid(fieldCountry)},
		{name: "country digits", tool: toolMovieWatchNow, args: movie("f1"), wantErr: invalid(fieldCountry)},
		{name: "movie id traversal", tool: toolMovieWatchNow, args: withCountry(movieRef(traversalID), testCountry), wantErr: invalid(fieldMovieID)},
		{name: "show id empty", tool: toolShowWatchNow, args: withCountry(showShow(""), testCountry), wantErr: invalid(fieldShowID)},
		{name: "zero episode", tool: toolEpisodeWatchNow, args: withCountry(episodeArgs(1, 0), testCountry), wantErr: invalid(fieldEpisode)},
		{
			name: "season 0 is the specials", tool: toolEpisodeWatchNow, args: withCountry(episodeArgs(0, 1), testCountry),
			route: "GET /shows/breaking-bad/seasons/0/episodes/1/watchnow/fr", body: emptyObject,
		},
	}

	runArgCases(t, cases)
}

// TestWatchNowOtherCountryIsEmpty requires a body that holds only another
// country to give empty lists, never that country's links.
func TestWatchNowOtherCountryIsEmpty(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET "+routeMovieSlug+"/watchnow/de", http.StatusOK, `{"fr":{"free":[{"source":"x","link":"https://example.com","uhd":false,"prices":{}}]}}`, nil)

	var out map[string]any

	h.ok(toolMovieWatchNow, withCountry(movieRef(testMovieSlug), "de"), &out)

	for _, group := range []string{"free", "subscription", "purchase", "cable", "cinema"} {
		if got, ok := out[group].([]any); !ok || len(got) != 0 {
			t.Errorf("%s = %v, want an empty list", group, out[group])
		}
	}
}

// upstreamFailure is a Trakt answer and the tool error it must become.
type upstreamFailure struct {
	name   string
	status int
	body   string
	want   string
}

// upstreamFailures lists the failures every read endpoint must map the same
// way; malformed is a 200 body the tool's decoder cannot read. No Trakt text
// may reach the tool error or the logs.
func upstreamFailures(malformed string) []upstreamFailure {
	body := `{"error_description":"` + injectionMarker + `"}`

	return []upstreamFailure{
		{"not found", http.StatusNotFound, body, msgNotFound},
		{"unauthorized", http.StatusUnauthorized, body, msgUpstreamAuth},
		{"rate limited", http.StatusTooManyRequests, body, msgRateLimitedNoDelay},
		{"server error", http.StatusInternalServerError, body, "Trakt returned an error (status 500)"},
		{"bad gateway", http.StatusBadGateway, ``, "Trakt returned an error (status 502)"},
		{"malformed body", http.StatusOK, malformed, msgInternal},
	}
}

func runUpstreamFailures(t *testing.T, tool string, args map[string]any, route, malformed string) {
	t.Helper()

	for _, f := range upstreamFailures(malformed) {
		t.Run(tool+"/"+f.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(route, f.status, f.body, nil)
			h.fail(tool, args, f.want)

			if strings.Contains(h.logs.String(), injectionMarker) {
				t.Error("Trakt's message reached the logs")
			}
		})
	}
}

// TestWatchNowTraktFailures covers the four where-to-watch endpoints,
// including the hand-written decoders of the country and sources bodies.
func TestWatchNowTraktFailures(t *testing.T) {
	t.Parallel()

	const (
		notAnObject = `[1]`
		notAList    = `{"not":"a list"}`
	)

	runUpstreamFailures(t, toolMovieWatchNow, withCountry(movieRef(testMovieSlug), testCountry), "GET "+routeMovieSlug+"/watchnow/fr", notAnObject)
	runUpstreamFailures(t, toolShowWatchNow, withCountry(showShow(testShowSlug), testCountry), "GET /shows/breaking-bad/watchnow/fr", notAnObject)
	runUpstreamFailures(t, toolEpisodeWatchNow, withCountry(episodeArgs(1, 2), testCountry), "GET /shows/breaking-bad/seasons/1/episodes/2/watchnow/fr", notAnObject)
	runUpstreamFailures(t, toolWatchNowSources, map[string]any{}, routeWatchNowSrc, notAList)
}

// TestWatchNowTextIsBounded requires prices, currency and offer type from
// Trakt to be cut.
func TestWatchNowTextIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("9", 5*maxPriceRunes)

	h := newHarness(t)
	h.trakt.respond("GET "+routeMovieSlug+"/watchnow/fr", http.StatusOK,
		`{"fr":{"purchase":[{"source":"s","link":"https://example.com","uhd":false,"currency":"`+long+`","prices":{"purchase":"`+long+`","rent":"`+long+`"}}]}}`, nil)

	var out WatchNow

	h.ok(toolMovieWatchNow, withCountry(movieRef(testMovieSlug), testCountry), &out)

	l := out.Purchase[0]
	if len([]rune(l.BuyPrice)) > maxPriceRunes || len([]rune(l.RentPrice)) > maxPriceRunes || len([]rune(l.Currency)) > maxCurrencyRunes {
		t.Errorf("link = %+v, want bounded text", l)
	}

	h = newHarness(t)
	h.trakt.respond(routeWatchNowSrc, http.StatusOK, `[{"fr":[{"source":"s","name":"n","type":"`+long+`"}]}]`, nil)

	var srcs Listing[WatchSource]

	h.ok(toolWatchNowSources, map[string]any{}, &srcs)

	if len([]rune(srcs.Items[0].Type)) > maxProviderTypeRunes {
		t.Errorf("type = %q, want it cut", srcs.Items[0].Type)
	}
}

// TestWatchNowSourcesCountry requires the country argument to keep only that
// country's providers, ignore the case of Trakt's group key, and be validated.
func TestWatchNowSourcesCountry(t *testing.T) {
	t.Parallel()

	const body = `[{"fr":[{"source":"netflix","name":"Netflix"}],"US":[{"source":"hulu","name":"Hulu"}]}]`

	cases := []struct {
		name, country string
		want          []string
	}{
		{"fr only", "fr", []string{"netflix"}},
		{"group case ignored", "us", []string{"hulu"}},
		{"unknown country is empty", "zz", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(routeWatchNowSrc, http.StatusOK, body, nil)

			var out Listing[WatchSource]

			h.ok(toolWatchNowSources, map[string]any{fieldCountry: tc.country}, &out)

			var got []string
			for _, it := range out.Items {
				got = append(got, it.Source)
			}

			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("sources = %v, want %v", got, tc.want)
			}
		})
	}

	runArgCases(t, []argCase{
		{name: "country three letters", tool: toolWatchNowSources, args: map[string]any{fieldCountry: "FRA"}, wantErr: invalid(fieldCountry)},
		{name: "country upper case", tool: toolWatchNowSources, args: map[string]any{fieldCountry: "FR"}, wantErr: invalid(fieldCountry)},
	})
}

// TestWatchNowSourcesAreCapped requires a list longer than maxReferenceItems
// to be cut.
func TestWatchNowSourcesAreCapped(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	b.WriteString(`[{"fr":[`)

	for i := range maxReferenceItems + 1 {
		if i > 0 {
			b.WriteString(",")
		}

		fmt.Fprintf(&b, `{"source":"s%d","name":"n"}`, i)
	}

	b.WriteString(`]}]`)

	h := newHarness(t)
	h.trakt.respond(routeWatchNowSrc, http.StatusOK, b.String(), nil)

	var out Listing[WatchSource]

	h.ok(toolWatchNowSources, map[string]any{}, &out)

	if len(out.Items) != maxReferenceItems {
		t.Errorf("items = %d, want the cap %d", len(out.Items), maxReferenceItems)
	}
}
