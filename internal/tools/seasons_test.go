package tools

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

const (
	toolSeasonEpisodes     = "fetch_season_episodes"
	toolSeasonRatings      = "fetch_season_ratings"
	toolSeasonStats        = "fetch_season_stats"
	toolSeasonPeople       = "fetch_season_people"
	toolSeasonVideos       = "fetch_season_videos"
	toolSeasonWatching     = "fetch_season_watching"
	toolSeasonTranslations = "fetch_season_translations"
	toolSeasonLists        = "fetch_season_lists"

	argLanguage = "language"
	argListType = "list_type"
	argSort     = "sort"

	// seasonBase is the Trakt path of season 1 of testShowSlug.
	seasonBase = "/shows/breaking-bad/seasons/1"
	// peopleFirstName is the output path of the first cast member's name.
	pathFirstCastName  = "cast.0.name"
	pathFirstItemTitle = "items.0.markdown"
	pathFirstItemName  = "items.0.name"
	testSeasonPathTail = "/lists/all/popular"
)

func init() { catalogCaseSets = append(catalogCaseSets, seasonCases) }

// seasonRef returns the arguments naming season 1 of the test show.
func seasonRef(extra map[string]any) map[string]any {
	return merge(map[string]any{fieldShowID: testShowSlug, fieldSeason: 1}, extra)
}

func seasonRoute(suffix string) string { return "GET " + seasonBase + suffix }

// sharedPeopleWant is what the people fixture must produce, for seasons and
// episodes alike.
var sharedPeopleWant = map[string]any{
	pathFirstCastName: "Bryan Cranston", "cast.0.character": "Walter White", "cast.0.ids.slug": "bryan-cranston",
	"cast.1.name": "Aaron Paul", "crew.0.department": "directing", "crew.0.members.0.job": "Director", "crew.1.department": "writing",
}

// sharedVideosWant is what the videos fixture must produce.
var sharedVideosWant = map[string]any{
	pathFirstItemTitle: `[Official \[Trailer\]](https://www.youtube.com/watch?v=abc)`, "items.0.site": "youtube", "items.0.type": "trailer",
	"items.1.markdown": `[Clip (1)](https://vimeo.com/a%28b%29)`,
}

// sharedRatingsWant is what the ratings fixture must produce.
var sharedRatingsWant = map[string]any{
	keyRating: 9.3, keyVotes: 1000, "distribution": []any{10.0, 20.0, 30.0, 40.0, 50.0, 60.0, 70.0, 80.0, 90.0, 100.0},
}

// sharedStatsWant is what the stats fixture must produce.
var sharedStatsWant = map[string]any{"watchers": 100, "plays": 400, "collectors": 50, "comments": 7, "lists": 8, keyVotes: 90}

// sharedWatchingWant is what the watching fixture must produce: the private
// and deleted users are counted, not listed.
var sharedWatchingWant = map[string]any{"count": 3, "users.0.username": "sean", "users.0.slug": "sean"}

// sharedTranslationsWant is what the translations fixture must produce.
var sharedTranslationsWant = map[string]any{
	"items.0.language": "fr", "items.0.country": "fr", "items.0.title": "Saison 1", "items.0.overview": "La premiere saison.", "items.1.language": "de",
}

// sharedListsWant is what the lists fixture must produce.
var sharedListsWant = map[string]any{
	pathFirstItemName: "Best seasons", "items.0.description": "My favourites.", "items.0.ids.trakt": 777, "items.0.ids.slug": "best-seasons",
	"items.0.owner": "sean", "items.0.item_count": 12, "items.0.likes": 34, "items.0.comment_count": 2, "items.1.name": "Empty",
}

var seasonCases = []catalogCase{
	{
		tool: toolGetSummary, args: seasonItem(testShowSlug, 1), route: seasonRoute("/info"), fixture: "season_info", object: true,
		wantQuery: map[string]string{queryExtended: extendedFull},
		want: inItem(map[string]any{
			"number": 1, keyTitle: "Season 1", keyOverview: "The first season.", keyRating: 8.9, keyVotes: 500, "episode_count": 7,
			"aired_episodes": 7, "first_aired": testFirstAired, "ids.trakt": 3961, "ids.tvdb": 30272, "ids.tmdb": 3572,
		}),
		absent: inItemPaths([]string{keyImages}),
	},
	{
		tool: toolSeasonEpisodes, args: seasonRef(nil), route: seasonRoute(""), fixture: "season_episodes",
		wantQuery: map[string]string{queryExtended: extendedFull},
		want: map[string]any{
			"items.0.season": 1, "items.0.number": 1, "items.0.title": "Pilot", "items.0.overview": "Walter learns something.", "items.0.rating": 8.6,
			"items.0.votes": 300, "items.0.runtime": 58, "items.0.first_aired": testFirstAired, "items.0.episode_type": "series_premiere",
			"items.0.ids.trakt": 62118, "items.0.ids.imdb": "tt0959621", "items.0.ids.tmdb": 62085, "items.0.ids.tvdb": 349232,
			"items.1.number": 2, pathSecondTitle: "Cat's in the Bag...",
		},
		absent: []string{"items.0.images", "items.1.overview", "items.1.runtime"},
	},
	{tool: toolSeasonRatings, args: seasonRef(nil), route: seasonRoute("/ratings"), fixture: "season_ratings", object: true, want: sharedRatingsWant},
	{tool: toolSeasonStats, args: seasonRef(nil), route: seasonRoute("/stats"), fixture: "season_stats", object: true, want: sharedStatsWant, absent: []string{"extra"}},
	{
		tool: toolSeasonPeople, args: seasonRef(nil), route: seasonRoute("/people"), fixture: "season_people", object: true,
		want: sharedPeopleWant, absent: []string{"guest_stars"},
	},
	{tool: toolSeasonVideos, args: seasonRef(nil), route: seasonRoute("/videos"), fixture: "season_videos", want: sharedVideosWant},
	{tool: toolSeasonWatching, args: seasonRef(nil), route: seasonRoute("/watching"), fixture: "season_watching", want: sharedWatchingWant, absent: []string{"users.1"}},
	{
		tool: toolSeasonTranslations, args: seasonRef(map[string]any{argLanguage: "fr"}), route: seasonRoute("/translations"), fixture: "season_translations",
		wantQuery: map[string]string{argLanguage: "fr"}, want: sharedTranslationsWant, absent: []string{"items.0.tagline", pathSecondTitle},
	},
	{
		tool: toolSeasonLists, args: merge(seasonRef(nil), pagedArgs), route: seasonRoute(testSeasonPathTail), fixture: "season_lists", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want:      merge(sharedListsWant, map[string]any{pathPageCount: 3}),
	},
}

func TestSeasonToolArguments(t *testing.T) {
	t.Parallel()

	infoRoute := func(n int) string { return seasonBaseFor(n) + "/info" }

	cases := []argCase{
		{name: "season -1", tool: toolGetSummary, args: seasonItem(testShowSlug, -1), wantErr: invalid(fieldSeason)},
		{name: "season 0 is the specials", tool: toolGetSummary, args: seasonItem(testShowSlug, 0), route: "GET " + infoRoute(0), body: emptyObject},
		{name: "season 1", tool: toolGetSummary, args: seasonItem(testShowSlug, 1), route: "GET " + infoRoute(1), body: emptyObject},
		{name: "season 999", tool: toolSeasonStats, args: merge(seasonRef(nil), map[string]any{fieldSeason: 999}), route: "GET " + seasonBaseFor(999) + "/stats", body: emptyObject},
		{name: "season 1000", tool: toolSeasonStats, args: merge(seasonRef(nil), map[string]any{fieldSeason: 1000}), wantErr: invalid(fieldSeason)},
		{name: "season 1000 episodes", tool: toolSeasonEpisodes, args: merge(seasonRef(nil), map[string]any{fieldSeason: 1000}), wantErr: invalid(fieldSeason)},
		{name: "show id imdb", tool: toolSeasonVideos, args: merge(seasonRef(nil), map[string]any{fieldShowID: testShowIMDb}), route: "GET /shows/tt0903747/seasons/1/videos", body: emptyList},
		{name: "show id numeric", tool: toolSeasonPeople, args: merge(seasonRef(nil), map[string]any{fieldShowID: "1388"}), route: "GET /shows/1388/seasons/1/people", body: emptyObject},
		{name: "show id traversal", tool: toolGetSummary, args: seasonItem(traversalID, 1), wantErr: invalid(fieldID)},
		{name: "show id person imdb", tool: toolSeasonRatings, args: merge(seasonRef(nil), map[string]any{fieldShowID: personIMDb}), wantErr: invalid(fieldShowID)},
		{name: "show id empty", tool: toolSeasonWatching, args: merge(seasonRef(nil), map[string]any{fieldShowID: ""}), wantErr: invalid(fieldShowID)},
		{name: "language fr", tool: toolSeasonTranslations, args: seasonRef(map[string]any{argLanguage: "fr"}), route: seasonRoute("/translations"), body: emptyList, wantQuery: map[string]string{argLanguage: "fr"}},
		{name: "language omitted means all", tool: toolSeasonTranslations, args: seasonRef(nil), route: seasonRoute("/translations"), body: emptyList, noQuery: []string{argLanguage}},
		{name: "language upper case", tool: toolSeasonTranslations, args: seasonRef(map[string]any{argLanguage: "FR"}), wantErr: invalid(fieldLanguage)},
		{name: "language three letters", tool: toolSeasonTranslations, args: seasonRef(map[string]any{argLanguage: "fra"}), wantErr: invalid(fieldLanguage)},
		{name: "language one letter", tool: toolSeasonTranslations, args: seasonRef(map[string]any{argLanguage: "f"}), wantErr: invalid(fieldLanguage)},
		{name: "language injection", tool: toolSeasonTranslations, args: seasonRef(map[string]any{argLanguage: "fr&x=1"}), wantErr: invalid(fieldLanguage)},
		{name: "lists defaults", tool: toolSeasonLists, args: seasonRef(nil), route: seasonRoute(testSeasonPathTail), body: emptyList},
		{name: "lists official by likes", tool: toolSeasonLists, args: seasonRef(map[string]any{argListType: listTypeOfficial, argSort: listSortLikes}), route: seasonRoute("/lists/official/likes"), body: emptyList},
		{name: "lists watchlist by updated", tool: toolSeasonLists, args: seasonRef(map[string]any{argListType: listTypeWatchlist, argSort: listSortUpdated}), route: seasonRoute("/lists/watchlist/updated"), body: emptyList},
		{name: "lists favorites by items", tool: toolSeasonLists, args: seasonRef(map[string]any{argListType: listTypeFavorites, argSort: listSortItems}), route: seasonRoute("/lists/favorites/items"), body: emptyList},
		{name: "lists personal by added", tool: toolSeasonLists, args: seasonRef(map[string]any{argListType: listTypePersonal, argSort: listSortAdded}), route: seasonRoute("/lists/personal/added"), body: emptyList},
		{name: "lists all by comments", tool: toolSeasonLists, args: seasonRef(map[string]any{argListType: listTypeAll, argSort: listSortComments}), route: seasonRoute("/lists/all/comments"), body: emptyList},
		{name: "lists type unknown", tool: toolSeasonLists, args: seasonRef(map[string]any{argListType: "watchlists"}), wantErr: invalid(fieldListType)},
		{name: "lists type traversal", tool: toolSeasonLists, args: seasonRef(map[string]any{argListType: traversalID}), wantErr: invalid(fieldListType)},
		{name: "lists sort unknown", tool: toolSeasonLists, args: seasonRef(map[string]any{argSort: "newest"}), wantErr: invalid(fieldSort)},
		{name: "lists sort upper case", tool: toolSeasonLists, args: seasonRef(map[string]any{argSort: "Popular"}), wantErr: invalid(fieldSort)},
		{name: "lists limit 101", tool: toolSeasonLists, args: seasonRef(map[string]any{fieldLimit: 101}), wantErr: invalid(fieldLimit)},
	}

	runArgCases(t, cases)
}

// seasonBaseFor is the Trakt path of season n of the test show.
func seasonBaseFor(n int) string { return "/shows/breaking-bad/seasons/" + strconv.Itoa(n) }

func TestSeasonWatchingIsBounded(t *testing.T) {
	t.Parallel()

	users := make([]map[string]any, 0, maxWatchingUsers+10)
	for i := range maxWatchingUsers + 10 {
		users = append(users, map[string]any{"username": "u" + strconv.Itoa(i), "ids": map[string]any{keySlug: "u", keyTrakt: i}})
	}

	body, err := json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}

	h := newHarness(t)
	h.trakt.respond(seasonRoute("/watching"), http.StatusOK, string(body), nil)

	var out Watching

	h.ok(toolSeasonWatching, seasonRef(nil), &out)

	if out.Count != maxWatchingUsers+10 || len(out.Users) != maxWatchingUsers {
		t.Errorf("count %d, users %d, want %d and %d", out.Count, len(out.Users), maxWatchingUsers+10, maxWatchingUsers)
	}
}
