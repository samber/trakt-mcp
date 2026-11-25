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
	Scope     string `json:"scope,omitempty"      jsonschema:"my (the user's own TV shows and movies) or all (everything airing); default my"`
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

// Values of the kind argument of list_calendar: the calendar names a user's
// request maps to, one per Trakt calendar endpoint.
const (
	calendarKindShows     = mediaShows
	calendarKindNewShows  = "new_shows"
	calendarKindPremieres = "premieres"
	calendarKindFinales   = "finales"
	calendarKindMovies    = "movies"
	calendarKindDVD       = "dvd"
	calendarKindStreaming = "streaming"
)

// ListCalendarArgs are the arguments of list_calendar.
type ListCalendarArgs struct {
	Kind string `json:"kind" jsonschema:"which calendar: shows, new_shows, premieres, finales, movies, dvd or streaming; the tool description explains each"`
	CalendarArgs
}

// calendarHandlers lists the handler of every kind of list_calendar.
var calendarHandlers = []kindHandler[CalendarArgs, Listing[CalendarEntry]]{
	{kind: calendarKindShows, call: fetchCalendarShows},
	{kind: calendarKindNewShows, call: fetchCalendarNewShows},
	{kind: calendarKindPremieres, call: fetchCalendarShowPremieres},
	{kind: calendarKindFinales, call: fetchCalendarShowFinales},
	{kind: calendarKindMovies, call: fetchCalendarMovies},
	{kind: calendarKindDVD, call: fetchCalendarDVD},
	{kind: calendarKindStreaming, call: fetchCalendarStreaming},
}

func registerCalendarTools(s *Server) {
	Register(s, Spec{
		Name: "list_calendar", Title: "List calendar", Access: Read,
		Description: "List what is airing or being released in a date window. kind picks the calendar: " +
			"shows is the TV show episodes airing, each with its episode and TV show; " +
			"new_shows is brand-new TV shows whose first season starts; " +
			"premieres is TV show season premieres; " +
			"finales is TV show season and series finales; " +
			"movies is the movies with a release date; " +
			"dvd is the movies released on DVD or other physical media; " +
			"streaming is the movies released on streaming services. " +
			"Scope my is the user's own TV shows and movies, all is every TV show and movie." + calendarCapNote,
	}, listCalendar)
}

func listCalendar(ctx context.Context, c *trakt.UserClient, in ListCalendarArgs) (Listing[CalendarEntry], error) {
	return callKind(ctx, c, in.Kind, calendarHandlers, in.CalendarArgs)
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
