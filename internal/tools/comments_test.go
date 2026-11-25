package tools

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	toolListComments        = "list_comments"
	toolFetchComment        = "get_comment"
	toolFetchCommentReplies = "list_comment_replies"

	argCommentID     = "comment_id"
	argShowSpoilers  = "show_spoilers"
	testCommentID    = "201"
	testCommentText  = "Great one."
	spoilerText      = "He dies at the end."
	routeShowSlug    = "/shows/" + testShowSlug
	routeCommentsEnd = "/comments/newest"

	pathUntrustedText = "items.0.untrusted_text"
	pathFirstCommID   = "items.0.id"
	keyUntrustedText  = "untrusted_text"
	keyRatingField    = ".rating"
)

// newToolNames are the tools of the movie, people and comment groups.
var newToolNames = []string{
	toolListPopular, toolListAnticipated, toolListFavorited, toolListPlayed, toolListWatched,
	toolFetchBoxofficeMovies, toolSearch, toolGetRatings, toolListVideos, toolListRelated, toolListPeople,
	toolListComments,
	toolFetchPersonSummary, toolListPersonCredits,
	toolFetchComment, toolFetchCommentReplies,
}

// commentWant is what the three-comment fixtures return by default: the
// spoiler (second in the fixture) is dropped, so the review is second.
var commentWant = map[string]any{
	pathFirstCommID: 101, "items.0.parent_id": 0, "items.0.created_at": "2024-01-01T10:00:00.000Z", "items.0.updated_at": "2024-01-02T10:00:00.000Z",
	"items.0.likes": 7, "items.0.replies": 2, "items.0.rating": 8, "items.0.is_review": false, "items.0.is_spoiler": false,
	pathUntrustedText: testCommentText, "items.0.truncated": false, "items.0.user.username": testUsername, "items.0.user.slug": testUsername,
	"items.1.id": 103, "items.1.is_review": true, "items.1.rating": 9,
}

// commentAbsent lists what Trakt sends and the tools must drop.
var commentAbsent = []string{"items.2", "items.0.comment", "items.0.user.avatar", "items.0.user.private", "items.0.user.vip", "items.0.user_stats", "items.0.spoiler", "items.0.review"}

func commentListCase(tool string, args map[string]any, route, fixtureName string) catalogCase {
	return catalogCase{
		tool: tool, args: args, route: route, fixture: fixtureName, headers: pagedHeaders,
		wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want:      merge(commentWant, map[string]any{pathPageCount: 3}), absent: commentAbsent,
	}
}

var commentCases = []catalogCase{
	commentListCase(toolListComments, merge(movieItem(testMovieSlug), pagedArgs), "GET "+routeMovieSlug+"/comments/newest", "movie_comments"),
	commentListCase(toolListComments, merge(showItem(testShowSlug), pagedArgs), "GET "+routeShowSlug+routeCommentsEnd, "show_comments"),
	commentListCase(toolListComments, merge(seasonItem(testShowSlug, 1), pagedArgs), "GET "+routeShowSlug+"/seasons/1"+routeCommentsEnd, "season_comments"),
	commentListCase(toolListComments, merge(episodeItem(testShowSlug, 1, 2), pagedArgs), "GET "+routeShowSlug+"/seasons/1/episodes/2"+routeCommentsEnd, "episode_comments"),
	{
		tool: toolFetchComment, args: map[string]any{argCommentID: testCommentID}, route: "GET /comments/" + testCommentID, fixture: "comment_summary", object: true,
		want: map[string]any{
			"id": 201, "parent_id": 0, "likes": 7, keyReplies: 2, "rating": 8, "is_review": false, "is_spoiler": false,
			keyUntrustedText: testCommentText, "truncated": false, "user.username": testUsername, "user.slug": testUsername,
		},
		absent: []string{"withheld", "comment", "user.avatar", "user_stats"},
	},
	{
		tool: toolFetchCommentReplies, args: merge(map[string]any{argCommentID: testCommentID}, pagedArgs), route: "GET /comments/" + testCommentID + "/replies",
		fixture: "comment_replies", headers: pagedHeaders, wantQuery: map[string]string{fieldPage: "1", fieldLimit: "2"},
		want: map[string]any{pathFirstCommID: 301, "items.0.parent_id": 201, pathUntrustedText: "I agree.", pathPageCount: 3},
		// the spoiler reply (302) is dropped.
		absent: []string{"items.1"},
	},
}

// findNullExcept returns the path of the first JSON null in v other than a
// rating, which is null when the author did not rate the title.
func findNullExcept(v any, path string) string {
	switch node := v.(type) {
	case nil:
		if strings.HasSuffix(path, keyRatingField) {
			return ""
		}

		return path
	case map[string]any:
		for k, child := range node {
			if p := findNullExcept(child, path+"."+k); p != "" {
				return p
			}
		}
	case []any:
		for i, child := range node {
			if p := findNullExcept(child, path+"."+strconv.Itoa(i)); p != "" {
				return p
			}
		}
	}

	return ""
}

func TestCommentToolsOutputs(t *testing.T) {
	t.Parallel()

	for _, tc := range commentCases {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t, withOnlyTools(tc.tool))
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

			requireQuery(t, tc, h.trakt.seen())
		})
	}
}

// TestCommentToolsNeverReturnNullArrays feeds every comment tool the bodies
// Trakt sends for nothing and requires no JSON null in the output, apart
// from an unrated comment's rating.
func TestCommentToolsNeverReturnNullArrays(t *testing.T) {
	t.Parallel()

	for _, tc := range commentCases {
		bodies := []string{emptyList, nullBody}
		if tc.object {
			bodies = []string{emptyObject, nullBody}
		}

		for _, body := range bodies {
			t.Run(tc.tool+"/"+body, func(t *testing.T) {
				t.Parallel()

				h := newHarness(t, withOnlyTools(tc.tool))
				h.trakt.respond(tc.route, http.StatusOK, body, nil)

				var out any

				res := h.ok(tc.tool, tc.args, &out)

				if p := findNullExcept(out, ""); p != "" {
					t.Errorf("%s: null at %s in %s", tc.tool, p, resultText(res))
				}

				if !tc.object {
					if items, ok := lookup(out, "items"); !ok || items == nil {
						t.Errorf("%s: items = %v in %s, want []", tc.tool, items, resultText(res))
					}
				}
			})
		}
	}
}

func TestCommentNotFoundIsToolError(t *testing.T) {
	t.Parallel()

	for _, tc := range commentCases {
		t.Run(tc.tool, func(t *testing.T) {
			t.Parallel()

			newHarness(t, withOnlyTools(tc.tool)).fail(tc.tool, tc.args, msgNotFound)
		})
	}
}

// listCommentCases are the cases whose Trakt route carries a sort.
func listCommentTools() []catalogCase {
	return []catalogCase{commentCases[0], commentCases[1], commentCases[2], commentCases[3]}
}

func TestCommentsDropSpoilersByDefault(t *testing.T) {
	t.Parallel()

	all := append(listCommentTools(), commentCases[5])

	for _, tc := range all {
		for _, showSpoilers := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/show_spoilers=%v", tc.tool, showSpoilers), func(t *testing.T) {
				t.Parallel()

				h := newHarness(t, withOnlyTools(tc.tool))
				h.trakt.respond(tc.route, http.StatusOK, fixture(t, tc.fixture), nil)

				args := map[string]any{}
				maps.Copy(args, tc.args)

				if showSpoilers {
					args[argShowSpoilers] = true
				}

				var out Listing[Comment]

				h.ok(tc.tool, args, &out)

				spoilers := 0

				for _, c := range out.Items {
					if c.IsSpoiler {
						spoilers++
					}
				}

				switch {
				case showSpoilers && spoilers != 1:
					t.Errorf("spoilers returned = %d in %+v, want 1 with show_spoilers", spoilers, out.Items)
				case !showSpoilers && spoilers != 0:
					t.Errorf("spoilers returned = %d in %+v, want none by default", spoilers, out.Items)
				}
			})
		}
	}
}

// commentBody is a Trakt comment list with one comment of the given text.
func commentBody(t *testing.T, text string) string {
	t.Helper()

	raw, err := json.Marshal([]map[string]any{{
		"id": 1, "parent_id": 0, "created_at": "2024-01-01T10:00:00.000Z", "updated_at": "2024-01-01T10:00:00.000Z",
		"comment": text, "spoiler": false, "review": false, keyReplies: 0, "likes": 0,
		"user_stats": map[string]any{"rating": 5}, "user": map[string]any{keyUsername: testUsername, keyIDs: map[string]any{keySlug: testUsername}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestCommentTextTruncatedAndInUntrustedField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		runes     int
		truncated bool
	}{
		{"below the limit", maxCommentChars - 1, false},
		{"exactly the limit", maxCommentChars, false},
		{"one over the limit", maxCommentChars + 1, true},
		{"fifty over the limit", maxCommentChars + 50, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// é is two bytes: the bound counts runes, not bytes.
			h := newHarness(t)
			h.trakt.respond("GET "+routeShowSlug+routeCommentsEnd, http.StatusOK, commentBody(t, strings.Repeat("é", tc.runes)), nil)

			var out Listing[Comment]

			h.ok(toolListComments, showItem(testShowSlug), &out)

			if len(out.Items) != 1 {
				t.Fatalf("items = %d, want 1", len(out.Items))
			}

			got := out.Items[0]

			if got.Truncated != tc.truncated {
				t.Errorf("truncated = %v, want %v", got.Truncated, tc.truncated)
			}

			if want := min(tc.runes, maxCommentChars); utf8.RuneCountInString(got.UntrustedText) != want {
				t.Errorf("untrusted_text has %d runes, want %d", utf8.RuneCountInString(got.UntrustedText), want)
			}
		})
	}
}

func TestCommentTextOnlyInUntrustedField(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET "+routeShowSlug+routeCommentsEnd, http.StatusOK, commentBody(t, injectionText), nil)

	var out []map[string]any

	var wrapper struct {
		Items []map[string]any `json:"items"`
	}

	res := h.ok(toolListComments, showItem(testShowSlug), &wrapper)
	out = wrapper.Items

	for key, v := range out[0] {
		if s, ok := v.(string); ok && strings.Contains(s, "Ignore") && key != keyUntrustedText {
			t.Errorf("injection text in field %q", key)
		}
	}

	if got := strings.Count(resultText(res), injectionText); got != 1 {
		t.Errorf("injection text appears %d times in the result, want once", got)
	}

	if strings.Contains(h.logs.String(), "Ignore") {
		t.Errorf("comment text reached the logs: %s", h.logs.String())
	}
}

func TestCommentErrorsNeverEchoTraktText(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusInternalServerError, http.StatusBadRequest, http.StatusForbidden} {
		h := newHarness(t)
		h.trakt.respond("GET "+routeShowSlug+routeCommentsEnd, status, `{"error":"`+injectionText+`"}`, nil)

		res := h.call(toolListComments, showItem(testShowSlug))
		if !res.IsError || strings.Contains(resultText(res), "Ignore") || strings.Contains(h.logs.String(), "Ignore") {
			t.Errorf("status %d: result = %v %q, logs hold the text = %v", status, res.IsError, resultText(res), strings.Contains(h.logs.String(), "Ignore"))
		}
	}
}

func TestCommentUserHasOnlyUsernameAndSlug(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET "+routeShowSlug+routeCommentsEnd, http.StatusOK, fixture(t, "show_comments"), nil)

	var out struct {
		Items []struct {
			User map[string]any `json:"user"`
		} `json:"items"`
	}

	res := h.ok(toolListComments, showItem(testShowSlug), &out)

	for _, item := range out.Items {
		keys := make([]string, 0, len(item.User))
		for k := range item.User {
			keys = append(keys, k)
		}

		slices.Sort(keys)

		if !slices.Equal(keys, []string{keySlug, keyUsername}) {
			t.Errorf("user keys = %v, want slug and username only", keys)
		}
	}

	if strings.Contains(resultText(res), "dropped.example") {
		t.Errorf("avatar URL leaked: %s", resultText(res))
	}
}

func TestCommentIsReviewFlag(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET "+routeShowSlug+routeCommentsEnd, http.StatusOK, fixture(t, "show_comments"), nil)

	var out Listing[Comment]

	h.ok(toolListComments, showItem(testShowSlug), &out)

	if len(out.Items) != 2 || out.Items[0].IsReview || !out.Items[1].IsReview {
		t.Errorf("is_review flags = %+v, want [false true]", out.Items)
	}
}

func TestCommentRatingIsNullWhenUnrated(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond("GET /comments/"+testCommentID, http.StatusOK, `{"id":201,"comment":"x","user":{"username":"u","ids":{"slug":"u"}}}`, nil)

	var out map[string]any

	h.ok(toolFetchComment, map[string]any{argCommentID: testCommentID}, &out)

	if v, ok := out["rating"]; !ok || v != nil {
		t.Errorf("rating = %v (present %v), want null", v, ok)
	}
}

func TestCommentSortValidated(t *testing.T) {
	t.Parallel()

	var cases []argCase

	for _, sortBy := range commentSorts {
		cases = append(cases,
			argCase{name: "show sort " + sortBy, tool: toolListComments, args: merge(showItem(testShowSlug), map[string]any{fieldSort: sortBy}), route: "GET " + routeShowSlug + "/comments/" + sortBy, body: emptyList},
			argCase{name: "movie sort " + sortBy, tool: toolListComments, args: merge(movieItem(testMovieSlug), map[string]any{fieldSort: sortBy}), route: "GET " + routeMovieSlug + "/comments/" + sortBy, body: emptyList},
		)
	}

	cases = append(cases,
		argCase{name: "season sort replies", tool: toolListComments, args: merge(seasonItem(testShowSlug, 2), map[string]any{fieldSort: "replies"}), route: "GET " + routeShowSlug + "/seasons/2/comments/replies", body: emptyList},
		argCase{name: "episode sort oldest", tool: toolListComments, args: merge(episodeItem(testShowSlug, 2, 3), map[string]any{fieldSort: "oldest"}), route: "GET " + routeShowSlug + "/seasons/2/episodes/3/comments/oldest", body: emptyList},
		argCase{name: "default sort is newest", tool: toolListComments, args: episodeItem(testShowSlug, 1, 1), route: "GET " + routeShowSlug + "/seasons/1/episodes/1/comments/newest", body: emptyList},
		argCase{name: "sort unknown", tool: toolListComments, args: merge(showItem(testShowSlug), map[string]any{fieldSort: "popular"}), wantErr: invalid(fieldSort)},
		argCase{name: "sort wrong case", tool: toolListComments, args: merge(movieItem(testMovieSlug), map[string]any{fieldSort: "Likes"}), wantErr: invalid(fieldSort)},
		argCase{name: "sort path injection", tool: toolListComments, args: merge(seasonItem(testShowSlug, 1), map[string]any{fieldSort: traversalID}), wantErr: invalid(fieldSort)},
		argCase{name: "sort query injection", tool: toolListComments, args: merge(episodeItem(testShowSlug, 1, 1), map[string]any{fieldSort: "likes?x=1"}), wantErr: invalid(fieldSort)},
	)

	runArgCases(t, cases)
}

func TestCommentToolArguments(t *testing.T) {
	t.Parallel()

	cases := []argCase{
		{name: "show id traversal", tool: toolListComments, args: showItem(traversalID), wantErr: invalid(fieldID)},
		{name: "show id person imdb", tool: toolListComments, args: showItem(personIMDb), wantErr: invalid(fieldID)},
		{name: "movie id traversal", tool: toolListComments, args: movieItem(traversalID), wantErr: invalid(fieldID)},
		{name: "season -1", tool: toolListComments, args: seasonItem(testShowSlug, -1), wantErr: invalid(fieldSeason)},
		{name: "season 0", tool: toolListComments, args: seasonItem(testShowSlug, 0), route: "GET " + routeShowSlug + "/seasons/0" + routeCommentsEnd, body: emptyList},
		{name: "season 999", tool: toolListComments, args: seasonItem(testShowSlug, 999), route: "GET " + routeShowSlug + "/seasons/999" + routeCommentsEnd, body: emptyList},
		{name: "season 1000", tool: toolListComments, args: seasonItem(testShowSlug, 1000), wantErr: invalid(fieldSeason)},
		{name: "episode 0", tool: toolListComments, args: episodeItem(testShowSlug, 1, 0), wantErr: invalid(fieldEpisode)},
		{name: "episode 1", tool: toolListComments, args: episodeItem(testShowSlug, 1, 1), route: "GET " + routeShowSlug + "/seasons/1/episodes/1" + routeCommentsEnd, body: emptyList},
		{name: "episode 9999", tool: toolListComments, args: episodeItem(testShowSlug, 1, 9999), route: "GET " + routeShowSlug + "/seasons/1/episodes/9999" + routeCommentsEnd, body: emptyList},
		{name: "episode 10000", tool: toolListComments, args: episodeItem(testShowSlug, 1, 10000), wantErr: invalid(fieldEpisode)},
		{name: "episode season out of range", tool: toolListComments, args: episodeItem(testShowSlug, 1000, 1), wantErr: invalid(fieldSeason)},
		{name: nameLimit101, tool: toolListComments, args: merge(movieItem(testMovieSlug), map[string]any{fieldLimit: 101}), wantErr: invalid(fieldLimit)},
		{name: "limit 100", tool: toolListComments, args: merge(movieItem(testMovieSlug), map[string]any{fieldLimit: 100}), route: "GET " + routeMovieSlug + routeCommentsEnd, body: emptyList, wantQuery: map[string]string{fieldLimit: "100"}},
		{name: namePage0, tool: toolListComments, args: merge(showItem(testShowSlug), map[string]any{fieldPage: 0}), wantErr: invalid(fieldPage)},
		{name: "replies limit 0 is the default", tool: toolFetchCommentReplies, args: map[string]any{argCommentID: testCommentID, fieldLimit: 0}, route: "GET /comments/201/replies", body: emptyList, wantQuery: map[string]string{fieldLimit: "10"}},
		{name: "replies page 0", tool: toolFetchCommentReplies, args: map[string]any{argCommentID: testCommentID, fieldPage: 0}, wantErr: invalid(fieldPage)},
	}

	runArgCases(t, cases)
}

func TestCommentIDNumericOnly(t *testing.T) {
	t.Parallel()

	var cases []argCase

	for _, tool := range []string{toolFetchComment, toolFetchCommentReplies} {
		suffix, body := "", emptyObject
		if tool == toolFetchCommentReplies {
			suffix, body = "/replies", emptyList
		}

		cases = append(cases,
			argCase{name: tool + " numeric", tool: tool, args: map[string]any{argCommentID: testCommentID}, route: "GET /comments/201" + suffix, body: body, wantPath: "/comments/201" + suffix},
			argCase{name: tool + " max length", tool: tool, args: map[string]any{argCommentID: strings.Repeat("9", maxCommentIDLength)}, route: "GET /comments/" + strings.Repeat("9", maxCommentIDLength) + suffix, body: body},
			argCase{name: tool + " too long", tool: tool, args: map[string]any{argCommentID: strings.Repeat("9", maxCommentIDLength+1)}, wantErr: invalid(argCommentID)},
			argCase{name: tool + " slug", tool: tool, args: map[string]any{argCommentID: "great-comment"}, wantErr: invalid(argCommentID)},
			argCase{name: tool + " traversal", tool: tool, args: map[string]any{argCommentID: traversalID}, wantErr: invalid(argCommentID)},
			argCase{name: tool + " negative", tool: tool, args: map[string]any{argCommentID: "-1"}, wantErr: invalid(argCommentID)},
			argCase{name: tool + " exponent", tool: tool, args: map[string]any{argCommentID: "1e3"}, wantErr: invalid(argCommentID)},
			argCase{name: tool + " space", tool: tool, args: map[string]any{argCommentID: "1 2"}, wantErr: invalid(argCommentID)},
			argCase{name: tool + " imdb", tool: tool, args: map[string]any{argCommentID: testShowIMDb}, wantErr: invalid(argCommentID)},
			argCase{name: tool + " empty", tool: tool, args: map[string]any{argCommentID: ""}, wantErr: invalid(argCommentID)},
		)
	}

	runArgCases(t, cases)
}

func TestFetchCommentWithholdsSpoilers(t *testing.T) {
	t.Parallel()

	body := `{"id":202,"comment":"` + spoilerText + `","spoiler":true,"review":false,"replies":0,"likes":0,"user":{"username":"u","ids":{"slug":"u"}}}`

	h := newHarness(t)
	h.trakt.respond("GET /comments/202", http.StatusOK, body, nil)

	var hidden, shown Comment

	h.ok(toolFetchComment, map[string]any{argCommentID: "202"}, &hidden)

	if hidden.UntrustedText != "" || !hidden.Withheld || !hidden.IsSpoiler {
		t.Errorf("default = %+v, want the text withheld and is_spoiler set", hidden)
	}

	h.ok(toolFetchComment, map[string]any{argCommentID: "202", argShowSpoilers: true}, &shown)

	if shown.UntrustedText != spoilerText || shown.Withheld {
		t.Errorf("show_spoilers = %+v, want the text", shown)
	}
}

// spoilerPageBody is a Trakt comment page holding n comments, all spoilers
// when spoilers is set.
func spoilerPageBody(t *testing.T, n int, spoilers bool) string {
	t.Helper()

	items := make([]map[string]any, 0, n)
	for i := range n {
		items = append(items, map[string]any{
			"id": i + 1, "comment": "c", "spoiler": spoilers,
			"user": map[string]any{keyUsername: testUsername, keyIDs: map[string]any{keySlug: testUsername}},
		})
	}

	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

// serveCommentPages answers each page request with body(page) and a page
// count of pageCount.
func serveCommentPages(h *harness, route string, pageCount int, body func(page int) string) {
	h.trakt.handle(route, func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get(fieldPage))
		if err != nil {
			page = 1
		}

		w.Header().Set("X-Pagination-Page", strconv.Itoa(page))
		w.Header().Set("X-Pagination-Page-Count", strconv.Itoa(pageCount))
		w.Header().Set("Content-Type", "application/json")

		if _, err := w.Write([]byte(body(page))); err != nil {
			panic(err)
		}
	})
}

func TestCommentsSpoilerOnlyPageContinuesToNextPage(t *testing.T) {
	t.Parallel()

	const route = "GET " + routeShowSlug + routeCommentsEnd

	t.Run("later page has comments", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		serveCommentPages(h, route, 3, func(page int) string { return spoilerPageBody(t, 2, page == 1) })

		var out Listing[Comment]

		h.ok(toolListComments, merge(showItem(testShowSlug), map[string]any{fieldLimit: 2}), &out)

		if len(out.Items) != 2 {
			t.Fatalf("items = %d, want the 2 comments of page 2", len(out.Items))
		}

		for _, c := range out.Items {
			if c.IsSpoiler {
				t.Errorf("spoiler returned: %+v", c)
			}
		}
	})

	t.Run("every page is spoilers", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		serveCommentPages(h, route, maxAutoPages*3, func(int) string { return spoilerPageBody(t, 2, true) })

		var out Listing[Comment]

		h.ok(toolListComments, merge(showItem(testShowSlug), map[string]any{fieldLimit: 2}), &out)

		if len(out.Items) != 0 {
			t.Errorf("items = %d, want 0", len(out.Items))
		}

		if got := len(h.trakt.seen()); got > maxAutoPages {
			t.Errorf("Trakt requests = %d, want at most %d", got, maxAutoPages)
		}
	})
}

// TestNewToolsAreAllReadOnly requires the 17 tools of the movie, people and
// comment groups to be registered read-only and open-world.
func TestNewToolsAreAllReadOnly(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for _, name := range newToolNames {
		tool, ok := tools[name]
		if !ok {
			t.Errorf("tool %s is not registered", name)
			continue
		}

		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || (a.DestructiveHint != nil && *a.DestructiveHint) || a.OpenWorldHint == nil || !*a.OpenWorldHint {
			t.Errorf("%s annotations = %+v, want read-only and open-world", name, a)
		}

		if tool.Title == "" || tool.Description == "" {
			t.Errorf("%s has an empty title or description", name)
		}
	}

	if len(newToolNames) != 16 {
		t.Errorf("newToolNames holds %d tools, want 16", len(newToolNames))
	}
}

// TestCommentToolDescriptionsWarnAboutUntrustedText requires every tool that
// returns user-written text to say so.
func TestCommentToolDescriptionsWarnAboutUntrustedText(t *testing.T) {
	t.Parallel()

	tools := listTools(t, NewServer(offlineDeps()))

	for _, tc := range commentCases {
		tool, ok := tools[tc.tool]
		if !ok {
			t.Errorf("tool %s is not registered", tc.tool)
			continue
		}

		if d := tool.Description; !strings.Contains(d, thirdPartyNote) || !strings.Contains(d, "instructions") {
			t.Errorf("%s description %q does not warn about untrusted third-party text", tc.tool, d)
		}
	}
}
