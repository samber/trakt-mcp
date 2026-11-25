package tools

import (
	"strconv"
	"testing"
)

func init() { catalogCaseSets = append(catalogCaseSets, episodeCases) }

// episodeBaseFor is the Trakt path of episode n of season 1 of the test show.
func episodeBaseFor(n int) string { return seasonBase + "/episodes/" + strconv.Itoa(n) }

func episodeRoute(suffix string) string { return "GET " + episodeBaseFor(2) + suffix }

var episodeCases = []catalogCase{
	{
		tool: toolGetSummary, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute(""), fixture: "episode_summary", object: true,
		wantQuery: map[string]string{queryExtended: extendedFull},
		want: inItem(map[string]any{
			"season": 1, "number": 2, keyTitle: "Pilot", keyOverview: "Walter learns something.", keyRating: 8.6, keyVotes: 300, "runtime": 58,
			"first_aired": testFirstAired, "episode_type": "series_premiere", "ids.trakt": 62118, "ids.imdb": "tt0959621",
		}),
		absent: inItemPaths([]string{keyImages, "available_translations"}),
	},
	{tool: toolGetRatings, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute("/ratings"), fixture: "episode_ratings", object: true, want: sharedRatingsWant},
	{tool: toolGetStats, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute("/stats"), fixture: "episode_stats", object: true, want: sharedStatsWant, absent: []string{"extra"}},
	{
		tool: toolListPeople, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute("/people"), fixture: "episode_people", object: true,
		want: sharedPeopleWant, absent: []string{"guest_stars"},
	},
	{tool: toolListVideos, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute("/videos"), fixture: "episode_videos", want: sharedVideosWant},
	{tool: toolListWatching, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute("/watching"), fixture: "episode_watching", want: sharedWatchingWant, absent: []string{"users.1"}},
	{
		tool: toolListTranslations, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argLanguage: "fr"}), route: episodeRoute("/translations"), fixture: "episode_translations",
		wantQuery: map[string]string{argLanguage: "fr"}, want: sharedTranslationsWant, absent: []string{"items.0.tagline", "items.1.title"},
	},
	{
		tool: toolListItemLists, args: merge(episodeItem(testShowSlug, 1, 2), pagedArgs), route: episodeRoute(testSeasonPathTail), fixture: "episode_lists", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want:      merge(sharedListsWant, map[string]any{pathPageCount: 3}),
	},
}

func TestEpisodeToolArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{
		{name: "episode -1", tool: toolGetSummary, args: episodeItem(testShowSlug, 1, -1), wantErr: invalid(fieldEpisode)},
		{name: "episode 0", tool: toolGetSummary, args: episodeItem(testShowSlug, 1, 0), wantErr: invalid(fieldEpisode)},
		{name: "episode 1", tool: toolGetSummary, args: episodeItem(testShowSlug, 1, 1), route: "GET " + episodeBaseFor(1), body: emptyObject},
		{name: "episode 9999", tool: toolGetStats, args: episodeItem(testShowSlug, 1, 9999), route: "GET " + episodeBaseFor(9999) + "/stats", body: emptyObject},
		{name: "episode 10000", tool: toolGetStats, args: episodeItem(testShowSlug, 1, 10000), wantErr: invalid(fieldEpisode)},
		{name: "episode 0 lists", tool: toolListItemLists, args: episodeItem(testShowSlug, 1, 0), wantErr: invalid(fieldEpisode)},
		{name: "season -1", tool: toolGetSummary, args: episodeItem(testShowSlug, -1, 2), wantErr: invalid(fieldSeason)},
		{name: "season 0 is the specials", tool: toolListVideos, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{fieldSeason: 0}), route: "GET /shows/breaking-bad/seasons/0/episodes/2/videos", body: emptyList},
		{name: "season 999", tool: toolGetRatings, args: episodeItem(testShowSlug, 999, 2), route: "GET /shows/breaking-bad/seasons/999/episodes/2/ratings", body: emptyObject},
		{name: "season 1000", tool: toolGetRatings, args: episodeItem(testShowSlug, 1000, 2), wantErr: invalid(fieldSeason)},
		{name: "show id imdb", tool: toolListPeople, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{fieldID: testShowIMDb}), route: "GET /shows/tt0903747/seasons/1/episodes/2/people", body: emptyObject},
		{name: "show id traversal", tool: toolGetSummary, args: episodeItem(traversalID, 1, 2), wantErr: invalid(fieldID)},
		{name: "show id person imdb", tool: toolListWatching, args: episodeItem(personIMDb, 1, 2), wantErr: invalid(fieldID)},
		{name: "language fr", tool: toolListTranslations, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argLanguage: "fr"}), route: episodeRoute("/translations"), body: emptyList, wantQuery: map[string]string{argLanguage: "fr"}},
		{name: "language omitted means all", tool: toolListTranslations, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute("/translations"), body: emptyList, noQuery: []string{argLanguage}},
		{name: "language upper case", tool: toolListTranslations, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argLanguage: "Fr"}), wantErr: invalid(fieldLanguage)},
		{name: "language three letters", tool: toolListTranslations, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argLanguage: "fra"}), wantErr: invalid(fieldLanguage)},
		{name: "lists defaults", tool: toolListItemLists, args: episodeItem(testShowSlug, 1, 2), route: episodeRoute(testSeasonPathTail), body: emptyList},
		{name: "lists official by likes", tool: toolListItemLists, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argListType: listTypeOfficial, argSort: listSortLikes}), route: episodeRoute("/lists/official/likes"), body: emptyList},
		{name: "lists watchlist by updated", tool: toolListItemLists, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argListType: listTypeWatchlist, argSort: listSortUpdated}), route: episodeRoute("/lists/watchlist/updated"), body: emptyList},
		{name: "lists type unknown", tool: toolListItemLists, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argListType: "watchlists"}), wantErr: invalid(fieldListType)},
		{name: "lists sort unknown", tool: toolListItemLists, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{argSort: "newest"}), wantErr: invalid(fieldSort)},
		{name: "lists page 0", tool: toolListItemLists, args: merge(episodeItem(testShowSlug, 1, 2), map[string]any{fieldPage: 0}), wantErr: invalid(fieldPage)},
	}

	runArgCases(t, cases)
}
