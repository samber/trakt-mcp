package tools

import (
	"context"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// extendedFull asks Trakt for every field of an item: without it Trakt
// returns only title, year and ids.
const extendedFull = "full"

// extendedGuestStars asks Trakt to add guest_stars to a show's people, which
// it omits otherwise.
const extendedGuestStars = "guest_stars"

// fieldMovieID names the movie argument in tool errors.
const fieldMovieID = "movie_id"

// MovieIDs are the identifiers of a movie, enough to call any other tool or
// link it elsewhere.
type MovieIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
	IMDb  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
}

// Movie is the trimmed movie every movie tool returns. Fields Trakt did not
// send are omitted; Genres is always an array.
type Movie struct {
	Title         string   `json:"title"`
	Year          int      `json:"year,omitempty"`
	IDs           MovieIDs `json:"ids"`
	Rating        float64  `json:"rating,omitempty"`
	Votes         int      `json:"votes,omitempty"`
	Overview      string   `json:"overview,omitempty"`
	Runtime       int      `json:"runtime,omitempty"     jsonschema:"minutes"`
	Certification string   `json:"certification,omitempty"`
	Genres        []string `json:"genres"`
	Released      string   `json:"released,omitempty"    jsonschema:"release date, YYYY-MM-DD"`
}

func (Movie) resultCount() int { return 1 }

// TrendingMovie is a movie with the number of people watching it now.
type TrendingMovie struct {
	Watchers int   `json:"watchers"`
	Movie    Movie `json:"movie"`
}

// traktMovie is the part of Trakt's movie object the tools read.
type traktMovie struct {
	Title string `json:"title"`
	Year  *int   `json:"year"`
	IDs   struct {
		Trakt int     `json:"trakt"`
		Slug  string  `json:"slug"`
		IMDb  *string `json:"imdb"`
		TMDB  *int    `json:"tmdb"`
	} `json:"ids"`
	Rating        *float64 `json:"rating"`
	Votes         *int     `json:"votes"`
	Overview      *string  `json:"overview"`
	Runtime       *int     `json:"runtime"`
	Certification *string  `json:"certification"`
	Genres        []string `json:"genres"`
	Released      *string  `json:"released"`
}

func (m traktMovie) trimIDs() MovieIDs {
	return MovieIDs{
		Trakt: m.IDs.Trakt,
		Slug:  m.IDs.Slug,
		IMDb:  deref(m.IDs.IMDb),
		TMDB:  deref(m.IDs.TMDB),
	}
}

func (m traktMovie) trim(maxOverview int) Movie {
	return Movie{
		Title:         m.Title,
		Year:          deref(m.Year),
		IDs:           m.trimIDs(),
		Rating:        deref(m.Rating),
		Votes:         deref(m.Votes),
		Overview:      boundText(deref(m.Overview), maxOverview),
		Runtime:       deref(m.Runtime),
		Certification: deref(m.Certification),
		Genres:        NonNil(m.Genres),
		Released:      deref(m.Released),
	}
}

// TrendingMoviesArgs are the arguments of the movie handler of list_trending.
type TrendingMoviesArgs struct {
	PageArgs
}

// MovieSummaryArgs are the arguments of fetchMovieSummary, which serves
// get_summary for type movie.
type MovieSummaryArgs struct {
	MovieID  string `json:"movie_id"           jsonschema:"Trakt slug (the-matrix-1999), numeric Trakt id (481) or IMDb id (tt0133093)"`
	Extended *bool  `json:"extended,omitempty" jsonschema:"include overview, rating, runtime, certification and genres (default true)"`
}

func fetchTrendingMovies(ctx context.Context, c *trakt.UserClient, in TrendingMoviesArgs) (Listing[TrendingMovie], error) {
	trim := func(r trendingMovieEntry) TrendingMovie {
		return TrendingMovie{Watchers: r.Watchers, Movie: r.Movie.trim(maxListOverviewRunes)}
	}

	return paginateDecoded(ctx, in.PageArgs, trim, func(page, limit int) (*http.Response, error) {
		return c.API().GetMoviesTrending(ctx, &api.GetMoviesTrendingParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

// trendingMovieEntry is one element of Trakt's trending movies answer.
type trendingMovieEntry struct {
	Watchers int        `json:"watchers"`
	Movie    traktMovie `json:"movie"`
}

func fetchMovieSummary(ctx context.Context, c *trakt.UserClient, in MovieSummaryArgs) (Movie, error) {
	id, err := idArg(fieldMovieID, in.MovieID, Slug|Numeric|IMDbTitle)
	if err != nil {
		return Movie{}, err
	}

	params := &api.GetMoviesSummaryParams{}

	if in.Extended == nil || *in.Extended {
		params.Extended = extendedFullPtr()
	}

	r, err := readReply(func() (*http.Response, error) { return c.API().GetMoviesSummary(ctx, string(id), params) })
	if err != nil {
		return Movie{}, err
	}

	raw, err := decodeAs[traktMovie](r.body)
	if err != nil {
		return Movie{}, err
	}

	return raw.trim(maxOverviewRunes), nil
}

// trimListMovie trims a movie inside a list, where the overview is short.
func trimListMovie(m traktMovie) Movie { return m.trim(maxListOverviewRunes) }

// AnticipatedMovie is a movie with the number of lists it appears on.
type AnticipatedMovie struct {
	ListCount int   `json:"list_count"`
	Movie     Movie `json:"movie"`
}

// FavoritedMovie is a movie with the number of users who favorited it.
type FavoritedMovie struct {
	UserCount int   `json:"user_count"`
	Movie     Movie `json:"movie"`
}

// PlayedMovie is a movie with its play statistics for a period.
type PlayedMovie struct {
	WatcherCount   int   `json:"watcher_count"`
	PlayCount      int   `json:"play_count"`
	CollectedCount int   `json:"collected_count"`
	CollectorCount int   `json:"collector_count"`
	Movie          Movie `json:"movie"`
}

// BoxOfficeMovie is a movie with its weekend box office revenue.
type BoxOfficeMovie struct {
	Revenue int64 `json:"revenue" jsonschema:"weekend revenue in US dollars"`
	Movie   Movie `json:"movie"`
}

// SearchedMovie is one match of search for type movie.
type SearchedMovie struct {
	Score float64  `json:"score"`
	Title string   `json:"title"`
	Year  int      `json:"year,omitempty"`
	IDs   MovieIDs `json:"ids"`
}

type traktAnticipatedMovie struct {
	ListCount int        `json:"list_count"`
	Movie     traktMovie `json:"movie"`
}

type traktFavoritedMovie struct {
	UserCount int        `json:"user_count"`
	Movie     traktMovie `json:"movie"`
}

type traktPlayedMovie struct {
	WatcherCount   int        `json:"watcher_count"`
	PlayCount      int        `json:"play_count"`
	CollectedCount int        `json:"collected_count"`
	CollectorCount int        `json:"collector_count"`
	Movie          traktMovie `json:"movie"`
}

type traktBoxOfficeMovie struct {
	Revenue int64      `json:"revenue"`
	Movie   traktMovie `json:"movie"`
}

type traktMovieHit struct {
	Score float64    `json:"score"`
	Movie traktMovie `json:"movie"`
}

// MovieRef names a movie.
type MovieRef struct {
	MovieID string `json:"movie_id" jsonschema:"Trakt slug (the-matrix-1999), numeric Trakt id (481) or IMDb id (tt0133093)"`
}

func (r MovieRef) path() (string, error) {
	id, err := idArg(fieldMovieID, r.MovieID, Slug|Numeric|IMDbTitle)

	return string(id), err
}

// MovieListArgs are the arguments of the unfiltered movie rankings.
type MovieListArgs struct {
	PageArgs
}

// PeriodMovieArgs are the arguments of the period-based movie rankings.
type PeriodMovieArgs = PeriodShowArgs

// SearchMoviesArgs are the arguments of the movie text search of search.
type SearchMoviesArgs struct {
	PageArgs
	Query string `json:"query" jsonschema:"text to search for in movie titles and descriptions, up to 200 characters"`
}

// BoxOfficeArgs are the arguments of list_boxoffice: none, since
// Trakt returns the whole top list.
type BoxOfficeArgs struct{}

// MovieVideosArgs are the arguments of the movie handler of list_videos.
type MovieVideosArgs struct {
	MovieRef
	EmbedMarkdown *bool `json:"embed_markdown,omitempty" jsonschema:"also return each video as a ready markdown link (default true)"`
}

// RelatedMoviesArgs are the arguments of the movie handler of list_related.
type RelatedMoviesArgs struct {
	PageArgs
	MovieRef
}

func registerMovieCatalogTools(s *Server) {
	Register(s, Spec{
		Name: "list_boxoffice", Title: "List box office movies", Access: Read,
		Description: "List the top movies of the last weekend's US box office with their revenue. Trakt returns the whole list: no limit or page.",
	}, fetchBoxOfficeMovies)
}

func fetchPopularMovies(ctx context.Context, c *trakt.UserClient, in MovieListArgs) (Listing[Movie], error) {
	return paginateDecoded(ctx, in.PageArgs, trimListMovie, func(page, limit int) (*http.Response, error) {
		return c.API().GetMoviesPopular(ctx, &api.GetMoviesPopularParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func fetchAnticipatedMovies(ctx context.Context, c *trakt.UserClient, in MovieListArgs) (Listing[AnticipatedMovie], error) {
	return paginateDecoded(ctx, in.PageArgs, trimAnticipatedMovie, func(page, limit int) (*http.Response, error) {
		return c.API().GetMoviesAnticipated(ctx, &api.GetMoviesAnticipatedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func trimAnticipatedMovie(r traktAnticipatedMovie) AnticipatedMovie {
	return AnticipatedMovie{ListCount: r.ListCount, Movie: trimListMovie(r.Movie)}
}

func fetchFavoritedMovies(ctx context.Context, c *trakt.UserClient, in PeriodMovieArgs) (Listing[FavoritedMovie], error) {
	return periodShows(ctx, in, trimFavoritedMovie, func(period string, page, limit int) (*http.Response, error) {
		return c.API().GetMoviesFavorited(ctx, api.GetMoviesFavoritedParamsPeriod(period), &api.GetMoviesFavoritedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func trimFavoritedMovie(r traktFavoritedMovie) FavoritedMovie {
	return FavoritedMovie{UserCount: r.UserCount, Movie: trimListMovie(r.Movie)}
}

func trimPlayedMovie(r traktPlayedMovie) PlayedMovie {
	return PlayedMovie{WatcherCount: r.WatcherCount, PlayCount: r.PlayCount, CollectedCount: r.CollectedCount, CollectorCount: r.CollectorCount, Movie: trimListMovie(r.Movie)}
}

func fetchPlayedMovies(ctx context.Context, c *trakt.UserClient, in PeriodMovieArgs) (Listing[PlayedMovie], error) {
	return periodShows(ctx, in, trimPlayedMovie, func(period string, page, limit int) (*http.Response, error) {
		return c.API().GetMoviesPlayed(ctx, api.GetMoviesPlayedParamsPeriod(period), &api.GetMoviesPlayedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func fetchWatchedMovies(ctx context.Context, c *trakt.UserClient, in PeriodMovieArgs) (Listing[PlayedMovie], error) {
	return periodShows(ctx, in, trimPlayedMovie, func(period string, page, limit int) (*http.Response, error) {
		return c.API().GetMoviesWatched(ctx, api.GetMoviesWatchedParamsPeriod(period), &api.GetMoviesWatchedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func trimBoxOfficeMovie(r traktBoxOfficeMovie) BoxOfficeMovie {
	return BoxOfficeMovie{Revenue: r.Revenue, Movie: trimListMovie(r.Movie)}
}

func fetchBoxOfficeMovies(ctx context.Context, c *trakt.UserClient, _ BoxOfficeArgs) (Listing[BoxOfficeMovie], error) {
	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesBoxoffice(ctx, &api.GetMoviesBoxofficeParams{Extended: extendedFullPtr()})
	}, func(body []byte) (Listing[BoxOfficeMovie], error) { return unpaged(body, trimBoxOfficeMovie) })
}

func searchMovies(ctx context.Context, c *trakt.UserClient, in SearchMoviesArgs) (Listing[SearchedMovie], error) {
	query, err := queryArg(in.Query)
	if err != nil {
		return Listing[SearchedMovie]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimMovieHit, func(page, limit int) (*http.Response, error) {
		return c.API().GetSearchQuery(ctx, api.GetSearchQueryParamsTypeMovie, &api.GetSearchQueryParams{Query: &query, Page: &page, Limit: &limit})
	})
}

// queryArg trims a search query and checks its length.
func queryArg(raw string) (string, error) {
	query := strings.TrimSpace(raw)
	if query == "" || utf8.RuneCountInString(query) > maxQueryRunes {
		return "", invalidArgument(fieldQuery)
	}

	return query, nil
}

func trimMovieHit(r traktMovieHit) SearchedMovie {
	m := r.Movie

	return SearchedMovie{Score: r.Score, Title: m.Title, Year: deref(m.Year), IDs: m.trimIDs()}
}

func fetchMovieRatings(ctx context.Context, c *trakt.UserClient, in MovieRef) (Rating, error) {
	id, err := in.path()
	if err != nil {
		return Rating{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesRatings(ctx, id, &api.GetMoviesRatingsParams{})
	}, ratingOf)
}

func fetchMovieVideos(ctx context.Context, c *trakt.UserClient, in MovieVideosArgs) (Listing[Video], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Video]{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesVideos(ctx, id)
	}, func(body []byte) (Listing[Video], error) {
		return videosListing(body, embedByDefault(in.EmbedMarkdown))
	})
}

func fetchRelatedMovies(ctx context.Context, c *trakt.UserClient, in RelatedMoviesArgs) (Listing[Movie], error) {
	id, err := in.path()
	if err != nil {
		return Listing[Movie]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimListMovie, func(page, limit int) (*http.Response, error) {
		return c.API().GetMoviesRelated(ctx, id, &api.GetMoviesRelatedParams{Extended: extendedFullPtr(), Page: &page, Limit: &limit})
	})
}

func fetchMoviePeople(ctx context.Context, c *trakt.UserClient, in MovieRef) (People, error) {
	id, err := in.path()
	if err != nil {
		return People{}, err
	}

	return fetchParsed(func() (*http.Response, error) {
		return c.API().GetMoviesPeople(ctx, id, &api.GetMoviesPeopleParams{})
	}, func(body []byte) (People, error) { return peopleOf(body, false) })
}
