package tools

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// Argument names of the calendar tools.
const (
	fieldScope     = "scope"
	fieldStartDate = "start_date"
	fieldDays      = "days"
)

const (
	// calendarScopeMy is the signed-in user's own calendar; calendarScopeAll
	// is the global one. They are the two values Trakt accepts as target.
	calendarScopeMy  = "my"
	calendarScopeAll = "all"

	// defaultCalendarDays is Trakt's own default window: one week.
	defaultCalendarDays = 7
	// maxCalendarDays is the longest window Trakt documents for calendars.
	maxCalendarDays = 33
	// minCalendarDays is 1: a window needs at least one day.
	minCalendarDays = 1

	// maxCalendarEntries caps the entries a calendar tool returns. Trakt does
	// not paginate calendars; with scope all and a 33-day window the answer is
	// huge, so this is this server's own limit to protect the model's context,
	// not a Trakt limit.
	maxCalendarEntries = 300
)

// calendarCapNote is appended to every calendar tool description so the model
// knows a long window is cut.
var calendarCapNote = " Long windows on scope all are cut at " + strconv.Itoa(maxCalendarEntries) + " entries (this server's cap, not Trakt's): narrow days."

// calendarScopes lists the accepted values of the scope argument.
var calendarScopes = []string{calendarScopeMy, calendarScopeAll}

// CalendarArgs are the arguments shared by every calendar tool.
type CalendarArgs struct {
	Scope     string `json:"scope,omitempty"      jsonschema:"my (the user's own shows and movies) or all (everything airing); default my"`
	StartDate string `json:"start_date,omitempty" jsonschema:"first day of the window, YYYY-MM-DD in UTC; default today"`
	// Days is nullable: nil means the default window, while 0 is rejected.
	Days *int `json:"days,omitempty" jsonschema:"number of days from 1 to 33; default 7"`
}

// calendarWindow is CalendarArgs after defaults and validation.
type calendarWindow struct {
	scope     string
	startDate string
	days      int
}

func (a CalendarArgs) window() (calendarWindow, error) {
	scope, err := oneOf(fieldScope, a.Scope, calendarScopeMy, calendarScopes)
	if err != nil {
		return calendarWindow{}, err
	}

	start := a.StartDate
	if start == "" {
		start = time.Now().UTC().Format(time.DateOnly)
	} else if _, perr := time.Parse(time.DateOnly, start); perr != nil {
		return calendarWindow{}, invalidArgument(fieldStartDate)
	}

	days := defaultCalendarDays
	if a.Days != nil {
		days = *a.Days
	}

	if days < minCalendarDays || days > maxCalendarDays {
		return calendarWindow{}, invalidArgument(fieldDays)
	}

	return calendarWindow{scope: scope, startDate: start, days: days}, nil
}

// CalendarEntry is one dated entry of a calendar. A TV show entry carries an
// episode and its show; a movie entry carries a movie.
type CalendarEntry struct {
	FirstAired string   `json:"first_aired,omitempty" jsonschema:"air date and time of the episode (TV show calendars)"`
	Released   string   `json:"released,omitempty"    jsonschema:"release date (movie calendars)"`
	Episode    *Episode `json:"episode,omitempty"`
	Show       *Show    `json:"show,omitempty"`
	Movie      *Movie   `json:"movie,omitempty"`
}

type traktCalendarEntry struct {
	FirstAired *string       `json:"first_aired"`
	Released   *string       `json:"released"`
	Episode    *traktEpisode `json:"episode"`
	Show       *traktShow    `json:"show"`
	Movie      *traktMovie   `json:"movie"`
}

func trimCalendarEntry(e traktCalendarEntry) CalendarEntry {
	out := CalendarEntry{FirstAired: deref(e.FirstAired), Released: deref(e.Released)}

	if e.Episode != nil {
		ep := trimEpisode(*e.Episode)
		out.Episode = &ep
	}

	if e.Show != nil {
		show := e.Show.trim(maxListOverviewRunes)
		out.Show = &show
	}

	if e.Movie != nil {
		movie := e.Movie.trim(maxListOverviewRunes)
		out.Movie = &movie
	}

	return out
}

func parseCalendar(body []byte) (Listing[CalendarEntry], error) {
	out, err := unpaged(body, trimCalendarEntry)
	if err != nil {
		return out, err
	}

	out.Items = out.Items[:min(len(out.Items), maxCalendarEntries)]

	return out, nil
}

// fetchCalendar validates the window, then runs call with it.
func fetchCalendar(in CalendarArgs, call func(w calendarWindow) (*http.Response, error)) (Listing[CalendarEntry], error) {
	w, err := in.window()
	if err != nil {
		return Listing[CalendarEntry]{}, err
	}

	return fetchParsed(func() (*http.Response, error) { return call(w) }, parseCalendar)
}

func registerCalendarTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_calendar_shows", Title: "Fetch TV show calendar", Access: Read,
		Description: "List the TV series episodes airing in a date window, each with its episode and TV show. Scope my is the user's own TV shows, all is every TV show." + calendarCapNote,
	}, fetchCalendarShows)
	Register(s, Spec{
		Name: "fetch_calendar_new_shows", Title: "Fetch new TV shows calendar", Access: Read,
		Description: "List brand-new TV series whose first season starts in a date window. Scope my is the user's own TV shows, all is every TV show." + calendarCapNote,
	}, fetchCalendarNewShows)
	Register(s, Spec{
		Name: "fetch_calendar_show_premieres", Title: "Fetch TV season premieres calendar", Access: Read,
		Description: "List TV series season premieres airing in a date window. Scope my is the user's own TV shows, all is every TV show." + calendarCapNote,
	}, fetchCalendarShowPremieres)
	Register(s, Spec{
		Name: "fetch_calendar_show_finales", Title: "Fetch TV season finales calendar", Access: Read,
		Description: "List TV series season and series finales airing in a date window. Scope my is the user's own TV shows, all is every TV show." + calendarCapNote,
	}, fetchCalendarShowFinales)
	Register(s, Spec{
		Name: "fetch_calendar_movies", Title: "Fetch movie calendar", Access: Read,
		Description: "List the movies with a release date in a date window. Scope my is the user's own movies, all is every movie." + calendarCapNote,
	}, fetchCalendarMovies)
	Register(s, Spec{
		Name: "fetch_calendar_dvd", Title: "Fetch DVD and Blu-ray calendar", Access: Read,
		Description: "List the movies released on DVD or other physical media in a date window. Scope my is the user's own movies, all is every movie." + calendarCapNote,
	}, fetchCalendarDVD)
	Register(s, Spec{
		Name: "fetch_calendar_streaming", Title: "Fetch streaming releases calendar", Access: Read,
		Description: "List the movies released on streaming services in a date window. Scope my is the user's own movies, all is every movie." + calendarCapNote,
	}, fetchCalendarStreaming)
}

func fetchCalendarShows(ctx context.Context, c *trakt.UserClient, in CalendarArgs) (Listing[CalendarEntry], error) {
	return fetchCalendar(in, func(w calendarWindow) (*http.Response, error) {
		return c.API().GetCalendarsShows(ctx, api.GetCalendarsShowsParamsTarget(w.scope), w.startDate, w.days, &api.GetCalendarsShowsParams{Extended: extendedFullPtr()})
	})
}

func fetchCalendarNewShows(ctx context.Context, c *trakt.UserClient, in CalendarArgs) (Listing[CalendarEntry], error) {
	return fetchCalendar(in, func(w calendarWindow) (*http.Response, error) {
		return c.API().GetCalendarsNewShows(ctx, api.GetCalendarsNewShowsParamsTarget(w.scope), w.startDate, w.days, &api.GetCalendarsNewShowsParams{Extended: extendedFullPtr()})
	})
}

func fetchCalendarShowPremieres(ctx context.Context, c *trakt.UserClient, in CalendarArgs) (Listing[CalendarEntry], error) {
	return fetchCalendar(in, func(w calendarWindow) (*http.Response, error) {
		return c.API().GetCalendarsSeasonPremieres(ctx, api.GetCalendarsSeasonPremieresParamsTarget(w.scope), w.startDate, w.days, &api.GetCalendarsSeasonPremieresParams{Extended: extendedFullPtr()})
	})
}

func fetchCalendarShowFinales(ctx context.Context, c *trakt.UserClient, in CalendarArgs) (Listing[CalendarEntry], error) {
	return fetchCalendar(in, func(w calendarWindow) (*http.Response, error) {
		return c.API().GetCalendarsFinales(ctx, api.GetCalendarsFinalesParamsTarget(w.scope), w.startDate, w.days, &api.GetCalendarsFinalesParams{Extended: extendedFullPtr()})
	})
}

func fetchCalendarMovies(ctx context.Context, c *trakt.UserClient, in CalendarArgs) (Listing[CalendarEntry], error) {
	return fetchCalendar(in, func(w calendarWindow) (*http.Response, error) {
		return c.API().GetCalendarsMovies(ctx, api.GetCalendarsMoviesParamsTarget(w.scope), w.startDate, w.days, &api.GetCalendarsMoviesParams{Extended: extendedFullPtr()})
	})
}

func fetchCalendarDVD(ctx context.Context, c *trakt.UserClient, in CalendarArgs) (Listing[CalendarEntry], error) {
	return fetchCalendar(in, func(w calendarWindow) (*http.Response, error) {
		return c.API().GetCalendarsDvdReleases(ctx, api.GetCalendarsDvdReleasesParamsTarget(w.scope), w.startDate, w.days, &api.GetCalendarsDvdReleasesParams{Extended: extendedFullPtr()})
	})
}

func fetchCalendarStreaming(ctx context.Context, c *trakt.UserClient, in CalendarArgs) (Listing[CalendarEntry], error) {
	return fetchCalendar(in, func(w calendarWindow) (*http.Response, error) {
		return c.API().GetCalendarsStreaming(ctx, api.GetCalendarsStreamingParamsTarget(w.scope), w.startDate, w.days, &api.GetCalendarsStreamingParams{Extended: extendedFullPtr()})
	})
}
