package tools

import (
	"net/http"
	"strings"
	"testing"
)

const (
	toolFetchUserLists     = "fetch_user_lists"
	toolFetchList          = "fetch_list"
	toolFetchListItems     = "fetch_list_items"
	toolFetchTrendingLists = "fetch_trending_lists"
	toolFetchPopularLists  = "fetch_popular_lists"

	// testOtherUser is a user slug other than the default "me".
	testOtherUser = "sean"
	// testListSlug is the slug of the list in the fixtures.
	testListSlug = "heist-night"

	routeUserListsMe = "GET /users/me/lists"
	routeListOfUser  = "GET /users/" + testOtherUser + "/lists/" + testListSlug + "/"
	routeListGlobal  = "GET /lists/" + testListSlug
	// Item routes are built from these prefixes plus type/sort/direction.
	pathUserListItems   = "/users/" + testOtherUser + "/lists/" + testListSlug + "/items/"
	pathGlobalListItems = "/lists/" + testListSlug + "/items/"
	routeTrendingLists  = "GET /lists/trending"
	routePopularLists   = "GET /lists/popular"

	// testListName is the name of the list in the fixtures.
	testListName = "Heist night"

	// Output paths of the list fixtures.
	pathFirstRank      = "items.0.rank"
	pathFirstImages    = "items.0.images"
	pathSecondShowSlug = "items.1.show.ids.slug"
	pathListPrivacy    = "items.0.privacy"
	pathListWrapName   = "items.0.list.name"
)

func init() { catalogCaseSets = append(catalogCaseSets, listCases) }

// listDetailWant is what the heist-night fixture must produce as a list.
var listDetailWant = map[string]any{
	"name": testListName, "description": "Films about robberies.", pathIDTrakt: 777, "ids.slug": testListSlug, "owner": testOtherUser,
	"item_count": 12, "likes": 34, "comment_count": 2, "privacy": "public", "type": "personal", "sort_by": "rank", "sort_how": "asc",
	"created_at": "2023-05-01T10:00:00.000Z", "allow_comments": true, "display_numbers": true,
}

var listItemsWant = map[string]any{
	pathFirstRank: 1, "items.0.list_item_id": 9001, pathFirstItemType: itemMovie, "items.0.notes": "Watch first", "items.0.listed_at": "2024-01-01T00:00:00.000Z",
	"items.0.movie.title": "Heat", pathSecondType: itemShow, pathSecondShowSlug: testShowSlug, "items.2.type": itemSeason,
	"items.2.show.ids.slug": testShowSlug, "items.3.type": itemEpisode, "items.3.episode.number": 2, "items.4.type": itemPerson,
	"items.4.person.name": "Michael Mann", "items.4.person.ids.slug": "michael-mann", "items.4.person.ids.imdb": "nm0000520",
}

var listCases = []catalogCase{
	{
		tool: toolFetchUserLists, args: map[string]any{}, route: routeUserListsMe, fixture: "user_lists",
		want: map[string]any{
			"items.0.name": testListName, "items.0.privacy": "public", pathFirstItemType: "personal", pathFirstIDSlug: testListSlug,
			"items.0.item_count": 12, "items.0.owner": testOtherUser, pathSecondName: "Empty", "items.1.privacy": "private",
		},
		absent: []string{pathFirstImages, "items.0.user", "items.1.description"},
	},
	{
		tool: toolFetchList, args: map[string]any{argListID: testListSlug, argUser: testOtherUser}, route: routeListOfUser, fixture: "list_summary", object: true,
		want: listDetailWant, absent: []string{keyImages, fieldUser},
	},
	{
		tool: toolFetchList, args: map[string]any{argListID: testListSlug}, route: routeListGlobal, fixture: "list_summary", object: true,
		want: listDetailWant, absent: []string{keyImages, fieldUser},
	},
	{
		tool: toolFetchListItems, args: map[string]any{argListID: testListSlug, argUser: testOtherUser},
		route: "GET " + pathUserListItems + "all/rank/asc", fixture: "list_items", want: listItemsWant,
		absent: []string{"items.1.notes", "items.0.show", "items.4.person.extra", "items.4.movie"},
	},
	{
		tool: toolFetchListItems, args: merge(map[string]any{argListID: testListSlug}, pagedArgs),
		route: "GET " + pathGlobalListItems + "all/rank/asc", fixture: "list_items", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want: map[string]any{
			pathFirstRank: 1, "items.0.movie.title": "Heat", pathSecondType: itemShow, pathPageCount: 3,
		},
	},
	{
		tool: toolFetchTrendingLists, args: pagedArgs, route: routeTrendingLists, fixture: "lists_trending", headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want: map[string]any{
			"items.0.like_count": 50, "items.0.comment_count": 3, pathListWrapName: testListName, "items.0.list.ids.slug": testListSlug,
			"items.0.list.type": "official", "items.1.list.name": "Empty", "items.1.list.owner": "ann", pathPageCount: 3,
		},
		absent: []string{"items.0.list.user", "items.1.list.description"},
	},
	{
		tool: toolFetchPopularLists, args: map[string]any{}, route: routePopularLists, fixture: "lists_trending",
		want: map[string]any{pathListWrapName: testListName, "items.0.like_count": 50},
	},
}

func TestListToolArguments(t *testing.T) {
	t.Parallel()

	listID := func(extra map[string]any) map[string]any {
		return merge(map[string]any{argListID: testListSlug}, extra)
	}
	items := func(extra map[string]any) map[string]any {
		return merge(map[string]any{argListID: testListSlug, argUser: testOtherUser}, extra)
	}

	cases := []argCase{
		{name: "user lists default user", tool: toolFetchUserLists, args: map[string]any{}, route: routeUserListsMe, body: emptyList},
		{name: "user lists empty user", tool: toolFetchUserLists, args: map[string]any{argUser: ""}, route: routeUserListsMe, body: emptyList},
		{name: "user lists other user", tool: toolFetchUserLists, args: map[string]any{argUser: testOtherUser}, route: "GET /users/sean/lists", body: emptyList},
		{name: "user lists traversal", tool: toolFetchUserLists, args: map[string]any{argUser: traversalID}, wantErr: invalid(argUser)},
		{name: "user lists imdb", tool: toolFetchUserLists, args: map[string]any{argUser: personIMDb}, wantErr: invalid(argUser)},
		{name: "list user traversal", tool: toolFetchList, args: listID(map[string]any{argUser: traversalID}), wantErr: invalid(argUser)},
		{name: "list id empty", tool: toolFetchList, args: map[string]any{argListID: ""}, wantErr: invalid(argListID)},
		{name: "list id traversal", tool: toolFetchList, args: map[string]any{argListID: traversalID}, wantErr: invalid(argListID)},
		{name: "list id query", tool: toolFetchList, args: map[string]any{argListID: "a?x=1"}, wantErr: invalid(argListID)},
		{name: "list id too long", tool: toolFetchList, args: map[string]any{argListID: strings.Repeat("a", maxIDLength+1)}, wantErr: invalid(argListID)},
		{name: "list id numeric", tool: toolFetchList, args: map[string]any{argListID: "4242"}, route: "GET /lists/4242", body: emptyObject},
		{name: "list id numeric of user", tool: toolFetchList, args: map[string]any{argListID: "4242", argUser: "me"}, route: "GET /users/me/lists/4242/", body: emptyObject},
		{name: "items id empty", tool: toolFetchListItems, args: map[string]any{argListID: ""}, wantErr: invalid(argListID)},
		{name: "items user traversal", tool: toolFetchListItems, args: listID(map[string]any{argUser: traversalID}), wantErr: invalid(argUser)},
		{name: "items type unknown", tool: toolFetchListItems, args: items(map[string]any{argType: "x"}), wantErr: invalid(fieldType)},
		{name: "items type singular", tool: toolFetchListItems, args: items(map[string]any{argType: itemMovie}), wantErr: invalid(fieldType)},
		{name: "items sort unknown", tool: toolFetchListItems, args: items(map[string]any{argSort: "x"}), wantErr: invalid(fieldSort)},
		{name: "items sort traversal", tool: toolFetchListItems, args: items(map[string]any{argSort: "rank/../x"}), wantErr: invalid(fieldSort)},
		{name: "items direction unknown", tool: toolFetchListItems, args: items(map[string]any{argSortHow: "up"}), wantErr: invalid(fieldSortHow)},
		{name: "items page zero", tool: toolFetchListItems, args: items(map[string]any{fieldPage: 0}), wantErr: invalid(fieldPage)},
		{name: "items limit over max", tool: toolFetchListItems, args: items(map[string]any{fieldLimit: maxLimit + 1}), wantErr: invalid(fieldLimit)},
		{
			name: "items sorted", tool: toolFetchListItems, args: items(map[string]any{argType: mediaPeople, argSort: smartListSortTitle, argSortHow: sortHowDesc}),
			route: "GET " + pathUserListItems + "people/title/desc", body: emptyList, wantPath: pathUserListItems + "people/title/desc",
		},
		{
			name: "items global sorted", tool: toolFetchListItems, args: listID(map[string]any{argType: mediaEpisodes, argSort: "added"}),
			route: "GET " + pathGlobalListItems + "episodes/added/asc", body: emptyList, wantPath: pathGlobalListItems + "episodes/added/asc",
		},
		{name: "popular type official", tool: toolFetchPopularLists, args: map[string]any{argType: listTypeOfficial}, route: routePopularLists + "/official", body: emptyList},
		{name: "popular type personal", tool: toolFetchPopularLists, args: map[string]any{argType: listTypePersonal}, route: routePopularLists + "/personal", body: emptyList},
		{name: "popular type unknown", tool: toolFetchPopularLists, args: map[string]any{argType: "x"}, wantErr: invalid(fieldType)},
		{name: "popular type all is not a trakt type", tool: toolFetchPopularLists, args: map[string]any{argType: mediaAll}, wantErr: invalid(fieldType)},
		{name: "trending type official", tool: toolFetchTrendingLists, args: map[string]any{argType: listTypeOfficial}, route: routeTrendingLists + "/official", body: emptyList},
		{name: "trending type traversal", tool: toolFetchTrendingLists, args: map[string]any{argType: traversalID}, wantErr: invalid(fieldType)},
		{name: "trending page zero", tool: toolFetchTrendingLists, args: map[string]any{fieldPage: 0}, wantErr: invalid(fieldPage)},
		{name: "trending limit over max", tool: toolFetchTrendingLists, args: map[string]any{fieldLimit: maxLimit + 1}, wantErr: invalid(fieldLimit)},
	}

	runArgCases(t, cases)
}

// TestListTraktFailures covers every endpoint of the group: each Trakt failure
// maps to its own message and never echoes Trakt's text.
func TestListTraktFailures(t *testing.T) {
	t.Parallel()

	const (
		notAList   = `{"not":"a list"}`
		notAnArray = `[1]`
	)

	runUpstreamFailures(t, toolFetchUserLists, map[string]any{}, routeUserListsMe, notAList)
	runUpstreamFailures(t, toolFetchList, map[string]any{argListID: testListSlug, argUser: testOtherUser}, routeListOfUser, notAnArray)
	runUpstreamFailures(t, toolFetchList, map[string]any{argListID: testListSlug}, routeListGlobal, notAnArray)
	runUpstreamFailures(t, toolFetchListItems, map[string]any{argListID: testListSlug, argUser: testOtherUser}, "GET "+pathUserListItems+"all/rank/asc", notAList)
	runUpstreamFailures(t, toolFetchListItems, map[string]any{argListID: testListSlug}, "GET "+pathGlobalListItems+"all/rank/asc", notAList)
	runUpstreamFailures(t, toolFetchTrendingLists, map[string]any{}, routeTrendingLists, notAList)
	runUpstreamFailures(t, toolFetchPopularLists, map[string]any{}, routePopularLists, notAList)
}

// TestListTextIsBounded requires list names, descriptions, item notes and
// person names from Trakt to be cut.
func TestListTextIsBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 5*maxListOverviewRunes)
	listJSON := `{"name":"` + long + `","description":"` + long + `","privacy":"` + long + `","type":"` + long + `","sort_by":"` + long + `","sort_how":"` + long +
		`","ids":{"trakt":1,"slug":"s"},"user":{"username":"` + long + `"}}`

	checkList := func(t *testing.T, l ListDetail) {
		t.Helper()

		if len([]rune(l.Name)) > maxTitleRunes || len([]rune(l.Description)) > maxListOverviewRunes || len([]rune(l.Owner)) > maxTitleRunes ||
			len([]rune(l.Privacy)) > maxListFieldRunes || len([]rune(l.Type)) > maxListFieldRunes || len([]rune(l.SortBy)) > maxListFieldRunes || len([]rune(l.SortHow)) > maxListFieldRunes {
			t.Errorf("list = %+v, want bounded text", l)
		}
	}

	t.Run("user lists", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(routeUserListsMe, http.StatusOK, `[`+listJSON+`]`, nil)

		var out Listing[ListDetail]

		h.ok(toolFetchUserLists, map[string]any{}, &out)
		checkList(t, out.Items[0])
	})

	t.Run("list summary", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(routeListGlobal, http.StatusOK, listJSON, nil)

		var out ListDetail

		h.ok(toolFetchList, map[string]any{argListID: testListSlug}, &out)
		checkList(t, out)
	})

	t.Run("trending", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond(routeTrendingLists, http.StatusOK, `[{"like_count":1,"comment_count":1,"list":`+listJSON+`}]`, nil)

		var out Listing[TrendingList]

		h.ok(toolFetchTrendingLists, map[string]any{}, &out)
		checkList(t, out.Items[0].List)
	})

	t.Run("items", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("GET "+pathGlobalListItems+"all/rank/asc", http.StatusOK,
			`[{"rank":1,"id":1,"listed_at":"2024-01-01T00:00:00.000Z","notes":"`+long+`","type":"`+long+`","person":{"name":"`+long+`","ids":{"trakt":1,"slug":"p"},"biography":"`+long+`"}}]`, nil)

		var out Listing[ListItem]

		h.ok(toolFetchListItems, map[string]any{argListID: testListSlug}, &out)

		i := out.Items[0]
		if len([]rune(i.Notes)) > maxNotesRunes || len([]rune(i.Type)) > maxListFieldRunes || i.Person == nil ||
			len([]rune(i.Person.Name)) > maxTitleRunes || len([]rune(i.Person.Biography)) > maxListOverviewRunes {
			t.Errorf("item = %+v, want bounded text", i)
		}
	})
}
