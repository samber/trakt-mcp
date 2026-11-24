package tools

import "testing"

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
		{
			name: "other country in body", tool: toolMovieWatchNow, args: movie("de"), route: "GET " + routeMovieSlug + "/watchnow/de",
			body: `{"fr":{"free":[{"source":"x","link":"https://example.com","uhd":false,"prices":{}}]}}`,
		},
	}

	runArgCases(t, cases)
}
