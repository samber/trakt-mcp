package tools

import (
	"context"
	"net/http"
	"regexp"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// minSeason is 0, Trakt's number for specials; maxSeason is far above any
	// real show and keeps the path value short.
	minSeason = 0
	maxSeason = 999
	// maxWatchingUsers bounds the list of users currently watching.
	maxWatchingUsers = 50
	// languagePattern is an ISO 639-1 code, which Trakt takes lower-case.
	languagePattern = `^[a-z]{2}$`
)

// Kinds of list Trakt can return for a season or an episode, and the sorts
// it supports on them.
const (
	listTypeAll       = "all"
	listTypePersonal  = "personal"
	listTypeOfficial  = "official"
	listTypeWatchlist = "watchlist"
	listTypeFavorites = "favorites"
	defaultListType   = listTypeAll

	listSortPopular  = "popular"
	listSortLikes    = "likes"
	listSortComments = "comments"
	listSortItems    = "items"
	listSortAdded    = "added"
	listSortUpdated  = "updated"
	defaultListSort  = listSortPopular
)

var (
	listTypes = []string{listTypeAll, listTypePersonal, listTypeOfficial, listTypeWatchlist, listTypeFavorites}
	listSorts = []string{listSortPopular, listSortLikes, listSortComments, listSortItems, listSortAdded, listSortUpdated}

	languageRE = regexp.MustCompile(languagePattern)
)

// Stats are the counters of a season or an episode.
type Stats struct {
	Watchers   int `json:"watchers"`
	Plays      int `json:"plays"`
	Collectors int `json:"collectors"`
	Comments   int `json:"comments"`
	Lists      int `json:"lists"`
	Votes      int `json:"votes"`
}

func (Stats) resultCount() int { return 1 }

// Translation is a title's text in one language.
type Translation struct {
	Language string `json:"language"`
	Country  string `json:"country,omitempty"`
	Title    string `json:"title,omitempty"`
	Overview string `json:"overview,omitempty"`
}

// WatchingUser is a user currently watching a title.
type WatchingUser struct {
	Username string `json:"username"`
	Slug     string `json:"slug,omitempty"`
}

// Watching lists the users watching a title now. Count includes private and
// deleted accounts, which Users leaves out.
type Watching struct {
	Count int            `json:"count"`
	Users []WatchingUser `json:"users"`
}

func (Watching) resultCount() int { return 1 }

// ListIDs are the identifiers of a Trakt list.
type ListIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
}

// TraktList is a user-made list containing a title.
type TraktList struct {
	Name         string  `json:"name"`
	Description  string  `json:"description,omitempty"`
	IDs          ListIDs `json:"ids"`
	Owner        string  `json:"owner,omitempty"`
	ItemCount    int     `json:"item_count"`
	Likes        int     `json:"likes"`
	CommentCount int     `json:"comment_count"`
	UpdatedAt    string  `json:"updated_at,omitempty"`
}

func checkSeason(n int) error {
	if n < minSeason || n > maxSeason {
		return invalidArgument(fieldSeason)
	}

	return nil
}

// SeasonRef names a season of a show; season 0 is the specials.
type SeasonRef struct {
	ShowRef
	Season int `json:"season" jsonschema:"season number, 0 to 999; 0 is the specials"`
}

func (r SeasonRef) path() (string, int, error) {
	id, err := r.ShowRef.path()
	if err != nil {
		return "", 0, err
	}

	return id, r.Season, checkSeason(r.Season)
}

// languageArg validates an optional ISO 639-1 code; empty means all
// languages and gives nil.
func languageArg(lang string) (*string, error) {
	if lang == "" {
		return nil, nil //nolint:nilnil // nil means "all languages"
	}

	if !languageRE.MatchString(lang) {
		return nil, invalidArgument(fieldLanguage)
	}

	return &lang, nil
}

type traktTranslation struct {
	Language string  `json:"language"`
	Country  *string `json:"country"`
	Title    *string `json:"title"`
	Overview *string `json:"overview"`
}

func trimTranslation(t traktTranslation) Translation {
	return Translation{
		Language: t.Language,
		Country:  deref(t.Country),
		Title:    boundText(deref(t.Title), maxTitleRunes),
		Overview: boundText(deref(t.Overview), maxOverviewRunes),
	}
}

// statsOf trims a Trakt stats body.
func statsOf(body []byte) (Stats, error) {
	return decodeAs[Stats](body)
}

// watchingOf trims a Trakt watching body.
func watchingOf(body []byte) (Watching, error) {
	raw, err := decodeAs[[]struct {
		Username string `json:"username"`
		Private  bool   `json:"private"`
		Deleted  bool   `json:"deleted"`
		IDs      struct {
			Slug *string `json:"slug"`
		} `json:"ids"`
	}](body)
	if err != nil {
		return Watching{}, err
	}

	out := Watching{Count: len(raw), Users: []WatchingUser{}}

	for _, u := range raw {
		if u.Private || u.Deleted || len(out.Users) == maxWatchingUsers {
			continue
		}

		out.Users = append(out.Users, WatchingUser{Username: u.Username, Slug: deref(u.IDs.Slug)})
	}

	return out, nil
}

type traktList struct {
	Name         string  `json:"name"`
	Description  *string `json:"description"`
	ItemCount    int     `json:"item_count"`
	Likes        int     `json:"likes"`
	CommentCount int     `json:"comment_count"`
	UpdatedAt    string  `json:"updated_at"`
	IDs          struct {
		Trakt int    `json:"trakt"`
		Slug  string `json:"slug"`
	} `json:"ids"`
	User struct {
		Username string `json:"username"`
	} `json:"user"`
}

func trimList(l traktList) TraktList {
	return TraktList{
		Name:         boundText(l.Name, maxTitleRunes),
		Description:  boundText(deref(l.Description), maxListOverviewRunes),
		IDs:          ListIDs{Trakt: l.IDs.Trakt, Slug: l.IDs.Slug},
		Owner:        l.User.Username,
		ItemCount:    l.ItemCount,
		Likes:        l.Likes,
		CommentCount: l.CommentCount,
		UpdatedAt:    l.UpdatedAt,
	}
}

// Episode is an episode of a show.
type Episode struct {
	Season      int        `json:"season"`
	Number      int        `json:"number"`
	Title       string     `json:"title,omitempty"`
	Overview    string     `json:"overview,omitempty"`
	Rating      float64    `json:"rating,omitempty"`
	Votes       int        `json:"votes,omitempty"`
	Runtime     int        `json:"runtime,omitempty"      jsonschema:"minutes"`
	FirstAired  string     `json:"first_aired,omitempty"`
	EpisodeType string     `json:"episode_type,omitempty" jsonschema:"for example standard, series_premiere, season_finale"`
	IDs         EpisodeIDs `json:"ids"`
}

func (Episode) resultCount() int { return 1 }

// EpisodeIDs are the identifiers of an episode.
type EpisodeIDs struct {
	Trakt int    `json:"trakt"`
	IMDb  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
	TVDB  int    `json:"tvdb,omitempty"`
}

type traktEpisode struct {
	Season      int      `json:"season"`
	Number      int      `json:"number"`
	Title       *string  `json:"title"`
	Overview    *string  `json:"overview"`
	Rating      *float64 `json:"rating"`
	Votes       *int     `json:"votes"`
	Runtime     *int     `json:"runtime"`
	FirstAired  *string  `json:"first_aired"`
	EpisodeType *string  `json:"episode_type"`
	IDs         struct {
		Trakt int     `json:"trakt"`
		IMDb  *string `json:"imdb"`
		TMDB  *int    `json:"tmdb"`
		TVDB  *int    `json:"tvdb"`
	} `json:"ids"`
}

func (e traktEpisode) trim(maxOverview int) Episode {
	return Episode{
		Season:      e.Season,
		Number:      e.Number,
		Title:       boundText(deref(e.Title), maxTitleRunes),
		Overview:    boundText(deref(e.Overview), maxOverview),
		Rating:      deref(e.Rating),
		Votes:       deref(e.Votes),
		Runtime:     deref(e.Runtime),
		FirstAired:  deref(e.FirstAired),
		EpisodeType: deref(e.EpisodeType),
		IDs:         EpisodeIDs{Trakt: e.IDs.Trakt, IMDb: deref(e.IDs.IMDb), TMDB: deref(e.IDs.TMDB), TVDB: deref(e.IDs.TVDB)},
	}
}

func trimEpisode(e traktEpisode) Episode { return e.trim(maxListOverviewRunes) }

// fetchParsed makes a generated-client call and parses its body.
func fetchParsed[T any](call func() (*http.Response, error), parse func([]byte) (T, error)) (T, error) {
	r, err := readReply(call)
	if err != nil {
		var zero T

		return zero, err
	}

	return parse(r.body)
}

func parseSeason(body []byte) (Season, error) {
	raw, err := decodeAs[traktSeason](body)

	return raw.trim(maxOverviewRunes), err
}

func parseEpisodes(body []byte) (Listing[Episode], error) { return unpaged(body, trimEpisode) }

func parsePeople(body []byte) (People, error) { return peopleOf(body, false) }

func parseVideos(body []byte) (Listing[Video], error) { return videosListing(body, true) }

func parseTranslations(body []byte) (Listing[Translation], error) {
	return unpaged(body, trimTranslation)
}

// SeasonTranslationsArgs are the arguments of fetch_season_translations.
type SeasonTranslationsArgs struct {
	SeasonRef
	Language string `json:"language,omitempty" jsonschema:"two-letter ISO 639-1 code such as fr; omit it for every language"`
}

// SeasonListsArgs are the arguments of fetch_season_lists.
type SeasonListsArgs struct {
	PageArgs
	SeasonRef
	ListType string `json:"list_type,omitempty" jsonschema:"all, personal, official, watchlist or favorites (default all)"`
	Sort     string `json:"sort,omitempty"      jsonschema:"popular, likes, comments, items, added or updated (default popular)"`
}

func registerSeasonTools(s *Server) {
	Register(s, Spec{
		Name: "fetch_season_info", Title: "Fetch season info", Access: Read,
		Description: "Get one TV show season's details: number, title, overview, rating, episode count and first air date. Season 0 is the specials.",
	}, fetchSeasonInfo)
	Register(s, Spec{
		Name: "fetch_season_episodes", Title: "Fetch season episodes", Access: Read,
		Description: "List the episodes of a TV show season with number, title, overview, rating, runtime and air date.",
	}, fetchSeasonEpisodes)
	Register(s, Spec{
		Name: "fetch_season_ratings", Title: "Fetch season ratings", Access: Read,
		Description: "Get a TV show season's average rating, vote count and the number of votes for each rating from 1 to 10.",
	}, fetchSeasonRatings)
	Register(s, Spec{
		Name: "fetch_season_stats", Title: "Fetch season stats", Access: Read,
		Description: "Get a TV show season's counters: watchers, plays, collectors, comments, lists and votes.",
	}, fetchSeasonStats)
	Register(s, Spec{
		Name: "fetch_season_people", Title: "Fetch season people", Access: Read,
		Description: "Get a TV show season's cast (name, character) and crew grouped by department. Long credit lists are cut.",
	}, fetchSeasonPeople)
	Register(s, Spec{
		Name: "fetch_season_videos", Title: "Fetch season videos", Access: Read,
		Description: "List a TV show season's trailers and other videos with title, type, site, url, publication date and a ready markdown link.",
	}, fetchSeasonVideos)
	Register(s, Spec{
		Name: "fetch_season_watching", Title: "Fetch season watchers", Access: Read,
		Description: "Count the users watching a TV show season right now and list their usernames, up to 50. Private accounts are counted, not listed.",
	}, fetchSeasonWatching)
	Register(s, Spec{
		Name: "fetch_season_translations", Title: "Fetch season translations", Access: Read,
		Description: "Get a TV show season's title and overview in one language (two-letter code) or in every language.",
	}, fetchSeasonTranslations)
	Register(s, Spec{
		Name: "fetch_season_lists", Title: "Fetch lists with a season", Access: Read,
		Description: "List the Trakt lists that contain a TV show season, by list type and sort. Takes limit and page." + untrustedNote,
	}, fetchSeasonLists)
}

func fetchSeasonInfo(ctx context.Context, c *trakt.UserClient, in SeasonRef) (Season, error) {
	id, season, err := in.path()
	if err != nil {
		return Season{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonInfo(ctx, id, season, &api.GetShowsSeasonInfoParams{Extended: extendedFullPtr()})
	}, parseSeason)
}

func fetchSeasonEpisodes(ctx context.Context, c *trakt.UserClient, in SeasonRef) (Listing[Episode], error) {
	id, season, err := in.path()
	if err != nil {
		return Listing[Episode]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonEpisodes(ctx, id, season, &api.GetShowsSeasonEpisodesParams{Extended: extendedFullPtr()})
	}, parseEpisodes)
}

func fetchSeasonRatings(ctx context.Context, c *trakt.UserClient, in SeasonRef) (Rating, error) {
	id, season, err := in.path()
	if err != nil {
		return Rating{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonRatings(ctx, id, season, &api.GetShowsSeasonRatingsParams{})
	}, ratingOf)
}

func fetchSeasonStats(ctx context.Context, c *trakt.UserClient, in SeasonRef) (Stats, error) {
	id, season, err := in.path()
	if err != nil {
		return Stats{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonStats(ctx, id, season)
	}, statsOf)
}

func fetchSeasonPeople(ctx context.Context, c *trakt.UserClient, in SeasonRef) (People, error) {
	id, season, err := in.path()
	if err != nil {
		return People{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonPeople(ctx, id, season, &api.GetShowsSeasonPeopleParams{})
	}, parsePeople)
}

func fetchSeasonVideos(ctx context.Context, c *trakt.UserClient, in SeasonRef) (Listing[Video], error) {
	id, season, err := in.path()
	if err != nil {
		return Listing[Video]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonVideos(ctx, id, season)
	}, parseVideos)
}

func fetchSeasonWatching(ctx context.Context, c *trakt.UserClient, in SeasonRef) (Watching, error) {
	id, season, err := in.path()
	if err != nil {
		return Watching{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonWatching(ctx, id, season, &api.GetShowsSeasonWatchingParams{})
	}, watchingOf)
}

func fetchSeasonTranslations(ctx context.Context, c *trakt.UserClient, in SeasonTranslationsArgs) (Listing[Translation], error) {
	id, season, err := in.path()
	if err != nil {
		return Listing[Translation]{}, err
	}

	lang, err := languageArg(in.Language)
	if err != nil {
		return Listing[Translation]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetShowsSeasonTranslations(ctx, id, season, &api.GetShowsSeasonTranslationsParams{Language: lang})
	}, parseTranslations)
}

func fetchSeasonLists(ctx context.Context, c *trakt.UserClient, in SeasonListsArgs) (Listing[TraktList], error) {
	id, season, err := in.path()
	if err != nil {
		return Listing[TraktList]{}, err
	}

	listType, sortBy, err := listArgs(in.ListType, in.Sort)
	if err != nil {
		return Listing[TraktList]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimList, func(page, limit int) (*http.Response, error) {
		return c.API().GetShowsSeasonLists(ctx, id, season, api.GetShowsSeasonListsParamsType(listType), api.GetShowsSeasonListsParamsSort(sortBy),
			&api.GetShowsSeasonListsParams{Page: &page, Limit: &limit})
	})
}

// listArgs validates the list type and sort arguments of the lists tools,
// applying their defaults.
func listArgs(listType, sortBy string) (string, string, error) {
	lt, err := oneOf(fieldListType, listType, defaultListType, listTypes)
	if err != nil {
		return "", "", err
	}

	sb, err := oneOf(fieldSort, sortBy, defaultListSort, listSorts)

	return lt, sb, err
}
