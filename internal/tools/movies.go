package tools

import (
	"context"
	"errors"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// extendedFull asks Trakt for every field of an item: without it Trakt
// returns only title, year and ids.
const extendedFull = "full"

// fieldMovieID names the movie argument in tool errors.
const fieldMovieID = "movie_id"

// errNoResponse is the cause of a 2xx answer without a parsed HTTP
// response, which the generated client never produces.
var errNoResponse = errors.New("trakt: response missing")

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

func (m traktMovie) trim() Movie {
	return Movie{
		Title: m.Title,
		Year:  deref(m.Year),
		IDs: MovieIDs{
			Trakt: m.IDs.Trakt,
			Slug:  m.IDs.Slug,
			IMDb:  deref(m.IDs.IMDb),
			TMDB:  deref(m.IDs.TMDB),
		},
		Rating:        deref(m.Rating),
		Votes:         deref(m.Votes),
		Overview:      boundText(deref(m.Overview), maxOverviewRunes),
		Runtime:       deref(m.Runtime),
		Certification: deref(m.Certification),
		Genres:        NonNil(m.Genres),
		Released:      deref(m.Released),
	}
}

// TrendingMoviesArgs are the arguments of fetch_trending_movies.
type TrendingMoviesArgs struct {
	PageArgs
}

// MovieSummaryArgs are the arguments of fetch_movie_summary.
type MovieSummaryArgs struct {
	MovieID  string `json:"movie_id"           jsonschema:"Trakt slug (the-matrix-1999), numeric Trakt id (481) or IMDb id (tt0133093)"`
	Extended *bool  `json:"extended,omitempty" jsonschema:"include overview, rating, runtime, certification and genres (default true)"`
}

func registerMovieTools(s *Server) {
	Register(s, Spec{
		Name:        "fetch_trending_movies",
		Title:       "Fetch trending movies",
		Description: "List the movies most watched on Trakt right now, with how many people are watching each.",
		Access:      Read,
	}, fetchTrendingMovies)
	Register(s, Spec{
		Name:        "fetch_movie_summary",
		Title:       "Fetch movie summary",
		Description: "Get one movie's details: year, ids, rating, overview, runtime, certification, genres and release date.",
		Access:      Read,
	}, fetchMovieSummary)
}

func fetchTrendingMovies(ctx context.Context, c *trakt.UserClient, in TrendingMoviesArgs) (Listing[TrendingMovie], error) {
	return Paginate(ctx, in.PageArgs, func(page, limit int) ([]TrendingMovie, trakt.Page, error) {
		extended := extendedFull

		resp, err := c.API().GetMoviesTrendingWithResponse(ctx, &api.GetMoviesTrendingParams{Extended: &extended, Page: &page, Limit: &limit})
		if err != nil {
			return nil, trakt.Page{}, err //nolint:wrapcheck // *trakt.Error is mapped by Kind
		}

		if resp.HTTPResponse == nil {
			return nil, trakt.Page{}, errNoResponse
		}

		var raw []struct {
			Watchers int        `json:"watchers"`
			Movie    traktMovie `json:"movie"`
		}
		if err := decodeBody(resp.Body, &raw); err != nil {
			return nil, trakt.Page{}, err
		}

		out := make([]TrendingMovie, 0, len(raw))
		for _, r := range raw {
			out = append(out, TrendingMovie{Watchers: r.Watchers, Movie: r.Movie.trim()})
		}

		return out, trakt.ParsePage(resp.HTTPResponse.Header), nil
	})
}

func fetchMovieSummary(ctx context.Context, c *trakt.UserClient, in MovieSummaryArgs) (Movie, error) {
	id, err := idArg(fieldMovieID, in.MovieID, Slug|Numeric|IMDbTitle)
	if err != nil {
		return Movie{}, err
	}

	params := &api.GetMoviesSummaryParams{}

	if in.Extended == nil || *in.Extended {
		extended := extendedFull
		params.Extended = &extended
	}

	resp, err := c.API().GetMoviesSummaryWithResponse(ctx, string(id), params)
	if err != nil {
		return Movie{}, err //nolint:wrapcheck // *trakt.Error is mapped by Kind
	}

	var raw traktMovie
	if err := decodeBody(resp.Body, &raw); err != nil {
		return Movie{}, err
	}

	return raw.trim(), nil
}
