package tools

import (
	"testing"
	"time"
)

const (
	toolCalendarShows     = "fetch_calendar_shows"
	toolCalendarNewShows  = "fetch_calendar_new_shows"
	toolCalendarPremieres = "fetch_calendar_show_premieres"
	toolCalendarFinales   = "fetch_calendar_show_finales"
	toolCalendarMovies    = "fetch_calendar_movies"
	toolCalendarDVD       = "fetch_calendar_dvd"
	toolCalendarStreaming = "fetch_calendar_streaming"

	testCalendarStart = "2026-11-24"
	testCalendarDays  = 7

	// testCalendarMaxDays is the largest window Trakt serves for calendars.
	testCalendarMaxDays = 33
)

func init() { catalogCaseSets = append(catalogCaseSets, calendarCases) }

// calendarArgs asks for 7 days from 2026-11-24 on the user's own calendar.
func calendarArgs(extra map[string]any) map[string]any {
	return merge(map[string]any{fieldScope: "my", fieldStartDate: testCalendarStart, fieldDays: testCalendarDays}, extra)
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
		tool: toolCalendarShows, args: calendarArgs(nil), route: calendarRoute("shows"), fixture: "calendar_shows",
		wantQuery: map[string]string{queryExtended: extendedFull}, want: calendarShowWant, absent: []string{"items.0.movie", "items.0.released"},
	},
	{tool: toolCalendarNewShows, args: calendarArgs(nil), route: calendarRoute("shows/new"), fixture: "calendar_new_shows", want: calendarShowWant},
	{tool: toolCalendarPremieres, args: calendarArgs(nil), route: calendarRoute("shows/premieres"), fixture: "calendar_premieres", want: calendarShowWant},
	{tool: toolCalendarFinales, args: calendarArgs(nil), route: calendarRoute("shows/finales"), fixture: "calendar_finales", want: calendarShowWant},
	{
		tool: toolCalendarMovies, args: calendarArgs(nil), route: calendarRoute("movies"), fixture: "calendar_movies",
		wantQuery: map[string]string{queryExtended: extendedFull}, want: calendarMovieWant, absent: []string{"items.0.show", "items.0.episode", "items.0.first_aired"},
	},
	{tool: toolCalendarDVD, args: calendarArgs(nil), route: calendarRoute("dvd"), fixture: "calendar_dvd", want: calendarMovieWant},
	{tool: toolCalendarStreaming, args: calendarArgs(nil), route: calendarRoute("streaming"), fixture: "calendar_streaming", want: calendarMovieWant},
}

func TestCalendarToolArguments(t *testing.T) {
	t.Parallel()

	today := time.Now().UTC().Format(time.DateOnly)

	cases := []argCase{
		{name: "scope x", tool: toolCalendarShows, args: calendarArgs(map[string]any{fieldScope: "x"}), wantErr: invalid(fieldScope)},
		{name: "scope all", tool: toolCalendarMovies, args: calendarArgs(map[string]any{fieldScope: "all"}), route: "GET /calendars/all/movies/" + testCalendarStart + "/7", body: emptyList},
		{name: "start date month 13", tool: toolCalendarShows, args: calendarArgs(map[string]any{fieldStartDate: "2026-13-01"}), wantErr: invalid(fieldStartDate)},
		{name: "start date day first", tool: toolCalendarDVD, args: calendarArgs(map[string]any{fieldStartDate: "24-11-2026"}), wantErr: invalid(fieldStartDate)},
		{name: "start date impossible day", tool: toolCalendarFinales, args: calendarArgs(map[string]any{fieldStartDate: "2026-02-30"}), wantErr: invalid(fieldStartDate)},
		{name: "days 0", tool: toolCalendarNewShows, args: calendarArgs(map[string]any{fieldDays: 0}), wantErr: invalid(fieldDays)},
		{name: "days -1", tool: toolCalendarStreaming, args: calendarArgs(map[string]any{fieldDays: -1}), wantErr: invalid(fieldDays)},
		{name: "days 1", tool: toolCalendarShows, args: calendarArgs(map[string]any{fieldDays: 1}), route: "GET /calendars/my/shows/" + testCalendarStart + "/1", body: emptyList},
		{name: "days max", tool: toolCalendarShows, args: calendarArgs(map[string]any{fieldDays: testCalendarMaxDays}), route: "GET /calendars/my/shows/" + testCalendarStart + "/33", body: emptyList},
		{name: "days max plus one", tool: toolCalendarShows, args: calendarArgs(map[string]any{fieldDays: testCalendarMaxDays + 1}), wantErr: invalid(fieldDays)},
		{
			name: "defaults are my scope, today and 7 days", tool: toolCalendarPremieres, args: map[string]any{},
			route: "GET /calendars/my/shows/premieres/" + today + "/7", body: emptyList,
		},
	}

	runArgCases(t, cases)
}
