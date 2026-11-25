package tools

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

const (
	toolListRecommendations  = "list_recommendations"
	toolHideRecommendation   = "hide_recommendation"
	toolUnhideRecommendation = "unhide_recommendation"

	// Routes keep the trailing slash the generated client sends for the
	// recommendation lists.
	routeMovieRecommendations = "GET /recommendations/movies/"
	routeShowRecommendations  = "GET /recommendations/shows/"
	pathHiddenRecsRemove      = "/users/hidden/recommendations/remove"
	routeHiddenRecsRemove     = "POST " + pathHiddenRecsRemove

	argIgnoreCollected   = "ignore_collected"
	argIgnoreWatchlisted = "ignore_watchlisted"

	// queryTrue and queryFalse are booleans as they appear in a query string.
	queryTrue  = "true"
	queryFalse = "false"

	keyHidden   = "hidden"
	keyUnhidden = "unhidden"
	// testFavoritedBy is the fixture field naming other users, which the
	// recommendation tools drop.
	testFavoritedBy = "items.0.favorited_by"
)

func init() {
	accountCaseSets = append(accountCaseSets, recommendationCases)
	accountAccess[toolListRecommendations] = Read
	accountAccess[toolHideRecommendation] = Write
	accountAccess[toolUnhideRecommendation] = Write
}

var recommendationCases = []accountCase{
	{
		tool: toolListRecommendations, args: typeArg(typeMovie), route: routeMovieRecommendations, fixture: "recommendations_movies",
		want: merge(map[string]any{argType: typeMovie}, inItem(map[string]any{
			pathFirstTitle: testMovieTitle, pathFirstIDSlug: testMovieSlug, pathFirstIDTrakt: 481,
			"items.1.title": "Untitled", pathSecondGenres: []any{},
		})),
		absent: inItemPaths([]string{testFavoritedBy, "items.1.year", "items.0.ids.plex"}),
	},
	{
		tool: toolListRecommendations, args: typeArg(typeShow), route: routeShowRecommendations, fixture: "recommendations_shows",
		want: merge(map[string]any{argType: typeShow}, inItem(map[string]any{
			pathFirstTitle: testShowTitle, pathFirstIDSlug: testShowSlug, "items.0.network": "AMC",
			pathSecondTitle: "Better Call Saul", pathSecondGenres: []any{},
		})),
		absent: inItemPaths([]string{testFavoritedBy, "items.1.overview"}),
	},
	{
		tool: toolHideRecommendation, args: movieItem(testMovieSlug),
		route: "DELETE /recommendations/movies/" + testMovieSlug, status: http.StatusNoContent, object: true,
		want: map[string]any{keyHidden: true, argType: itemMovie, argID: testMovieSlug},
	},
	{
		tool: toolHideRecommendation, args: showItem(testShowSlug),
		route: "DELETE /recommendations/shows/" + testShowSlug, status: http.StatusNoContent, object: true,
		want: map[string]any{keyHidden: true, argType: itemShow, argID: testShowSlug},
	},
	{
		tool: toolUnhideRecommendation, args: movieItem(testMovieSlug),
		route: routeHiddenRecsRemove, fixture: "hidden_recommendations_remove", object: true,
		want: map[string]any{keyUnhidden: true, argType: itemMovie, argID: testMovieSlug},
	},
	{
		tool: toolUnhideRecommendation, args: showItem(testShowSlug),
		route: routeHiddenRecsRemove, fixture: "hidden_recommendations_not_found", object: true,
		want: map[string]any{keyUnhidden: false, argType: itemShow, argID: testShowSlug},
	},
}

// TestIgnoreCollectedWatchlistedFlags checks that both recommendation lists
// forward ignore_collected and ignore_watchlisted only when the caller set
// them, always ask for full items, and bound limit.
func TestIgnoreCollectedWatchlistedFlags(t *testing.T) {
	t.Parallel()

	cases := []argCase{}

	for _, rt := range []struct{ typ, route string }{
		{typeMovie, routeMovieRecommendations},
		{typeShow, routeShowRecommendations},
	} {
		with := func(extra map[string]any) map[string]any { return merge(typeArg(rt.typ), extra) }

		cases = append(cases,
			argCase{
				name: rt.typ + " defaults", tool: toolListRecommendations, args: with(nil), route: rt.route, body: emptyList,
				wantQuery: map[string]string{fieldLimit: strconv.Itoa(defaultLimit), queryExtended: extendedFull},
				noQuery:   []string{argIgnoreCollected, argIgnoreWatchlisted, fieldPage},
			},
			argCase{
				name: rt.typ + " both true", tool: toolListRecommendations, args: with(map[string]any{argIgnoreCollected: true, argIgnoreWatchlisted: true}),
				route: rt.route, body: emptyList,
				wantQuery: map[string]string{argIgnoreCollected: queryTrue, argIgnoreWatchlisted: queryTrue},
			},
			argCase{
				name: rt.typ + " collected false only", tool: toolListRecommendations, args: with(map[string]any{argIgnoreCollected: false}),
				route: rt.route, body: emptyList,
				wantQuery: map[string]string{argIgnoreCollected: queryFalse},
				noQuery:   []string{argIgnoreWatchlisted},
			},
			argCase{
				name: rt.typ + " watchlisted only", tool: toolListRecommendations, args: with(map[string]any{argIgnoreWatchlisted: true}),
				route: rt.route, body: emptyList,
				wantQuery: map[string]string{argIgnoreWatchlisted: queryTrue},
				noQuery:   []string{argIgnoreCollected},
			},
			argCase{
				name: rt.typ + " limit max", tool: toolListRecommendations, args: with(map[string]any{fieldLimit: maxLimit}), route: rt.route, body: emptyList,
				wantQuery: map[string]string{fieldLimit: strconv.Itoa(maxLimit)},
			},
			argCase{
				name: rt.typ + " limit one", tool: toolListRecommendations, args: with(map[string]any{fieldLimit: 1}), route: rt.route, body: emptyList,
				wantQuery: map[string]string{fieldLimit: "1"},
			},
			argCase{name: rt.typ + " limit over max", tool: toolListRecommendations, args: with(map[string]any{fieldLimit: maxLimit + 1}), wantErr: invalid(fieldLimit)},
			argCase{name: rt.typ + " limit negative", tool: toolListRecommendations, args: with(map[string]any{fieldLimit: -1}), wantErr: invalid(fieldLimit)},
		)
	}

	runArgCases(t, cases)
}

// TestRecommendationsLimitCapsOutput checks a Trakt answer longer than limit
// is cut to limit.
func TestRecommendationsLimitCapsOutput(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.trakt.respond(routeMovieRecommendations, http.StatusOK, fixture(t, "recommendations_movies"), nil)

	var out typedOut[Listing[Movie]]

	h.ok(toolListRecommendations, merge(typeArg(typeMovie), map[string]any{fieldLimit: 1}), &out)

	if len(out.Item.Items) != 1 || out.Item.Items[0].Title != testMovieTitle {
		t.Errorf("items = %+v, want only %s", out.Item.Items, testMovieTitle)
	}
}

// TestHideUnhideRoundTripPaths hides then unhides the same item and checks
// both requests: the hide path carries the id, the unhide body names the
// same id under the right type.
func TestHideUnhideRoundTripPaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, typ, id, hidePath, bodyType string
		wantIDs                           SyncIDs
	}{
		{
			name: "movie slug", typ: typeMovie,
			id: testMovieSlug, hidePath: "/recommendations/movies/" + testMovieSlug, bodyType: mediaMovies,
			wantIDs: SyncIDs{Slug: testMovieSlug},
		},
		{
			name: "movie trakt id", typ: typeMovie,
			id: testMovieTraktID, hidePath: "/recommendations/movies/" + testMovieTraktID, bodyType: mediaMovies,
			wantIDs: SyncIDs{Trakt: 481},
		},
		{
			name: "movie imdb", typ: typeMovie,
			id: testMovieIMDb, hidePath: "/recommendations/movies/" + testMovieIMDb, bodyType: mediaMovies,
			wantIDs: SyncIDs{IMDb: testMovieIMDb},
		},
		{
			name: "show slug", typ: typeShow,
			id: testShowSlug, hidePath: "/recommendations/shows/" + testShowSlug, bodyType: mediaShows,
			wantIDs: SyncIDs{Slug: testShowSlug},
		},
		{
			name: "show imdb", typ: typeShow,
			id: testShowIMDb, hidePath: "/recommendations/shows/" + testShowIMDb, bodyType: mediaShows,
			wantIDs: SyncIDs{IMDb: testShowIMDb},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond("DELETE "+tc.hidePath, http.StatusNoContent, "", nil)
			log := captureBodies(h, routeHiddenRecsRemove, http.StatusOK, fixture(t, "hidden_recommendations_remove"))

			args := itemArg(tc.typ, tc.id)
			h.ok(toolHideRecommendation, args, nil)
			h.ok(toolUnhideRecommendation, args, nil)

			seen := h.trakt.seen()
			if len(seen) != 2 {
				t.Fatalf("Trakt requests = %d, want 2", len(seen))
			}

			if seen[0].Method != http.MethodDelete || seen[0].URL.Path != tc.hidePath {
				t.Errorf("hide request = %s %s, want DELETE %s", seen[0].Method, seen[0].URL.Path, tc.hidePath)
			}

			bodies := log.all()
			if len(bodies) != 1 {
				t.Fatalf("unhide bodies = %d, want 1", len(bodies))
			}

			// Decode to a generic map so the literal JSON keys are checked,
			// not the encoding struct's tags.
			var body map[string][]struct {
				IDs map[string]any `json:"ids"`
			}
			if err := json.Unmarshal([]byte(bodies[0]), &body); err != nil {
				t.Fatalf("decode unhide body %q: %v", bodies[0], err)
			}

			if len(body) != 1 || len(body[tc.bodyType]) != 1 {
				t.Fatalf("unhide body = %s, want one %s entry only", bodies[0], tc.bodyType)
			}

			got := body[tc.bodyType][0].IDs
			if len(got) != 1 {
				t.Errorf("unhide ids = %v, want exactly one id", got)
			}

			want, err := json.Marshal(tc.wantIDs)
			if err != nil {
				t.Fatal(err)
			}

			gotRaw, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}

			if string(gotRaw) != string(want) {
				t.Errorf("unhide ids = %s, want %s", gotRaw, want)
			}
		})
	}
}

// TestHideUnhideRejectBadIDs checks that ids are validated before Trakt is
// called: no traversal, no person or episode id forms.
func TestHideUnhideRejectBadIDs(t *testing.T) {
	t.Parallel()

	cases := []argCase{}

	for _, tool := range []string{toolHideRecommendation, toolUnhideRecommendation} {
		for _, typ := range []string{typeMovie, typeShow} {
			for _, bad := range []string{"", traversalID, personIMDb, "Breaking Bad", "481\n"} {
				cases = append(cases, argCase{
					name: tool + " " + typ + " " + strconv.Quote(bad), tool: tool, args: itemArg(typ, bad), wantErr: invalid(fieldID),
				})
			}
		}
	}

	runArgCases(t, cases)
}

// TestHideRecommendationNotFound maps Trakt's 404 for an unknown item.
func TestHideRecommendationNotFound(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	h.fail(toolHideRecommendation, movieItem("unknown-movie"), msgNotFound)
	h.fail(toolHideRecommendation, showItem("unknown-show"), msgNotFound)
}
