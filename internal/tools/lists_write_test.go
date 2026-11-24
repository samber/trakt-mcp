package tools

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

const (
	toolCreateList      = "create_list"
	toolUpdateList      = "update_list"
	toolAddListItems    = "add_list_items"
	toolRemoveListItems = "remove_list_items"
	toolDeleteList      = "delete_list"

	argName           = "name"
	argDescription    = "description"
	argPrivacy        = "privacy"
	argDisplayNumbers = "display_numbers"
	argAllowComments  = "allow_comments"

	// routeMeLists is the create route; routeMeList is a list under the
	// authenticated user, always addressed as "me".
	routeMeLists = "/users/me/lists"
	routeMeList  = routeMeLists + "/" + testListSlug

	testPrivacyPublic = "public"
	// testBlankName is whitespace only: trimmed to nothing, so no name.
	testBlankName = "   "
	// testSortTraversal is a path-injection attempt in a sort argument.
	testSortTraversal = "rank/../x"
	// testMsgBadGateway is the tool error for a Trakt 502.
	testMsgBadGateway = "Trakt returned an error (status 502)"

	// testStatusRateLimited is Trakt's rate-limit status.
	testStatusRateLimited = http.StatusTooManyRequests
	// testListFixture is the list detail fixture reused as Trakt's answer to
	// a create or update.
	testListFixture = "list_summary"
)

func init() {
	accountCaseSets = append(accountCaseSets, listWriteCases)
	batchTools = append(batchTools,
		batchTool{tool: toolAddListItems, route: "POST " + routeMeList + "/items", extra: map[string]any{argListID: testListSlug}},
		batchTool{tool: toolRemoveListItems, route: "POST " + routeMeList + "/items/remove", extra: map[string]any{argListID: testListSlug}},
	)
	accountAccess[toolCreateList] = Write
	accountAccess[toolUpdateList] = Write
	accountAccess[toolAddListItems] = Write
	accountAccess[toolRemoveListItems] = Write
	accountAccess[toolDeleteList] = Destroy
}

var listWriteCases = []accountCase{
	{
		tool: toolCreateList, args: map[string]any{argName: testListName}, route: "POST " + routeMeLists,
		status: http.StatusCreated, fixture: testListFixture, object: true, want: listDetailWant, absent: []string{keyImages, fieldUser},
	},
	{
		tool: toolUpdateList, args: map[string]any{argListID: testListSlug, argName: testListName}, route: "PUT " + routeMeList + "/",
		fixture: testListFixture, object: true, want: listDetailWant, absent: []string{keyImages, fieldUser},
	},
	{
		tool: toolAddListItems, args: merge(itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemShow, testShowSlug)), map[string]any{argListID: testListSlug}),
		route: "POST " + routeMeList + "/items", status: http.StatusCreated, fixture: "sync_watchlist_add", object: true,
		want: map[string]any{keyAdded: 1, keyExisting: 1, keyDeleted: 0, keyNotFound: []any{}},
	},
	{
		tool: toolRemoveListItems, args: merge(itemsArg(itemArg(itemShow, testShowSlug)), map[string]any{argListID: testListSlug}),
		route: "POST " + routeMeList + "/items/remove", fixture: fixtureSyncRemove, object: true,
		want: map[string]any{keyDeleted: 62, keyNotFound: []any{}},
	},
	{
		tool: toolDeleteList, args: map[string]any{argListID: testListSlug}, route: "DELETE " + routeMeList + "/",
		status: http.StatusNoContent, object: true, want: map[string]any{keyDeleted: true},
	},
}

// recorded runs tool against a fake Trakt route that answers status and body,
// and returns the one request body Trakt received.
func recorded(t *testing.T, tool string, args map[string]any, route string, status int, body string) map[string]any {
	t.Helper()

	h := newHarness(t)
	log := captureBodies(h, route, status, body)

	h.ok(tool, args, nil)

	bodies := log.all()
	if len(bodies) != 1 {
		t.Fatalf("%s sent %d Trakt requests, want 1", tool, len(bodies))
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
		t.Fatalf("decode request body %q: %v", bodies[0], err)
	}

	return got
}

func TestCreateListBody(t *testing.T) {
	t.Parallel()

	fullArgs := map[string]any{
		argName: " " + testListName + " ", argDescription: "Films about robberies.", argPrivacy: "friends",
		argDisplayNumbers: true, argAllowComments: false, argSortBy: listSortAdded, argSortHow: sortHowDesc,
	}

	t.Run("all fields", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolCreateList, fullArgs, "POST "+routeMeLists, http.StatusCreated, fixture(t, testListFixture))
		want := map[string]any{
			argName: testListName, argDescription: "Films about robberies.", argPrivacy: "friends",
			argDisplayNumbers: true, argAllowComments: false, argSortBy: listSortAdded, argSortHow: sortHowDesc,
		}

		if len(got) != len(want) {
			t.Errorf("body = %v, want %v", got, want)
		}

		for k, v := range want {
			if got[k] != v {
				t.Errorf("body[%s] = %v, want %v", k, got[k], v)
			}
		}
	})

	t.Run("name only", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolCreateList, map[string]any{argName: "x"}, "POST "+routeMeLists, http.StatusCreated, fixture(t, testListFixture))
		if len(got) != 1 || got[argName] != "x" {
			t.Errorf("body = %v, want only the name", got)
		}
	})
}

func TestUpdateListBody(t *testing.T) {
	t.Parallel()

	route := "PUT " + routeMeList + "/"

	t.Run("only the given fields", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateList, map[string]any{argListID: testListSlug, argPrivacy: testPrivacyPublic, argAllowComments: false}, route, http.StatusOK, fixture(t, testListFixture))
		if len(got) != 2 || got[argPrivacy] != "public" || got[argAllowComments] != false {
			t.Errorf("body = %v, want privacy and allow_comments only", got)
		}
	})

	t.Run("empty description clears it", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateList, map[string]any{argListID: testListSlug, argDescription: ""}, route, http.StatusOK, fixture(t, testListFixture))
		if v, ok := got[argDescription]; !ok || v != "" {
			t.Errorf("body = %v, want description \"\" sent", got)
		}
	})

	t.Run("numeric list id", func(t *testing.T) {
		t.Parallel()

		got := recorded(t, toolUpdateList, map[string]any{argListID: "777", argName: "n"}, "PUT "+routeMeLists+"/777/", http.StatusOK, fixture(t, testListFixture))
		if got[argName] != "n" {
			t.Errorf("body = %v", got)
		}
	})
}

func TestListItemsBody(t *testing.T) {
	t.Parallel()

	args := merge(itemsArg(itemArg(itemMovie, testMovieSlug), itemArg(itemShow, testShowSlug), itemArg(itemSeason, testSeasonTraktID), itemArg(itemEpisode, testEpisodeIMDb)),
		map[string]any{argListID: testListSlug})

	for _, bt := range []batchTool{
		{tool: toolAddListItems, route: "POST " + routeMeList + "/items"},
		{tool: toolRemoveListItems, route: "POST " + routeMeList + "/items/remove"},
	} {
		t.Run(bt.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			log := captureBodies(h, bt.route, http.StatusOK, emptyObject)

			h.ok(bt.tool, args, nil)

			got := decodeSync(t, log.all()[0])
			if len(got.Movies) != 1 || got.Movies[0].IDs.Slug != testMovieSlug || len(got.Shows) != 1 || len(got.Seasons) != 1 ||
				got.Seasons[0].IDs.Trakt != 3572 || len(got.Episodes) != 1 || got.Episodes[0].IDs.IMDb != testEpisodeIMDb {
				t.Errorf("body = %+v, want one item of each type", got)
			}
		})
	}
}

func TestListWriteArguments(t *testing.T) {
	t.Parallel()

	long := func(n int) string { return strings.Repeat("a", n) }
	listID := func(extra map[string]any) map[string]any {
		return merge(map[string]any{argListID: testListSlug}, extra)
	}
	noRequest := func(name, tool string, args map[string]any, field string) argCase {
		return argCase{name: name, tool: tool, args: args, wantErr: invalid(field)}
	}

	cases := []argCase{
		noRequest("create empty name", toolCreateList, map[string]any{argName: ""}, fieldName),
		noRequest("create blank name", toolCreateList, map[string]any{argName: testBlankName}, fieldName),
		noRequest("create name over limit", toolCreateList, map[string]any{argName: long(maxListNameRunes + 1)}, fieldName),
		noRequest("create description over limit", toolCreateList, map[string]any{argName: "x", argDescription: long(maxListDescriptionRunes + 1)}, fieldDescription),
		noRequest("create bad privacy", toolCreateList, map[string]any{argName: "x", argPrivacy: "x"}, fieldPrivacy),
		noRequest("create upper privacy", toolCreateList, map[string]any{argName: "x", argPrivacy: "Public"}, fieldPrivacy),
		noRequest("create bad sort_by", toolCreateList, map[string]any{argName: "x", argSortBy: testSortTraversal}, fieldSortBy),
		noRequest("create bad sort_how", toolCreateList, map[string]any{argName: "x", argSortHow: "up"}, fieldSortHow),
		noRequest("update empty name", toolUpdateList, listID(map[string]any{argName: ""}), fieldName),
		noRequest("update no change", toolUpdateList, listID(nil), fieldChanges),
		noRequest("update bad privacy", toolUpdateList, listID(map[string]any{argPrivacy: "x"}), fieldPrivacy),
		noRequest("update bad list id", toolUpdateList, map[string]any{argListID: traversalID, argName: "n"}, fieldListID),
		noRequest("update empty list id", toolUpdateList, map[string]any{argListID: "", argName: "n"}, fieldListID),
		noRequest("update imdb list id", toolUpdateList, map[string]any{argListID: testShowIMDb, argName: "n"}, fieldListID),
		noRequest("delete empty list id", toolDeleteList, map[string]any{argListID: ""}, fieldListID),
		noRequest("delete bad list id", toolDeleteList, map[string]any{argListID: traversalID}, fieldListID),
		noRequest("add zero items", toolAddListItems, listID(map[string]any{argItems: []any{}}), fieldItems),
		noRequest("add 51 items", toolAddListItems, merge(listID(nil), manyMovies(maxBatchItems+1)), fieldItems),
		noRequest("add bad list id", toolAddListItems, merge(map[string]any{argListID: "A B"}, itemsArg(itemArg(itemMovie, testMovieSlug))), fieldListID),
		noRequest("add person item", toolAddListItems, merge(listID(nil), itemsArg(itemArg("person", "keanu-reeves"))), fieldItems),
		noRequest("remove zero items", toolRemoveListItems, listID(map[string]any{argItems: []any{}}), fieldItems),
		noRequest("remove 51 items", toolRemoveListItems, merge(listID(nil), manyMovies(maxBatchItems+1)), fieldItems),
		noRequest("remove bad list id", toolRemoveListItems, merge(map[string]any{argListID: ""}, itemsArg(itemArg(itemMovie, testMovieSlug))), fieldListID),
		// Boundary values: accepted.
		{
			name: "name at limit", tool: toolCreateList, args: map[string]any{argName: long(maxListNameRunes)},
			route: "POST " + routeMeLists, body: fixture(t, testListFixture),
		},
		{
			name: "description at limit", tool: toolCreateList, args: map[string]any{argName: "x", argDescription: long(maxListDescriptionRunes)},
			route: "POST " + routeMeLists, body: fixture(t, testListFixture),
		},
	}

	for _, p := range listPrivacies {
		cases = append(cases, argCase{
			name: "create privacy " + p, tool: toolCreateList, args: map[string]any{argName: "x", argPrivacy: p},
			route: "POST " + routeMeLists, body: fixture(t, testListFixture),
		})
	}

	runArgCases(t, cases)
}

// TestListWriteAlwaysActsAsMe checks the owner in the path is never taken
// from an argument.
func TestListWriteAlwaysActsAsMe(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("DELETE "+routeMeList+"/", http.StatusNoContent, "", nil)

	// An unknown "user" argument is ignored or rejected by the schema, never
	// forwarded to another owner's path.
	res := h.call(toolDeleteList, map[string]any{argListID: testListSlug, argUser: testOtherUser})
	if res.IsError {
		return
	}

	for _, r := range h.trakt.seen() {
		if strings.Contains(r.URL.Path, testOtherUser) {
			t.Errorf("request path %s names another user", r.URL.Path)
		}
	}
}

func TestListWriteTraktFailures(t *testing.T) {
	t.Parallel()

	tools := []struct {
		tool  string
		args  map[string]any
		route string
	}{
		{toolCreateList, map[string]any{argName: "x"}, "POST " + routeMeLists},
		{toolUpdateList, map[string]any{argListID: testListSlug, argName: "x"}, "PUT " + routeMeList + "/"},
		{toolAddListItems, merge(itemsArg(itemArg(itemMovie, testMovieSlug)), map[string]any{argListID: testListSlug}), "POST " + routeMeList + "/items"},
		{toolRemoveListItems, merge(itemsArg(itemArg(itemMovie, testMovieSlug)), map[string]any{argListID: testListSlug}), "POST " + routeMeList + "/items/remove"},
		{toolDeleteList, map[string]any{argListID: testListSlug}, "DELETE " + routeMeList + "/"},
	}

	hostile := `{"error_description":"` + injectionMarker + `"}`
	failures := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"not found", http.StatusNotFound, hostile, msgNotFound},
		{"unauthorized", http.StatusUnauthorized, hostile, msgUpstreamAuth},
		{"account limit", statusTraktAccountLimit, hostile, msgAccountLimit},
		{"vip required", http.StatusUpgradeRequired, hostile, msgVIPRequired},
		{"server error", http.StatusInternalServerError, hostile, "Trakt returned an error (status 500)"},
		{"bad gateway", http.StatusBadGateway, ``, testMsgBadGateway},
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

		t.Run(tl.tool+"/rate limited", func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tl.route, testStatusRateLimited, hostile, nil)

			res := h.call(tl.tool, tl.args)
			if !res.IsError || !strings.Contains(resultText(res), "rate limited") || strings.Contains(resultText(res), injectionMarker) {
				t.Errorf("result = %v %q, want a rate limit error without Trakt text", res.IsError, resultText(res))
			}
		})
	}

	// Tools that read a body must reject a malformed 200.
	for _, tl := range tools[:4] {
		t.Run(tl.tool+"/malformed body", func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(tl.route, http.StatusOK, `{"not": "closed"`, nil)
			h.fail(tl.tool, tl.args, msgInternal)
		})
	}
}

func TestCreateListAccountLimitNoRetry(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("POST "+routeMeLists, statusTraktAccountLimit, `{}`, nil)
	h.fail(toolCreateList, map[string]any{argName: "x"}, msgAccountLimit)

	if got := len(h.trakt.seen()); got != 1 {
		t.Errorf("Trakt requests = %d, want 1 (no retry)", got)
	}
}

func TestListWriteDescriptions(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	if d := tools[toolDeleteList].Description; !strings.Contains(d, "irreversible") {
		t.Errorf("delete_list description %q does not say it is irreversible", d)
	}

	for _, name := range []string{toolCreateList, toolUpdateList} {
		if d := tools[name].Description; !strings.Contains(d, untrustedNote) {
			t.Errorf("%s returns list text but its description lacks the untrusted note", name)
		}
	}
}

func TestListDetailOutputBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", 5000)
	h := newHarness(t)
	h.trakt.respond("POST "+routeMeLists, http.StatusCreated, `{"name":"`+long+`","description":"`+long+`","ids":{"trakt":1,"slug":"s"}}`, nil)

	var out ListDetail

	h.ok(toolCreateList, map[string]any{argName: "x"}, &out)

	if n := len([]rune(out.Name)); n > maxListNameRunes+1 {
		t.Errorf("name has %d runes, want it bounded", n)
	}
}
