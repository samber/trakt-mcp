package tools

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const (
	toolFetchSmartLists     = "list_smart_lists"
	toolFetchSmartListItems = "list_smart_list_items"

	argUser     = "user"
	argListID   = "list_id"
	argSortBy   = "sort_by"
	testListID  = "crime-thrillers"
	testListNum = "4242"

	routeSmartListsMe   = "GET /users/me/smart-lists"
	routeSmartListItems = "/smart-lists/" + testListID + "/items/"
	routeSmartListsOf   = "GET /users/"

	pathFirstListName   = "items.0.name"
	pathFirstListFilter = "items.0.filters.genres"
)

func init() {
	accountCaseSets = append(accountCaseSets, smartListCases)
	accountAccess[toolFetchSmartLists] = Read
	accountAccess[toolFetchSmartListItems] = Read
}

var smartListCases = []accountCase{
	{
		tool: toolFetchSmartLists, args: map[string]any{}, route: routeSmartListsMe, fixture: "smart_lists",
		want: map[string]any{
			pathFirstListName: "Crime thrillers", pathFirstIDTrakt: 4242, pathFirstIDSlug: testListID,
			"items.0.privacy": "private", "items.0.media_type": mediaMovies, "items.0.source": mediaMovies,
			"items.0.created_at": "2024-02-01T10:00:00.000Z", "items.0.filters.genres_operator": "and",
			"items.1.name": "Comfort shows",
		},
		// A null filter value is dropped, images are not returned.
		absent: []string{"items.0.filters.keywords", "items.0.images"},
	},
	{
		tool: toolFetchSmartListItems, args: map[string]any{argListID: testListID},
		route: "GET " + routeSmartListItems + "all/rank/asc", fixture: "smart_list_items",
		want: map[string]any{
			"items.0.rank": 1, pathFirstItemType: itemMovie, pathFirstMovieTitle: testMovieTitle,
			pathSecondType: itemShow, "items.1.show.ids.slug": testShowSlug,
		},
		absent: []string{pathFirstShow, "items.1.movie"},
	},
}

// TestSmartListItemsSortValidation checks type, sort_by and sort_how against
// their enums, the default direction per sort, and that no value reaches the
// Trakt path unvalidated.
func TestSmartListItemsSortValidation(t *testing.T) {
	t.Parallel()

	cases := []argCase{}
	route := func(path string) string { return "GET " + path }

	for _, sortBy := range smartListSorts {
		how := sortHowDesc
		if sortBy == watchlistSortRank || sortBy == "title" {
			how = sortHowAsc
		}

		path := routeSmartListItems + "all/" + sortBy + "/" + how
		cases = append(cases, argCase{
			name: "sort " + sortBy, tool: toolFetchSmartListItems, args: map[string]any{argListID: testListID, argSortBy: sortBy},
			route: route(path), body: emptyList, wantPath: path,
		})
	}

	for _, typ := range []string{mediaAll, mediaMovies, mediaShows} {
		path := routeSmartListItems + typ + "/title/desc"
		cases = append(cases, argCase{
			name: "type " + typ, tool: toolFetchSmartListItems,
			args:  map[string]any{argListID: testListID, argType: typ, argSortBy: "title", argSortHow: sortHowDesc},
			route: route(path), body: emptyList, wantPath: path,
		})
	}

	listID := func(extra map[string]any) map[string]any { return merge(map[string]any{argListID: testListID}, extra) }

	cases = append(cases,
		argCase{
			name: "numeric list id", tool: toolFetchSmartListItems, args: map[string]any{argListID: testListNum},
			route: route("/smart-lists/" + testListNum + "/items/all/rank/asc"), body: emptyList,
		},
		argCase{name: "bad sort", tool: toolFetchSmartListItems, args: listID(map[string]any{argSortBy: "added_at"}), wantErr: invalid(argSortBy)},
		argCase{name: "sort upper case", tool: toolFetchSmartListItems, args: listID(map[string]any{argSortBy: "Title"}), wantErr: invalid(argSortBy)},
		argCase{name: "sort traversal", tool: toolFetchSmartListItems, args: listID(map[string]any{argSortBy: "rank/../x"}), wantErr: invalid(argSortBy)},
		argCase{name: "smart bad type", tool: toolFetchSmartListItems, args: listID(map[string]any{argType: typeUpperCase}), wantErr: invalid(fieldType)},
		argCase{name: "type episodes", tool: toolFetchSmartListItems, args: listID(map[string]any{argType: mediaEpisodes}), wantErr: invalid(fieldType)},
		argCase{name: "bad direction", tool: toolFetchSmartListItems, args: listID(map[string]any{argSortHow: "up"}), wantErr: invalid(fieldSortHow)},
		argCase{name: "upper direction", tool: toolFetchSmartListItems, args: listID(map[string]any{argSortHow: "DESC"}), wantErr: invalid(fieldSortHow)},
		argCase{name: "page zero", tool: toolFetchSmartListItems, args: listID(map[string]any{fieldPage: 0}), wantErr: invalid(fieldPage)},
		argCase{name: "page negative", tool: toolFetchSmartListItems, args: listID(map[string]any{fieldPage: -1}), wantErr: invalid(fieldPage)},
		argCase{
			name: "limit zero is default", tool: toolFetchSmartListItems, args: listID(map[string]any{fieldLimit: 0}),
			route: route(routeSmartListItems + "all/rank/asc"), body: emptyList, wantQuery: map[string]string{fieldLimit: "10"},
		},
		argCase{name: "limit over max", tool: toolFetchSmartListItems, args: listID(map[string]any{fieldLimit: maxLimit + 1}), wantErr: invalid(fieldLimit)},
		argCase{name: "limit negative", tool: toolFetchSmartListItems, args: listID(map[string]any{fieldLimit: -1}), wantErr: invalid(fieldLimit)},
		argCase{
			name: "smart paged", tool: toolFetchSmartListItems, args: listID(pagedArgs),
			route: route(routeSmartListItems + "all/rank/asc"), body: emptyList,
			wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2", queryExtended: extendedFull},
		},
	)

	runArgCases(t, cases)
}

// TestSmartListIDsRejectHostileValues checks that list_id and user are
// validated before any Trakt request.
func TestSmartListIDsRejectHostileValues(t *testing.T) {
	t.Parallel()

	// An IMDb id is no smart list id or user slug either.
	hostile := []string{traversalID, "a/b", "a?x=1", "a#b", "A-B", "a b", "a%2fb", "a.b", "a\n", strings.Repeat("a", maxIDLength+1), "tt0903747", personIMDb}
	cases := []argCase{{name: "list_id empty", tool: toolFetchSmartListItems, args: map[string]any{argListID: ""}, wantErr: invalid(argListID)}}

	for _, id := range hostile {
		cases = append(cases,
			argCase{name: "list_id " + id, tool: toolFetchSmartListItems, args: map[string]any{argListID: id}, wantErr: invalid(argListID)},
			argCase{name: "user " + id, tool: toolFetchSmartLists, args: map[string]any{argUser: id}, wantErr: invalid(argUser)},
		)
	}

	// An empty or omitted user means the default, "me".
	cases = append(cases,
		argCase{name: "user omitted", tool: toolFetchSmartLists, args: map[string]any{}, route: routeSmartListsMe, body: emptyList},
		argCase{name: "user empty", tool: toolFetchSmartLists, args: map[string]any{argUser: ""}, route: routeSmartListsMe, body: emptyList},
	)

	runArgCases(t, cases)
}

func TestSmartListsOfAnotherUser(t *testing.T) {
	t.Parallel()

	runArgCases(t, []argCase{{
		name: "other user", tool: toolFetchSmartLists, args: map[string]any{argUser: "some-user"},
		route: routeSmartListsOf + "some-user/smart-lists", body: emptyList, wantPath: "/users/some-user/smart-lists",
		wantQuery: map[string]string{},
	}})
}

// TestSmartListsTraktFailures maps each Trakt failure by Kind for both tools.
func TestSmartListsTraktFailures(t *testing.T) {
	t.Parallel()

	tools := []struct {
		tool  string
		args  map[string]any
		route string
	}{
		{toolFetchSmartLists, map[string]any{}, routeSmartListsMe},
		{toolFetchSmartListItems, map[string]any{argListID: testListID}, "GET " + routeSmartListItems + "all/rank/asc"},
	}

	failures := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"not found", http.StatusNotFound, `{"error_description":"` + injectionMarker + `"}`, msgNotFound},
		{"unauthorized", http.StatusUnauthorized, `{"error_description":"` + injectionMarker + `"}`, msgUpstreamAuth},
		{"private list of another user", http.StatusForbidden, `{"error_description":"` + injectionMarker + `"}`, msgSmartListForbidden},
		{"server error", http.StatusInternalServerError, `{"error_description":"` + injectionMarker + `"}`, "Trakt returned an error (status 500)"},
		{"bad gateway", http.StatusBadGateway, ``, "Trakt returned an error (status 502)"},
		{"malformed body", http.StatusOK, `{"not": "a list"`, msgInternal},
		{"object instead of list", http.StatusOK, `{"not": "a list"}`, msgInternal},
	}

	for _, tl := range tools {
		for _, f := range failures {
			t.Run(tl.tool+"/"+f.name, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t)
				h.trakt.respond(tl.route, f.status, f.body, nil)
				h.fail(tl.tool, tl.args, f.want)

				if strings.Contains(h.logs.String(), injectionMarker) {
					t.Error("Trakt's message reached the logs")
				}
			})
		}
	}
}

// TestSmartListItemsPaginatedPartial checks a call without page collects
// across pages and stops at the last one, and that a page past the end is an
// empty list with its metadata.
func TestSmartListItemsPaginatedPartial(t *testing.T) {
	t.Parallel()

	headers := map[string]string{"X-Pagination-Page": "1", "X-Pagination-Limit": "2", "X-Pagination-Page-Count": "3", "X-Pagination-Item-Count": "5"}

	t.Run("one page requested", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("GET "+routeSmartListItems+"all/rank/asc", http.StatusOK, fixture(t, "smart_list_items"), headers)

		var out Listing[SmartListItem]
		h.ok(toolFetchSmartListItems, map[string]any{argListID: testListID, fieldPage: 1, fieldLimit: 2}, &out)

		if len(out.Items) != 2 || out.Page == nil || out.Page.PageCount != 3 || out.Page.ItemCount != 5 {
			t.Errorf("out = %+v, want 2 items on page 1 of 3 (5 in all)", out)
		}
	})

	t.Run("page past the end", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("GET "+routeSmartListItems+"all/rank/asc", http.StatusOK, emptyList, headers)

		var out map[string]any
		h.ok(toolFetchSmartListItems, map[string]any{argListID: testListID, fieldPage: 9, fieldLimit: 2}, &out)

		if items, ok := out["items"].([]any); !ok || len(items) != 0 {
			t.Errorf("items = %v, want []", out["items"])
		}
	})

	t.Run("empty list", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("GET "+routeSmartListItems+"all/rank/asc", http.StatusOK, emptyList, nil)

		var out map[string]any
		h.ok(toolFetchSmartListItems, map[string]any{argListID: testListID}, &out)

		if items, ok := out["items"].([]any); !ok || len(items) != 0 {
			t.Errorf("items = %v, want []", out["items"])
		}
	})
}

// TestSmartListsEmptyAndNull checks no tool output holds a JSON null, even
// for a null filters object or a null list.
func TestSmartListsEmptyAndNull(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond(routeSmartListsMe, http.StatusOK, `[{"name":"x","ids":{"trakt":1,"slug":"x"},"filters":null}]`, nil)

	var out any

	res := h.ok(toolFetchSmartLists, map[string]any{}, &out)

	if p := findNull(out, ""); p != "" {
		t.Errorf("null at %s in %s", p, resultText(res))
	}

	if got, ok := lookup(out, "items.0.filters"); !ok || got == nil {
		t.Errorf("filters = %v (present %v), want {}", got, ok)
	}
}

func filterKeys(n int) map[string]any {
	m := make(map[string]any, n)
	for i := range n {
		m["k"+strconv.Itoa(1000+i)] = true
	}

	return m
}

func nested(levels int) map[string]any {
	var v any = "leaf"
	for range levels {
		v = map[string]any{"k": v}
	}

	m, _ := v.(map[string]any)

	return m
}

// TestCleanFiltersCaps checks the entry and depth caps at the cap and one
// past it.
func TestCleanFiltersCaps(t *testing.T) {
	t.Parallel()

	for _, n := range []int{maxFilterEntries, maxFilterEntries + 1, maxFilterEntries * 3} {
		if got, want := len(cleanFilters(filterKeys(n))), min(n, maxFilterEntries); got != want {
			t.Errorf("%d keys: kept %d, want %d", n, got, want)
		}
	}

	// Deterministic: the smallest keys survive.
	if _, ok := cleanFilters(filterKeys(maxFilterEntries + 1))["k1050"]; ok {
		t.Error("key past the cap kept")
	}

	for _, n := range []int{maxFilterEntries, maxFilterEntries + 1} {
		arr := make([]any, n)
		for i := range arr {
			arr[i] = "x"
		}

		list, _ := cleanFilters(map[string]any{"a": arr})["a"].([]any)
		if want := min(n, maxFilterEntries); len(list) != want {
			t.Errorf("%d elements: kept %d, want %d", n, len(list), want)
		}
	}

	// The filters object is level 1: nested maps survive down to maxFilterDepth.
	depthOf := func(m map[string]any) int {
		d := 1

		for {
			next, ok := m["k"].(map[string]any)
			if !ok {
				return d
			}

			m, d = next, d+1
		}
	}

	// nested(n) is the filters object with n levels of maps.
	for _, n := range []int{maxFilterDepth, maxFilterDepth + 1, maxFilterDepth + 5} {
		if got, want := depthOf(cleanFilters(nested(n))), min(n, maxFilterDepth); got != want {
			t.Errorf("%d levels: kept %d, want %d", n, got, want)
		}
	}
}

// TestSmartListForbiddenLoggedAsRejected checks that a smart-list 403 says it
// may also be Trakt refusing access (an invalid app key also gets 403), and is
// logged like every other rejected Trakt call.
func TestSmartListForbiddenLoggedAsRejected(t *testing.T) {
	t.Parallel()

	if !strings.Contains(msgSmartListForbidden, "or access denied by Trakt") {
		t.Errorf("msgSmartListForbidden = %q, want the access-denied hedge", msgSmartListForbidden)
	}

	h := newHarness(t)
	h.trakt.respond(routeSmartListsMe, http.StatusForbidden, `{}`, nil)
	h.fail(toolFetchSmartLists, map[string]any{}, msgSmartListForbidden)

	rec := toolLine(t, h.logs, toolFetchSmartLists)
	if rec["level"] != slog.LevelError.String() || rec[keyErrorClass] != errorClassTraktRejected {
		t.Errorf("line = %v, want ERROR with class %s", rec, errorClassTraktRejected)
	}
}

// TestSmartListItemsSingularType checks the singular media types of the
// smart list items filter.
func TestSmartListItemsSingularType(t *testing.T) {
	t.Parallel()

	path := func(plural string) string { return routeSmartListItems + plural + "/title/desc" }

	runArgCases(t, singularFilterCases(singularFilter{
		tool: toolFetchSmartListItems,
		args: func(typ string) map[string]any {
			return map[string]any{argListID: testListID, argType: typ, argSortBy: smartListSortTitle, argSortHow: sortHowDesc}
		},
		path:      path,
		accepted:  smartListTypes,
		emptyPath: path(mediaAll),
	}))
}
