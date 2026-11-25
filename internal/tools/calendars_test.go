package tools

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

const (
	toolCalendar = "list_calendar"

	testCalendarStart = "2026-11-24"
	testCalendarDays  = 7

	// testCalendarMaxDays is the largest window Trakt serves for calendars.
	testCalendarMaxDays = 33
)

func init() { catalogCaseSets = append(catalogCaseSets, calendarCases) }

// calendarArgs asks for the calendar kind, 7 days from 2026-11-24 on the user's own calendar.
func calendarArgs(kind string, extra map[string]any) map[string]any {
	return merge(map[string]any{fieldKind: kind, fieldScope: "my", fieldStartDate: testCalendarStart, fieldDays: testCalendarDays}, extra)
}

func calendarRoute(kind string) string {
	return "GET /calendars/my/" + kind + "/" + testCalendarStart + "/7"
}

var calendarShowWant = map[string]any{
	"items.0.episode.season": 2, "items.0.episode.number": 1, "items.0.episode.first_aired": "2026-11-24T02:00:00.000Z",
	"items.0.episode.title": "Seven Thirty-Seven", "items.0.episode.episode_type": "season_premiere", "items.0.episode.ids.trakt": 62119,
	pathFirstShowTitle: testShowTitle, "items.0.show.ids.slug": testShowSlug, "items.0.show.genres": []any{"drama", "crime"},
	"items.1.episode.number": 8, "items.1.show.ids.slug": "better-call-saul", pathSecondShowGenres: []any{},
}

var calendarMovieWant = map[string]any{
	"items.0.released": "2026-11-27", "items.0.movie.title": "The Matrix", "items.0.movie.year": 1999,
	"items.0.movie.ids.slug": testMovieSlug, "items.0.movie.ids.imdb": testMovieIMDb, "items.0.movie.genres": []any{"scifi", "thriller"},
	"items.1.movie.title": "Untitled Sequel", "items.1.movie.genres": []any{}, "items.1.released": "2026-11-28",
}

var calendarCases = []catalogCase{
	{
		tool: toolCalendar, args: calendarArgs(calendarKindShows, nil), route: calendarRoute("shows"), fixture: "calendar_shows",
		wantQuery: map[string]string{queryExtended: extendedFull}, want: calendarShowWant, absent: []string{"items.0.movie", "items.0.released"},
	},
	{tool: toolCalendar, args: calendarArgs(calendarKindNewShows, nil), route: calendarRoute("shows/new"), fixture: "calendar_new_shows", want: calendarShowWant},
	{tool: toolCalendar, args: calendarArgs(calendarKindPremieres, nil), route: calendarRoute("shows/premieres"), fixture: "calendar_premieres", want: calendarShowWant},
	{tool: toolCalendar, args: calendarArgs(calendarKindFinales, nil), route: calendarRoute("shows/finales"), fixture: "calendar_finales", want: calendarShowWant},
	{
		tool: toolCalendar, args: calendarArgs(calendarKindMovies, nil), route: calendarRoute("movies"), fixture: "calendar_movies",
		wantQuery: map[string]string{queryExtended: extendedFull}, want: calendarMovieWant, absent: []string{"items.0.show", "items.0.episode", "items.0.first_aired"},
	},
	{tool: toolCalendar, args: calendarArgs(calendarKindDVD, nil), route: calendarRoute("dvd"), fixture: "calendar_dvd", want: calendarMovieWant},
	{tool: toolCalendar, args: calendarArgs(calendarKindStreaming, nil), route: calendarRoute("streaming"), fixture: "calendar_streaming", want: calendarMovieWant},
}

func TestCalendarToolArguments(t *testing.T) {
	t.Parallel()

	today := time.Now().UTC().Format(time.DateOnly)

	cases := []argCase{
		{name: "scope x", tool: toolCalendar, args: calendarArgs(calendarKindShows, map[string]any{fieldScope: "x"}), wantErr: invalid(fieldScope)},
		{name: "scope all", tool: toolCalendar, args: calendarArgs(calendarKindMovies, map[string]any{fieldScope: "all"}), route: "GET /calendars/all/movies/" + testCalendarStart + "/7", body: emptyList},
		{name: "start date month 13", tool: toolCalendar, args: calendarArgs(calendarKindShows, map[string]any{fieldStartDate: "2026-13-01"}), wantErr: invalid(fieldStartDate)},
		{name: "start date day first", tool: toolCalendar, args: calendarArgs(calendarKindDVD, map[string]any{fieldStartDate: "24-11-2026"}), wantErr: invalid(fieldStartDate)},
		{name: "start date impossible day", tool: toolCalendar, args: calendarArgs(calendarKindFinales, map[string]any{fieldStartDate: "2026-02-30"}), wantErr: invalid(fieldStartDate)},
		{name: "days 0", tool: toolCalendar, args: calendarArgs(calendarKindNewShows, map[string]any{fieldDays: 0}), wantErr: invalid(fieldDays)},
		{name: "days -1", tool: toolCalendar, args: calendarArgs(calendarKindStreaming, map[string]any{fieldDays: -1}), wantErr: invalid(fieldDays)},
		{name: "days 1", tool: toolCalendar, args: calendarArgs(calendarKindShows, map[string]any{fieldDays: 1}), route: "GET /calendars/my/shows/" + testCalendarStart + "/1", body: emptyList},
		{name: "days max", tool: toolCalendar, args: calendarArgs(calendarKindShows, map[string]any{fieldDays: testCalendarMaxDays}), route: "GET /calendars/my/shows/" + testCalendarStart + "/33", body: emptyList},
		{name: "days max plus one", tool: toolCalendar, args: calendarArgs(calendarKindShows, map[string]any{fieldDays: testCalendarMaxDays + 1}), wantErr: invalid(fieldDays)},
		{
			name: "defaults are my scope, today and 7 days", tool: toolCalendar, args: map[string]any{fieldKind: calendarKindPremieres},
			route: "GET /calendars/my/shows/premieres/" + today + "/7", body: emptyList,
		},
	}

	runArgCases(t, cases)
}

// calendarKindPaths maps each kind to the Trakt path segment it reads: one
// distinct route per kind.
var calendarKindPaths = map[string]string{
	calendarKindShows: mediaShows, calendarKindNewShows: "shows/new", calendarKindPremieres: "shows/premieres", calendarKindFinales: "shows/finales",
	calendarKindMovies: "movies", calendarKindDVD: "dvd", calendarKindStreaming: "streaming",
}

// TestCalendarRoutesByKind requires each kind to reach its own Trakt endpoint
// with exactly one request.
func TestCalendarRoutesByKind(t *testing.T) {
	t.Parallel()

	for kind, path := range calendarKindPaths {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			h := newHarness(t)
			h.trakt.respond(calendarRoute(path), http.StatusOK, emptyList, nil)

			var out Listing[CalendarEntry]

			h.ok(toolCalendar, calendarArgs(kind, nil), &out)

			seen := h.trakt.seen()
			if len(seen) != 1 || seen[0].URL.Path != calendarRoute(path)[len("GET "):] {
				t.Errorf("requests = %v, want one to %s", seen, calendarRoute(path))
			}
		})
	}
}

// TestCalendarNotFound requires a Trakt 404 to give the not-found tool error.
func TestCalendarNotFound(t *testing.T) {
	t.Parallel()

	for kind := range calendarKindPaths {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			newHarness(t).fail(toolCalendar, calendarArgs(kind, nil), msgNotFound)
		})
	}
}

// TestCalendarRefusesOtherKinds requires an unknown, empty or upper-case kind
// to fail before any Trakt call.
func TestCalendarRefusesOtherKinds(t *testing.T) {
	t.Parallel()

	var cases []argCase

	for _, kind := range []string{"", "x", "Shows", "SHOWS", "show", "new-shows", "season_premieres", itemPerson} {
		cases = append(cases, argCase{name: "kind " + kind, tool: toolCalendar, args: calendarArgs(kind, nil), wantErr: invalid(fieldKind)})
	}

	runArgCases(t, cases)
}

// TestLegacyCalendarToolsRemoved requires the per-calendar tools to be gone.
func TestLegacyCalendarToolsRemoved(t *testing.T) {
	t.Parallel()

	requireToolsRemoved(t, legacyToolPrefix+"calendar_shows", legacyToolPrefix+"calendar_new_shows", legacyToolPrefix+"calendar_show_premieres", legacyToolPrefix+"calendar_show_finales",
		legacyToolPrefix+"calendar_movies", legacyToolPrefix+"calendar_dvd", legacyToolPrefix+"calendar_streaming")
}

// TestCalendarOutputIsCapped requires a window with more than
// maxCalendarEntries entries to be cut, for both response shapes.
func TestCalendarOutputIsCapped(t *testing.T) {
	t.Parallel()

	const (
		showEntry  = `{"first_aired":"2026-11-24T02:00:00.000Z","episode":{"season":1,"number":1,"title":"E","ids":{"trakt":1}},"show":{"title":"S","year":2020,"ids":{"trakt":1,"slug":"s"}}}`
		movieEntry = `{"released":"2026-11-27","movie":{"title":"M","year":2026,"ids":{"trakt":1,"slug":"m"}}}`
	)

	cases := []struct {
		name, kind, path, entry string
	}{
		{"shows", calendarKindShows, "shows", showEntry},
		{mediaMovies, calendarKindMovies, mediaMovies, movieEntry},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			body := "[" + strings.Repeat(tc.entry+",", maxCalendarEntries) + tc.entry + "]"

			h := newHarness(t)
			h.trakt.respond(calendarRoute(tc.path), http.StatusOK, body, nil)

			var out struct {
				Items []map[string]any `json:"items"`
			}

			h.ok(toolCalendar, calendarArgs(tc.kind, nil), &out)

			if len(out.Items) != maxCalendarEntries {
				t.Errorf("items = %d, want the cap %d", len(out.Items), maxCalendarEntries)
			}
		})
	}
}
