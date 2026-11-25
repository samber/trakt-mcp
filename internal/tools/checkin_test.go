package tools

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	toolCheckin       = "checkin"
	toolCancelCheckin = "cancel_checkin"

	routeCheckin = "/checkin"

	argShowTitle = "show_title"
	argShowYear  = "show_year"
	argMessage   = "message"

	keyCheckedIn        = "checked_in"
	keyAlreadyCheckedIn = "already_checked_in"
	keyExpiresAt        = "expires_at"
	testExpiresAt       = "2024-03-01T21:00:00.000Z"
	testShowYear        = 2008
)

func init() {
	accountCaseSets = append(accountCaseSets, checkinCases)
	accountAccess[toolCheckin] = Write
	accountAccess[toolCancelCheckin] = Destroy
}

func showEpisode(extra map[string]any) map[string]any {
	return merge(map[string]any{fieldSeason: 1, fieldEpisode: 1}, extra)
}

// showIDCheckin are the arguments of a TV show check-in by id, at season 1
// episode 1 unless extra says otherwise.
func showIDCheckin(id string, extra map[string]any) map[string]any {
	return merge(showItem(id), showEpisode(extra))
}

// showTitleCheckin are the arguments of a TV show check-in without an id.
func showTitleCheckin(extra map[string]any) map[string]any {
	return merge(typeArg(typeShow), showEpisode(extra))
}

var checkinCases = []accountCase{
	{
		tool: toolCheckin, args: movieItem(testMovieSlug), route: "POST " + routeCheckin, status: http.StatusCreated, fixture: "checkin_movie", object: true,
		want: map[string]any{
			keyCheckedIn: true, keyAlreadyCheckedIn: false, "id": 3373536619.0, "watched_at": testWatchedAt,
			"item.movie.title": testMovieTitle, "item.movie.ids.slug": testMovieSlug,
		},
		absent: []string{"sharing", keyExpiresAt, "item.show"},
	},
	{
		tool: toolCheckin, args: showIDCheckin(testShowSlug, nil), route: "POST " + routeCheckin, status: http.StatusCreated, fixture: "checkin_episode", object: true,
		want: map[string]any{
			keyCheckedIn: true, "item.episode.season": 1, "item.episode.number": 1, "item.episode.title": testEpisodeTitle, "item.show.title": testShowTitle,
		},
		absent: []string{"item.movie"},
	},
	{
		tool: toolCancelCheckin, args: map[string]any{}, route: "DELETE " + routeCheckin, status: http.StatusNoContent, object: true,
		want: map[string]any{"canceled": true},
	},
}

func TestCheckinBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, tool string
		args       map[string]any
		want       string
	}{
		{
			name: "show by slug", tool: toolCheckin, args: showIDCheckin(testShowSlug, map[string]any{argMessage: "hi"}),
			want: `{"show":{"ids":{"slug":"breaking-bad"}},"episode":{"season":1,"number":1},"message":"hi"}`,
		},
		{
			name: "show by imdb", tool: toolCheckin, args: showIDCheckin(testShowIMDb, nil),
			want: `{"show":{"ids":{"imdb":"tt0903747"}},"episode":{"season":1,"number":1}}`,
		},
		{
			name: "show by title", tool: toolCheckin, args: showTitleCheckin(map[string]any{argShowTitle: "  " + testShowTitle + " ", argShowYear: testShowYear}),
			want: `{"show":{"title":"Breaking Bad","year":2008},"episode":{"season":1,"number":1}}`,
		},
		{
			name: "specials", tool: toolCheckin, args: merge(showItem("1388"), map[string]any{fieldSeason: 0, fieldEpisode: 3}),
			want: `{"show":{"ids":{"trakt":1388}},"episode":{"season":0,"number":3}}`,
		},
		{
			name: "movie", tool: toolCheckin, args: movieItem(testMovieTraktID),
			want: `{"movie":{"ids":{"trakt":481}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			log := captureBodies(h, "POST "+routeCheckin, http.StatusCreated, emptyObject)
			h.ok(tc.tool, tc.args, nil)

			if got := log.all(); len(got) != 1 || got[0] != tc.want {
				t.Errorf("body = %q, want %s", got, tc.want)
			}
		})
	}
}

func TestCheckinArguments(t *testing.T) {
	t.Parallel()

	maxYear := time.Now().Year() + maxShowYearAhead
	route := "POST " + routeCheckin
	byTitle := func(title string, year int) map[string]any {
		return showTitleCheckin(map[string]any{argShowTitle: title, argShowYear: year})
	}
	withMovie := func(extra map[string]any) map[string]any { return merge(movieItem(testMovieSlug), extra) }
	noSeason := merge(showItem(testShowSlug), map[string]any{fieldEpisode: 1})
	noEpisode := merge(showItem(testShowSlug), map[string]any{fieldSeason: 1})

	cases := []argCase{
		{name: "id and title", tool: toolCheckin, args: showIDCheckin(testShowSlug, map[string]any{argShowTitle: testShowTitle, argShowYear: testShowYear}), wantErr: invalid(fieldID)},
		{name: "id and year", tool: toolCheckin, args: showIDCheckin(testShowSlug, map[string]any{argShowYear: testShowYear}), wantErr: invalid(fieldID)},
		{name: "no show", tool: toolCheckin, args: showTitleCheckin(nil), wantErr: invalid(fieldID)},
		{name: "bad id", tool: toolCheckin, args: showIDCheckin(traversalID, nil), wantErr: invalid(fieldID)},
		{name: "title without year", tool: toolCheckin, args: showTitleCheckin(map[string]any{argShowTitle: testShowTitle}), wantErr: invalid(fieldShowYear)},
		{name: "year without title", tool: toolCheckin, args: showTitleCheckin(map[string]any{argShowYear: testShowYear}), wantErr: invalid(fieldShowTitle)},
		{name: "blank title", tool: toolCheckin, args: byTitle("   ", testShowYear), wantErr: invalid(fieldShowTitle)},
		{name: "long title", tool: toolCheckin, args: byTitle(strings.Repeat("a", maxShowTitleRunes+1), testShowYear), wantErr: invalid(fieldShowTitle)},
		{name: "longest title", tool: toolCheckin, args: byTitle(strings.Repeat("é", maxShowTitleRunes), testShowYear), route: route, body: emptyObject},
		{name: "year too old", tool: toolCheckin, args: byTitle(testShowTitle, minShowYear-1), wantErr: invalid(fieldShowYear)},
		{name: "oldest year", tool: toolCheckin, args: byTitle(testShowTitle, minShowYear), route: route, body: emptyObject},
		{name: "latest year", tool: toolCheckin, args: byTitle(testShowTitle, maxYear), route: route, body: emptyObject},
		{name: "year too far", tool: toolCheckin, args: byTitle(testShowTitle, maxYear+1), wantErr: invalid(fieldShowYear)},
		{name: "show season -1", tool: toolCheckin, args: merge(showItem(testShowSlug), map[string]any{fieldSeason: -1, fieldEpisode: 1}), wantErr: invalid(fieldSeason)},
		{name: "show season 1000", tool: toolCheckin, args: merge(showItem(testShowSlug), map[string]any{fieldSeason: maxSeason + 1, fieldEpisode: 1}), wantErr: invalid(fieldSeason)},
		{name: "episode 0", tool: toolCheckin, args: merge(showItem(testShowSlug), map[string]any{fieldSeason: 1, fieldEpisode: 0}), wantErr: invalid(fieldEpisode)},
		{name: "episode 10000", tool: toolCheckin, args: merge(showItem(testShowSlug), map[string]any{fieldSeason: 1, fieldEpisode: maxEpisode + 1}), wantErr: invalid(fieldEpisode)},
		{name: "show without season", tool: toolCheckin, args: noSeason, wantErr: invalid(fieldSeason)},
		{name: "show without episode", tool: toolCheckin, args: noEpisode, wantErr: invalid(fieldEpisode)},
		{name: "show without season and episode", tool: toolCheckin, args: showItem(testShowSlug), wantErr: invalid(fieldSeason)},
		{name: "longest message", tool: toolCheckin, args: withMovie(map[string]any{argMessage: strings.Repeat("é", maxCheckinMessageRunes)}), route: route, body: emptyObject},
		{name: "long message", tool: toolCheckin, args: withMovie(map[string]any{argMessage: strings.Repeat("a", maxCheckinMessageRunes+1)}), wantErr: invalid(fieldMessage)},
		{name: "show message", tool: toolCheckin, args: showIDCheckin(testShowSlug, map[string]any{argMessage: strings.Repeat("a", maxCheckinMessageRunes+1)}), wantErr: invalid(fieldMessage)},
		{name: "bad movie", tool: toolCheckin, args: movieItem(personIMDb), wantErr: invalid(fieldID)},
		{name: "movie without id", tool: toolCheckin, args: typeArg(typeMovie), wantErr: invalid(fieldID)},
		{name: "movie with season", tool: toolCheckin, args: withMovie(map[string]any{fieldSeason: 1}), wantErr: invalid(fieldSeason)},
		{name: "movie with season 0", tool: toolCheckin, args: withMovie(map[string]any{fieldSeason: 0}), wantErr: invalid(fieldSeason)},
		{name: "movie with episode", tool: toolCheckin, args: withMovie(map[string]any{fieldEpisode: 1}), wantErr: invalid(fieldEpisode)},
		{name: "movie with show title", tool: toolCheckin, args: withMovie(map[string]any{argShowTitle: testShowTitle}), wantErr: invalid(fieldShowTitle)},
		{name: "movie with show year", tool: toolCheckin, args: withMovie(map[string]any{argShowYear: testShowYear}), wantErr: invalid(fieldShowYear)},
	}

	for _, typ := range []string{itemPerson, typeSeason, typeEpisode, "", typeUpperCase, typeUpperCaseShow} {
		cases = append(cases, argCase{name: "type " + typ, tool: toolCheckin, args: merge(showItem(testShowSlug), merge(showEpisode(nil), typeArg(typ))), wantErr: invalid(fieldType)})
	}

	runArgCases(t, cases)
}

func TestCheckinConflictReturnsActiveCheckin(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, body string
		want       map[string]any
		absent     []string
	}{
		{
			name: "movie", body: `{"watched_at": "2024-03-01T19:00:00.000Z", "expires_at": "` + testExpiresAt + `",
				"movie": {"title": "The Matrix", "year": 1999, "ids": {"trakt": 481, "slug": "the-matrix-1999"}}}`,
			want: map[string]any{keyCheckedIn: false, keyAlreadyCheckedIn: true, keyExpiresAt: testExpiresAt, "item.movie.title": testMovieTitle},
		},
		{
			name: "episode with offset", body: `{"expires_at": "2024-03-01T22:00:00+01:00",
				"show": {"title": "Breaking Bad", "year": 2008, "ids": {"trakt": 1388, "slug": "breaking-bad"}},
				"episode": {"season": 1, "number": 2, "ids": {"trakt": 73483}}}`,
			want: map[string]any{keyAlreadyCheckedIn: true, keyExpiresAt: testExpiresAt, "item.show.title": testShowTitle, "item.episode.number": 2},
		},
		{
			name: "expiry only", body: `{"expires_at": "` + testExpiresAt + `"}`,
			want: map[string]any{keyCheckedIn: false, keyAlreadyCheckedIn: true, keyExpiresAt: testExpiresAt}, absent: []string{"item"},
		},
		{
			name: "item of the wrong shape", body: `{"expires_at": "` + testExpiresAt + `", "movie": "the matrix"}`,
			want: map[string]any{keyAlreadyCheckedIn: true, keyExpiresAt: testExpiresAt}, absent: []string{"item"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			for _, tool := range []struct {
				name string
				args map[string]any
			}{{toolCheckin, movieItem(testMovieSlug)}, {toolCheckin, showIDCheckin(testShowSlug, nil)}} {
				h := newHarness(t)
				h.trakt.respond("POST "+routeCheckin, http.StatusConflict, tc.body, nil)

				var out any

				res := h.ok(tool.name, tool.args, &out)

				for path, want := range tc.want {
					if got, ok := lookup(out, path); !ok || jsonText(t, got) != jsonText(t, want) {
						t.Errorf("%s: %s = %v (present %v), want %v in %s", tool.name, path, got, ok, want, resultText(res))
					}
				}

				for _, path := range tc.absent {
					if _, ok := lookup(out, path); ok {
						t.Errorf("%s: %s present in %s", tool.name, path, resultText(res))
					}
				}

				if got := len(h.trakt.seen()); got != 1 {
					t.Errorf("%s: Trakt requests = %d, want 1", tool.name, got)
				}
			}
		})
	}
}

func jsonText(t *testing.T, v any) string {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestCheckinConflictBodyNeverEchoed(t *testing.T) {
	t.Parallel()

	errorBodies := map[string]string{
		"not json":       injectionMarker + ` {"expires_at": "` + testExpiresAt + `"}`,
		"no expiry":      `{"message": "` + injectionMarker + `"}`,
		"garbage expiry": `{"expires_at": "` + injectionMarker + `"}`,
		"empty":          ``,
		"cut":            `{"expires_at": "2024-03-01T21:00`,
	}

	for name, body := range errorBodies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			// Debug logs arguments and results: the most a log line holds.
			h := newHarness(t, withLogLevel(slog.LevelDebug))
			h.trakt.respond("POST "+routeCheckin, http.StatusConflict, body, nil)
			h.fail(toolCheckin, movieItem(testMovieSlug), msgCheckinActive)

			if strings.Contains(h.logs.String(), injectionMarker) {
				t.Error("conflict body reached the logs")
			}
		})
	}

	t.Run("extra fields and hostile ids", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("POST "+routeCheckin, http.StatusConflict, `{"expires_at": "`+testExpiresAt+`",
			"note": "`+injectionMarker+`", "error_description": "`+injectionMarker+`",
			"movie": {"title": "The Matrix", "year": 1999, "ids": {"trakt": 481, "slug": "`+injectionMarker+` x", "imdb": "`+injectionMarker+`"}}}`, nil)

		res := h.ok(toolCheckin, movieItem(testMovieSlug), nil)

		if strings.Contains(resultText(res), injectionMarker) {
			t.Errorf("conflict body echoed: %s", resultText(res))
		}

		if strings.Contains(h.logs.String(), injectionMarker) {
			t.Error("conflict body reached the logs")
		}
	})

	t.Run("long title bounded", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t)
		h.trakt.respond("POST "+routeCheckin, http.StatusConflict, `{"expires_at": "`+testExpiresAt+`",
			"movie": {"title": "`+strings.Repeat("x", maxReplyTitleProbe)+`", "ids": {"trakt": 481}}}`, nil)

		var out CheckinResult

		h.ok(toolCheckin, movieItem(testMovieSlug), &out)

		if out.Item == nil || out.Item.Movie == nil || len([]rune(out.Item.Movie.Title)) > maxTitleRunes {
			t.Errorf("item = %+v, want a title of at most %d runes", out.Item, maxTitleRunes)
		}
	})
}

// maxReplyTitleProbe is a title far over maxTitleRunes.
const maxReplyTitleProbe = 5000

// TestCheckinNotFound requires a Trakt 404 to give the not-found tool error
// for a movie and for a TV show.
func TestCheckinNotFound(t *testing.T) {
	t.Parallel()

	for typ, args := range map[string]map[string]any{
		typeMovie: movieItem(testMovieSlug),
		typeShow:  showIDCheckin(testShowSlug, nil),
	} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond("POST "+routeCheckin, http.StatusNotFound, emptyObject, nil)
			h.fail(toolCheckin, args, msgNotFound)
		})
	}
}

// TestCheckinRoutesByType requires a movie to send a movie body and a TV show
// to send a show body.
func TestCheckinRoutesByType(t *testing.T) {
	t.Parallel()

	for typ, tc := range map[string]struct {
		args   map[string]any
		prefix string
	}{
		typeMovie: {args: movieItem(testMovieSlug), prefix: `{"movie":`},
		typeShow:  {args: showIDCheckin(testShowSlug, nil), prefix: `{"show":`},
	} {
		t.Run(typ, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			log := captureBodies(h, "POST "+routeCheckin, http.StatusCreated, emptyObject)
			h.ok(toolCheckin, tc.args, nil)

			if got := log.all(); len(got) != 1 || !strings.HasPrefix(got[0], tc.prefix) {
				t.Errorf("body = %q, want it to start with %s", got, tc.prefix)
			}
		})
	}
}

// TestLegacyCheckinToolsRemoved requires the per-type check-in tools to be gone.
func TestLegacyCheckinToolsRemoved(t *testing.T) {
	t.Parallel()

	requireToolsRemoved(t, "checkin_to_movie", "checkin_to_show")
}
