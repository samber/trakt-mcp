package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// Argument names, as they appear in tool errors.
const (
	fieldShowID   = "show_id"
	fieldPeriod   = "period"
	fieldQuery    = "query"
	fieldSeason   = "season"
	fieldEpisode  = "episode"
	fieldLanguage = "language"
	fieldListType = "list_type"
	fieldSort     = "sort"
)

// Ranking periods Trakt accepts for favorited, played and watched shows.
const (
	periodDaily   = "daily"
	periodWeekly  = "weekly"
	periodMonthly = "monthly"
	periodYearly  = "yearly"
	periodAll     = "all"
	// defaultPeriod is a week: recent enough to be topical, long enough to be
	// stable.
	defaultPeriod = periodWeekly
)

// Bounds on identifiers and sizes.
const (
	// maxQueryRunes bounds a search query: titles are short, and a bound keeps
	// a hostile argument from reaching Trakt.
	maxQueryRunes = 200
	// maxCastMembers and maxGuestStars bound the people lists: long-running
	// shows have hundreds of credits, which would flood the model's context.
	maxCastMembers = 50
	maxGuestStars  = 50
	// maxCrewPerDepartment bounds each crew group for the same reason.
	maxCrewPerDepartment = 20
	// maxTitleRunes bounds free-text titles copied from Trakt (video and
	// translation titles).
	maxTitleRunes = 200
	// ratingBuckets is the number of rating values, 1 to 10.
	ratingBuckets = 10
	// schemeHTTP and schemeHTTPS are the only video URL schemes returned.
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

var periods = []string{periodDaily, periodWeekly, periodMonthly, periodYearly, periodAll}

// ShowIDs are the identifiers of a show.
type ShowIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
	IMDb  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
	TVDB  int    `json:"tvdb,omitempty"`
}

// Show is the trimmed show the show tools return. Fields Trakt did not send
// are omitted; Genres is always an array.
type Show struct {
	Title         string   `json:"title"`
	Year          int      `json:"year,omitempty"`
	IDs           ShowIDs  `json:"ids"`
	Rating        float64  `json:"rating,omitempty"`
	Votes         int      `json:"votes,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	Runtime       int      `json:"runtime,omitempty"        jsonschema:"minutes per episode"`
	Certification string   `json:"certification,omitempty"`
	Network       string   `json:"network,omitempty"`
	Status        string   `json:"status,omitempty"`
	FirstAired    string   `json:"first_aired,omitempty"`
	AiredEpisodes int      `json:"aired_episodes,omitempty"`
	Genres        []string `json:"genres"`
}

func (Show) resultCount() int { return 1 }

// TrendingShow is a show with the number of people watching it now.
type TrendingShow struct {
	Watchers int  `json:"watchers"`
	Show     Show `json:"show"`
}

// AnticipatedShow is a show with the number of lists it appears on.
type AnticipatedShow struct {
	ListCount int  `json:"list_count"`
	Show      Show `json:"show"`
}

// FavoritedShow is a show with the number of users who favorited it.
type FavoritedShow struct {
	UserCount int  `json:"user_count"`
	Show      Show `json:"show"`
}

// PlayedShow is a show with its play statistics for a period.
type PlayedShow struct {
	WatcherCount   int  `json:"watcher_count"`
	PlayCount      int  `json:"play_count"`
	CollectedCount int  `json:"collected_count"`
	CollectorCount int  `json:"collector_count"`
	Show           Show `json:"show"`
}

// SearchedShow is one match of search_shows.
type SearchedShow struct {
	Score float64 `json:"score"`
	Title string  `json:"title"`
	Year  int     `json:"year,omitempty"`
	IDs   ShowIDs `json:"ids"`
}

// Rating is the rating of a show, season or episode: the average, the vote
// count and the votes per rating value.
type Rating struct {
	Rating float64 `json:"rating"`
	Votes  int     `json:"votes"`
	// Distribution holds the number of votes for the ratings 1 to 10, in that
	// order; it is empty when Trakt sent none.
	Distribution []int `json:"distribution"`
}

func (Rating) resultCount() int { return 1 }

// Video is a trailer, clip or other video of a title.
type Video struct {
	Title       string `json:"title"`
	Type        string `json:"type"`
	Site        string `json:"site"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at,omitempty"`
	// Markdown is a ready link, "[title](url)", present when requested.
	Markdown string `json:"markdown,omitempty"`
}

// PersonIDs are the identifiers of a person.
type PersonIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
	IMDb  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
}

// CastMember is an actor and the character played.
type CastMember struct {
	Name         string    `json:"name"`
	Character    string    `json:"character,omitempty"`
	EpisodeCount int       `json:"episode_count,omitempty"`
	IDs          PersonIDs `json:"ids"`
}

// CrewMember is a crew person and the job done.
type CrewMember struct {
	Name string    `json:"name"`
	Job  string    `json:"job,omitempty"`
	IDs  PersonIDs `json:"ids"`
}

// CrewGroup is the crew of one department.
type CrewGroup struct {
	Department string       `json:"department"`
	Members    []CrewMember `json:"members"`
}

// People are the credits of a title. GuestStars is present only when it was
// requested.
type People struct {
	Cast       []CastMember  `json:"cast"`
	Crew       []CrewGroup   `json:"crew"`
	GuestStars *[]CastMember `json:"guest_stars,omitempty"`
}

func (People) resultCount() int { return 1 }

// Raw Trakt shapes. Each holds the fields the tools read; the rest of the
// body is dropped.

type traktShowIDs struct {
	Trakt int     `json:"trakt"`
	Slug  string  `json:"slug"`
	IMDb  *string `json:"imdb"`
	TMDB  *int    `json:"tmdb"`
	TVDB  *int    `json:"tvdb"`
}

func (i traktShowIDs) trim() ShowIDs {
	return ShowIDs{Trakt: i.Trakt, Slug: i.Slug, IMDb: deref(i.IMDb), TMDB: deref(i.TMDB), TVDB: deref(i.TVDB)}
}

type traktShow struct {
	Title         string       `json:"title"`
	Year          *int         `json:"year"`
	IDs           traktShowIDs `json:"ids"`
	Rating        *float64     `json:"rating"`
	Votes         *int         `json:"votes"`
	Overview      *string      `json:"overview"`
	Runtime       *int         `json:"runtime"`
	Certification *string      `json:"certification"`
	Network       *string      `json:"network"`
	Status        *string      `json:"status"`
	FirstAired    *string      `json:"first_aired"`
	AiredEpisodes *int         `json:"aired_episodes"`
	Genres        []string     `json:"genres"`
}

func (s traktShow) trim(maxOverview int) Show {
	return Show{
		Title:         s.Title,
		Year:          deref(s.Year),
		IDs:           s.IDs.trim(),
		Rating:        deref(s.Rating),
		Votes:         deref(s.Votes),
		Overview:      boundText(deref(s.Overview), maxOverview),
		Runtime:       deref(s.Runtime),
		Certification: deref(s.Certification),
		Network:       deref(s.Network),
		Status:        deref(s.Status),
		FirstAired:    deref(s.FirstAired),
		AiredEpisodes: deref(s.AiredEpisodes),
		Genres:        NonNil(s.Genres),
	}
}

type traktTrendingShow struct {
	Watchers int       `json:"watchers"`
	Show     traktShow `json:"show"`
}

type traktAnticipatedShow struct {
	ListCount int       `json:"list_count"`
	Show      traktShow `json:"show"`
}

type traktFavoritedShow struct {
	UserCount int       `json:"user_count"`
	Show      traktShow `json:"show"`
}

type traktSearchHit struct {
	Score float64   `json:"score"`
	Show  traktShow `json:"show"`
}

// trimListShow trims a show inside a list, where the overview is short.
func trimListShow(s traktShow) Show { return s.trim(maxListOverviewRunes) }

// Season is a season of a show.
type Season struct {
	Number        int       `json:"number"`
	Title         string    `json:"title,omitempty"`
	Overview      string    `json:"overview,omitempty"`
	Rating        float64   `json:"rating,omitempty"`
	Votes         int       `json:"votes,omitempty"`
	EpisodeCount  int       `json:"episode_count,omitempty"`
	AiredEpisodes int       `json:"aired_episodes,omitempty"`
	FirstAired    string    `json:"first_aired,omitempty"`
	IDs           SeasonIDs `json:"ids"`
}

func (Season) resultCount() int { return 1 }

// SeasonIDs are the identifiers of a season.
type SeasonIDs struct {
	Trakt int `json:"trakt"`
	TMDB  int `json:"tmdb,omitempty"`
	TVDB  int `json:"tvdb,omitempty"`
}

type traktSeason struct {
	Number        int      `json:"number"`
	Title         *string  `json:"title"`
	Overview      *string  `json:"overview"`
	Rating        *float64 `json:"rating"`
	Votes         *int     `json:"votes"`
	EpisodeCount  *int     `json:"episode_count"`
	AiredEpisodes *int     `json:"aired_episodes"`
	FirstAired    *string  `json:"first_aired"`
	IDs           struct {
		Trakt int  `json:"trakt"`
		TMDB  *int `json:"tmdb"`
		TVDB  *int `json:"tvdb"`
	} `json:"ids"`
}

func trimSeason(s traktSeason) Season {
	return Season{
		Number:        s.Number,
		Title:         boundText(deref(s.Title), maxTitleRunes),
		Overview:      boundText(deref(s.Overview), maxListOverviewRunes),
		Rating:        deref(s.Rating),
		Votes:         deref(s.Votes),
		EpisodeCount:  deref(s.EpisodeCount),
		AiredEpisodes: deref(s.AiredEpisodes),
		FirstAired:    deref(s.FirstAired),
		IDs:           SeasonIDs{Trakt: s.IDs.Trakt, TMDB: deref(s.IDs.TMDB), TVDB: deref(s.IDs.TVDB)},
	}
}

// Shared helpers.

// decodeAs parses a successful Trakt body into a T.
func decodeAs[T any](body []byte) (T, error) {
	var v T

	err := decodeBody(body, &v)

	return v, err
}

// reply is the part of a Trakt answer the tools read.
type reply struct {
	body   []byte
	header http.Header
}

// maxReplyBytes bounds a Trakt answer read into memory: the largest catalog
// answers (full credits of a long-running show) stay far below it, and a
// bound keeps a misbehaving upstream from exhausting memory.
const maxReplyBytes = 8 << 20

// errReplyTooLarge is the cause of an answer over maxReplyBytes.
var errReplyTooLarge = errors.New("trakt: response too large")

// readReply makes a generated-client call and reads its answer. Non-2xx
// answers and transport failures arrive as err, an *trakt.Error mapped by
// Kind.
func readReply(call func() (*http.Response, error)) (reply, error) {
	resp, err := call()
	if err != nil {
		return reply{}, err //nolint:wrapcheck // *trakt.Error is mapped by Kind
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes+1))
	if err != nil {
		return reply{}, fmt.Errorf("read trakt response: %w", err)
	}

	if len(body) > maxReplyBytes {
		return reply{}, errReplyTooLarge
	}

	return reply{body: body, header: resp.Header}, nil
}

// paginateDecoded runs a paginated Trakt call through Paginate: call makes
// the request for one page, the body is decoded as a list of R and each is
// trimmed into a T.
func paginateDecoded[R, T any](ctx context.Context, args PageArgs, trim func(R) T, call func(page, limit int) (*http.Response, error)) (Listing[T], error) {
	return Paginate(ctx, args, func(page, limit int) ([]T, trakt.Page, error) {
		r, err := readReply(func() (*http.Response, error) { return call(page, limit) })
		if err != nil {
			return nil, trakt.Page{}, err
		}

		items, err := trimAll(r.body, trim)
		if err != nil {
			return nil, trakt.Page{}, err
		}

		return items, trakt.ParsePage(r.header), nil
	})
}

// trimAll decodes a body holding a JSON array of R and trims each element.
// A null or missing array gives an empty, non-nil slice.
func trimAll[R, T any](body []byte, trim func(R) T) ([]T, error) {
	raw, err := decodeAs[[]R](body)
	if err != nil {
		return nil, err
	}

	out := make([]T, 0, len(raw))
	for _, r := range raw {
		out = append(out, trim(r))
	}

	return out, nil
}

// unpaged returns the items of a Trakt endpoint that is not paginated.
func unpaged[R, T any](body []byte, trim func(R) T) (Listing[T], error) {
	items, err := trimAll(body, trim)

	return Listing[T]{Items: items}, err
}

// extendedFullPtr is the "extended=full" query value, as the generated
// client takes it.
func extendedFullPtr() *string {
	e := extendedFull

	return &e
}

// showPath validates the show argument and returns it as a path segment.
func showPath(raw string) (string, error) {
	id, err := idArg(fieldShowID, raw, Slug|Numeric|IMDbTitle)

	return string(id), err
}

// ShowRef names a show.
type ShowRef struct {
	ShowID string `json:"show_id" jsonschema:"Trakt slug (breaking-bad), numeric Trakt id (1388) or IMDb id (tt0903747)"`
}

func (r ShowRef) path() (string, error) { return showPath(r.ShowID) }

// oneOf returns the argument value, or def when empty; it fails with an
// invalid-argument error when the value is not in allowed.
func oneOf(field, value, def string, allowed []string) (string, error) {
	if value == "" {
		return def, nil
	}

	if !slices.Contains(allowed, value) {
		return "", invalidArgument(field)
	}

	return value, nil
}

type traktVideo struct {
	Title       string `json:"title"`
	Type        string `json:"type"`
	Site        string `json:"site"`
	URL         string `json:"url"`
	PublishedAt string `json:"published_at"`
}

// videosListing turns a Trakt videos body into a Listing. Items whose URL is
// not http or https are dropped: they cannot be opened and must not become
// markdown links.
func videosListing(body []byte, embedMarkdown bool) (Listing[Video], error) {
	raw, err := decodeAs[[]traktVideo](body)
	if err != nil {
		return Listing[Video]{}, err
	}

	out := make([]Video, 0, len(raw))

	for _, v := range raw {
		u, perr := url.Parse(v.URL)
		if perr != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) {
			continue
		}

		video := Video{Title: boundText(v.Title, maxTitleRunes), Type: v.Type, Site: v.Site, URL: u.String(), PublishedAt: v.PublishedAt}
		if embedMarkdown {
			video.Markdown = "[" + markdownEscaper.Replace(video.Title) + "](" + markdownURLEscaper.Replace(video.URL) + ")"
		}

		out = append(out, video)
	}

	return Listing[Video]{Items: out}, nil
}

var (
	// markdownEscaper escapes the characters that would end a link text.
	markdownEscaper = strings.NewReplacer(`\`, `\\`, `[`, `\[`, `]`, `\]`)
	// markdownURLEscaper escapes the characters that would end a link target.
	markdownURLEscaper = strings.NewReplacer(" ", "%20", "(", "%28", ")", "%29")
)

// ratingOf trims a Trakt ratings body.
func ratingOf(body []byte) (Rating, error) {
	raw, err := decodeAs[struct {
		Rating       *float64           `json:"rating"`
		Votes        *int               `json:"votes"`
		Distribution map[string]float64 `json:"distribution"`
	}](body)
	if err != nil {
		return Rating{}, err
	}

	dist := []int{}

	if raw.Distribution != nil {
		for i := 1; i <= ratingBuckets; i++ {
			dist = append(dist, int(math.Round(raw.Distribution[strconv.Itoa(i)])))
		}
	}

	return Rating{Rating: deref(raw.Rating), Votes: deref(raw.Votes), Distribution: dist}, nil
}

type traktCredit struct {
	Character    *string `json:"character"`
	Job          *string `json:"job"`
	EpisodeCount *int    `json:"episode_count"`
	Person       struct {
		Name string `json:"name"`
		IDs  struct {
			Trakt int     `json:"trakt"`
			Slug  string  `json:"slug"`
			IMDb  *string `json:"imdb"`
			TMDB  *int    `json:"tmdb"`
		} `json:"ids"`
	} `json:"person"`
}

func (c traktCredit) personIDs() PersonIDs {
	return PersonIDs{Trakt: c.Person.IDs.Trakt, Slug: c.Person.IDs.Slug, IMDb: deref(c.Person.IDs.IMDb), TMDB: deref(c.Person.IDs.TMDB)}
}

func castOf(credits []traktCredit, limit int) []CastMember {
	out := make([]CastMember, 0, min(len(credits), limit))

	for _, c := range credits {
		if len(out) == limit {
			break
		}

		out = append(out, CastMember{
			Name:         boundText(c.Person.Name, maxTitleRunes),
			Character:    boundText(deref(c.Character), maxTitleRunes),
			EpisodeCount: deref(c.EpisodeCount),
			IDs:          c.personIDs(),
		})
	}

	return out
}

// peopleOf trims a Trakt people body. Crew is grouped by department, sorted
// by name so the output is stable.
func peopleOf(body []byte, guestStars bool) (People, error) {
	raw, err := decodeAs[struct {
		Cast       []traktCredit            `json:"cast"`
		Crew       map[string][]traktCredit `json:"crew"`
		GuestStars []traktCredit            `json:"guest_stars"`
	}](body)
	if err != nil {
		return People{}, err
	}

	out := People{Cast: castOf(raw.Cast, maxCastMembers), Crew: make([]CrewGroup, 0, len(raw.Crew))}

	departments := make([]string, 0, len(raw.Crew))
	for d := range raw.Crew {
		departments = append(departments, d)
	}

	sort.Strings(departments)

	for _, d := range departments {
		group := CrewGroup{Department: d, Members: make([]CrewMember, 0, min(len(raw.Crew[d]), maxCrewPerDepartment))}

		for _, c := range raw.Crew[d] {
			if len(group.Members) == maxCrewPerDepartment {
				break
			}

			group.Members = append(group.Members, CrewMember{Name: boundText(c.Person.Name, maxTitleRunes), Job: boundText(deref(c.Job), maxTitleRunes), IDs: c.personIDs()})
		}

		out.Crew = append(out.Crew, group)
	}

	if guestStars {
		guests := castOf(raw.GuestStars, maxGuestStars)
		out.GuestStars = &guests
	}

	return out, nil
}

// Tool arguments.

// ShowListArgs are the arguments of the unfiltered show rankings.
type ShowListArgs struct {
	PageArgs
}

// PeriodShowArgs are the arguments of the period-based show rankings.
type PeriodShowArgs struct {
	PageArgs
	Period string `json:"period,omitempty" jsonschema:"daily, weekly, monthly, yearly or all (default weekly)"`
}

// SearchShowsArgs are the arguments of search_shows.
type SearchShowsArgs struct {
	PageArgs
	Query string `json:"query" jsonschema:"text to search for in show titles and descriptions, up to 200 characters"`
}

// ShowSummaryArgs are the arguments of fetch_show_summary.
type ShowSummaryArgs struct {
	ShowRef
	Extended *bool `json:"extended,omitempty" jsonschema:"include overview, rating, runtime, certification, genres, network and status (default true)"`
}

// VideosArgs are the arguments of fetch_show_videos.
type VideosArgs struct {
	ShowRef
	EmbedMarkdown *bool `json:"embed_markdown,omitempty" jsonschema:"also return each video as a ready markdown link (default true)"`
}

// RelatedShowsArgs are the arguments of fetch_related_shows.
type RelatedShowsArgs struct {
	PageArgs
	ShowRef
}

// ShowPeopleArgs are the arguments of fetch_show_people.
type ShowPeopleArgs struct {
	ShowRef
	IncludeGuestStars bool `json:"include_guest_stars,omitempty" jsonschema:"also return guest stars (default false)"`
}

func registerShowTools(s *Server) {
	const pageNote = " Takes limit and page."

	Register(s, Spec{
		Name: "fetch_trending_shows", Title: "Fetch trending shows", Access: Read,
		Description: "List the shows most watched on Trakt right now, with how many people are watching each." + pageNote,
	}, fetchTrendingShows)
	Register(s, Spec{
		Name: "fetch_popular_shows", Title: "Fetch popular shows", Access: Read,
		Description: "List the most popular shows on Trakt, by rating and votes." + pageNote,
	}, fetchPopularShows)
	Register(s, Spec{
		Name: "fetch_anticipated_shows", Title: "Fetch anticipated shows", Access: Read,
		Description: "List the most anticipated upcoming shows, with the number of lists each is on." + pageNote,
	}, fetchAnticipatedShows)
	Register(s, Spec{
		Name: "fetch_favorited_shows", Title: "Fetch favorited shows", Access: Read,
		Description: "List the shows favorited by the most users in a period (daily, weekly, monthly, yearly, all)." + pageNote,
	}, fetchFavoritedShows)
	Register(s, Spec{
		Name: "fetch_played_shows", Title: "Fetch most played shows", Access: Read,
		Description: "List the shows with the most plays in a period (daily, weekly, monthly, yearly, all), with watcher and play counts." + pageNote,
	}, fetchPlayedShows)
	Register(s, Spec{
		Name: "fetch_watched_shows", Title: "Fetch most watched shows", Access: Read,
		Description: "List the shows watched by the most users in a period (daily, weekly, monthly, yearly, all), with watcher and play counts." + pageNote,
	}, fetchWatchedShows)
	Register(s, Spec{
		Name: "search_shows", Title: "Search shows", Access: Read,
		Description: "Search shows by text. Returns each match's relevance score, title, year and ids." + pageNote,
	}, searchShows)
	Register(s, Spec{
		Name: "fetch_show_ratings", Title: "Fetch show ratings", Access: Read,
		Description: "Get a show's average rating, vote count and the number of votes for each rating from 1 to 10.",
	}, fetchShowRatings)
	Register(s, Spec{
		Name: "fetch_show_summary", Title: "Fetch show summary", Access: Read,
		Description: "Get one show's details: year, ids, rating, overview, runtime, certification, network, status, first air date and genres.",
	}, fetchShowSummary)
	Register(s, Spec{
		Name: "fetch_show_videos", Title: "Fetch show videos", Access: Read,
		Description: "List a show's trailers and other videos with title, type, site, url, publication date and, by default, a ready markdown link.",
	}, fetchShowVideos)
	Register(s, Spec{
		Name: "fetch_related_shows", Title: "Fetch related shows", Access: Read,
		Description: "List shows related to a show." + pageNote,
	}, fetchRelatedShows)
	Register(s, Spec{
		Name: "fetch_show_seasons", Title: "Fetch show seasons", Access: Read,
		Description: "List a show's seasons with number, title, episode count, rating and first air date. Season 0 is the specials.",
	}, fetchShowSeasons)
	Register(s, Spec{
		Name: "fetch_show_people", Title: "Fetch show people", Access: Read,
		Description: "Get a show's cast (name, character) and crew grouped by department, optionally with guest stars. Long credit lists are cut.",
	}, fetchShowPeople)
}

// Handlers.

func fetchTrendingShows(ctx context.Context, c *trakt.UserClient, in ShowListArgs) (Listing[TrendingShow], error) {
	return paginateDecoded(ctx, in.PageArgs, trimTrendingShow, func(page, limit int) (*http.Response, error) {
		return c.API().GetShowsTrending(ctx, &api.GetShowsTrendingParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func trimTrendingShow(r traktTrendingShow) TrendingShow {
	return TrendingShow{Watchers: r.Watchers, Show: trimListShow(r.Show)}
}

func fetchPopularShows(ctx context.Context, c *trakt.UserClient, in ShowListArgs) (Listing[Show], error) {
	return paginateDecoded(ctx, in.PageArgs, trimListShow, func(page, limit int) (*http.Response, error) {
		return c.API().GetShowsPopular(ctx, &api.GetShowsPopularParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func fetchAnticipatedShows(ctx context.Context, c *trakt.UserClient, in ShowListArgs) (Listing[AnticipatedShow], error) {
	return paginateDecoded(ctx, in.PageArgs, trimAnticipatedShow, func(page, limit int) (*http.Response, error) {
		return c.API().GetShowsAnticipated(ctx, &api.GetShowsAnticipatedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func trimAnticipatedShow(r traktAnticipatedShow) AnticipatedShow {
	return AnticipatedShow{ListCount: r.ListCount, Show: trimListShow(r.Show)}
}

func fetchFavoritedShows(ctx context.Context, c *trakt.UserClient, in PeriodShowArgs) (Listing[FavoritedShow], error) {
	return periodShows(ctx, in, trimFavoritedShow, func(period string, page, limit int) (*http.Response, error) {
		return c.API().GetShowsFavorited(ctx, api.GetShowsFavoritedParamsPeriod(period), &api.GetShowsFavoritedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

// periodShows validates the period of a ranking and paginates it.
func periodShows[R, T any](ctx context.Context, in PeriodShowArgs, trim func(R) T, call func(period string, page, limit int) (*http.Response, error)) (Listing[T], error) {
	period, err := oneOf(fieldPeriod, in.Period, defaultPeriod, periods)
	if err != nil {
		return Listing[T]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trim, func(page, limit int) (*http.Response, error) {
		return call(period, page, limit)
	})
}

func trimFavoritedShow(r traktFavoritedShow) FavoritedShow {
	return FavoritedShow{UserCount: r.UserCount, Show: trimListShow(r.Show)}
}

type traktPlayedShow struct {
	WatcherCount   int       `json:"watcher_count"`
	PlayCount      int       `json:"play_count"`
	CollectedCount int       `json:"collected_count"`
	CollectorCount int       `json:"collector_count"`
	Show           traktShow `json:"show"`
}

func trimPlayedShow(r traktPlayedShow) PlayedShow {
	return PlayedShow{WatcherCount: r.WatcherCount, PlayCount: r.PlayCount, CollectedCount: r.CollectedCount, CollectorCount: r.CollectorCount, Show: trimListShow(r.Show)}
}

func fetchPlayedShows(ctx context.Context, c *trakt.UserClient, in PeriodShowArgs) (Listing[PlayedShow], error) {
	return periodShows(ctx, in, trimPlayedShow, func(period string, page, limit int) (*http.Response, error) {
		return c.API().GetShowsPlayed(ctx, api.GetShowsPlayedParamsPeriod(period), &api.GetShowsPlayedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func fetchWatchedShows(ctx context.Context, c *trakt.UserClient, in PeriodShowArgs) (Listing[PlayedShow], error) {
	return periodShows(ctx, in, trimPlayedShow, func(period string, page, limit int) (*http.Response, error) {
		return c.API().GetShowsWatched(ctx, api.GetShowsWatchedParamsPeriod(period), &api.GetShowsWatchedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func searchShows(ctx context.Context, c *trakt.UserClient, in SearchShowsArgs) (Listing[SearchedShow], error) {
	query := strings.TrimSpace(in.Query)
	if query == "" || utf8.RuneCountInString(query) > maxQueryRunes {
		return Listing[SearchedShow]{}, invalidArgument(fieldQuery)
	}

	return paginateDecoded(ctx, in.PageArgs, trimSearchHit, func(page, limit int) (*http.Response, error) {
		return c.API().GetSearchQuery(ctx, api.GetSearchQueryParamsTypeShow, &api.GetSearchQueryParams{Query: &query, Page: &page, Limit: &limit})
	})
}

func trimSearchHit(r traktSearchHit) SearchedShow {
	return SearchedShow{Score: r.Score, Title: r.Show.Title, Year: deref(r.Show.Year), IDs: r.Show.IDs.trim()}
}

func fetchShowRatings(ctx context.Context, c *trakt.UserClient, in ShowRef) (Rating, error) {
	id, err := in.path()
	if err != nil {
		return Rating{}, err
	}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetShowsRatings(ctx, id, &api.GetShowsRatingsParams{}) })
	if err != nil {
		return Rating{}, err
	}

	return ratingOf(r.body)
}

func fetchShowSummary(ctx context.Context, c *trakt.UserClient, in ShowSummaryArgs) (Show, error) {
	id, err := in.path()
	if err != nil {
		return Show{}, err
	}

	params := &api.GetShowsSummaryParams{}
	if in.Extended == nil || *in.Extended {
		params.Extended = extendedFullPtr()
	}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetShowsSummary(ctx, id, params) })
	if err != nil {
		return Show{}, err
	}

	raw, err := decodeAs[traktShow](r.body)
	if err != nil {
		return Show{}, err
	}

	return raw.trim(maxOverviewRunes), nil
}

// embedByDefault is true unless the caller passed false.
func embedByDefault(p *bool) bool { return p == nil || *p }

func fetchShowVideos(ctx context.Context, c *trakt.UserClient, in VideosArgs) (Listing[Video], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Video]{}, err
	}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetShowsVideos(ctx, id) })
	if err != nil {
		return Listing[Video]{}, err
	}

	return videosListing(r.body, embedByDefault(in.EmbedMarkdown))
}

func fetchRelatedShows(ctx context.Context, c *trakt.UserClient, in RelatedShowsArgs) (Listing[Show], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Show]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimListShow, func(page, limit int) (*http.Response, error) {
		return c.API().GetShowsRelated(ctx, id, &api.GetShowsRelatedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func fetchShowSeasons(ctx context.Context, c *trakt.UserClient, in ShowRef) (Listing[Season], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Season]{}, err
	}

	r, err := readReply(func() (*http.Response, error) {
		return c.API().GetShowsSeasons(ctx, id, &api.GetShowsSeasonsParams{Extended: extendedFullPtr()})
	})
	if err != nil {
		return Listing[Season]{}, err
	}

	return unpaged(r.body, trimSeason)
}

func fetchShowPeople(ctx context.Context, c *trakt.UserClient, in ShowPeopleArgs) (People, error) {
	id, err := in.path()
	if err != nil {
		return People{}, err
	}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetShowsPeople(ctx, id, &api.GetShowsPeopleParams{}) })
	if err != nil {
		return People{}, err
	}

	return peopleOf(r.body, in.IncludeGuestStars)
}
