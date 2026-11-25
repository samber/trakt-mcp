package tools

import (
	"context"
	"net/http"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// Argument names of the id lookup.
const (
	fieldIDType = "id_type"
	fieldID     = "id"
)

const (
	idTypeTrakt = "trakt"
	idTypeIMDb  = "imdb"
	idTypeTMDB  = "tmdb"
	idTypeTVDB  = "tvdb"

	lookupPerson = "person"
)

// lookupIDTypes are the id systems Trakt resolves; lookupTypes filter the
// kind of result. Trakt also returns lists, which carry no title or id the
// tools could use, so the filter does not offer them.
var (
	lookupIDTypes = []string{idTypeTrakt, idTypeIMDb, idTypeTMDB, idTypeTVDB}
	lookupTypes   = []string{itemMovie, itemShow, itemEpisode, lookupPerson}
)

// SearchByIDArgs are the arguments of search_by_id.
type SearchByIDArgs struct {
	PageArgs
	IDType string `json:"id_type" jsonschema:"id system of id: trakt, imdb, tmdb or tvdb"`
	ID     string `json:"id" jsonschema:"the id: digits for trakt, tmdb and tvdb, tt... or nm... for imdb"`
	Type   string `json:"type,omitempty" jsonschema:"only return this kind of result: movie, show, episode or person; omit for all"`
}

// SearchIDs are the ids of a search by id result.
type SearchIDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug,omitempty"`
	IMDb  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
	TVDB  int    `json:"tvdb,omitempty"`
}

// IDMatch is one result of search_by_id.
type IDMatch struct {
	Type    string    `json:"type" jsonschema:"movie, show, episode or person"`
	Title   string    `json:"title,omitempty" jsonschema:"title of a movie, show or episode, or name of a person; third-party text"`
	Year    int       `json:"year,omitempty"`
	Season  int       `json:"season,omitempty" jsonschema:"season number (episodes)"`
	Episode int       `json:"episode,omitempty" jsonschema:"episode number (episodes)"`
	IDs     SearchIDs `json:"ids"`
}

type traktLookupIDs struct {
	Trakt int     `json:"trakt"`
	Slug  *string `json:"slug"`
	IMDb  *string `json:"imdb"`
	TMDB  *int    `json:"tmdb"`
	TVDB  *int    `json:"tvdb"`
}

// traktLookupEntity is the movie, show, episode or person of a result: the
// fields the four kinds share, which is all the tool keeps.
type traktLookupEntity struct {
	Title  *string        `json:"title"`
	Name   *string        `json:"name"`
	Year   *int           `json:"year"`
	Season *int           `json:"season"`
	Number *int           `json:"number"`
	IDs    traktLookupIDs `json:"ids"`
}

type traktLookupHit struct {
	Type    string             `json:"type"`
	Movie   *traktLookupEntity `json:"movie"`
	Show    *traktLookupEntity `json:"show"`
	Episode *traktLookupEntity `json:"episode"`
	Person  *traktLookupEntity `json:"person"`
}

func trimLookupHit(h traktLookupHit) IDMatch {
	var e *traktLookupEntity

	switch h.Type {
	case itemMovie:
		e = h.Movie
	case itemShow:
		e = h.Show
	case itemEpisode:
		e = h.Episode
	case lookupPerson:
		e = h.Person
	}

	out := IDMatch{Type: h.Type}
	if e == nil {
		return out
	}

	title := deref(e.Title)
	if e.Name != nil {
		title = *e.Name
	}

	out.Title = boundText(title, maxTitleRunes)
	out.Year = deref(e.Year)
	out.Season = deref(e.Season)
	out.Episode = deref(e.Number)
	out.IDs = SearchIDs{
		Trakt: e.IDs.Trakt, Slug: boundText(deref(e.IDs.Slug), maxIDLength), IMDb: boundText(deref(e.IDs.IMDb), maxIDLength),
		TMDB: deref(e.IDs.TMDB), TVDB: deref(e.IDs.TVDB),
	}

	return out
}

// lookupIDKinds are the id forms each id system accepts.
func lookupIDKinds(idType string) IDKinds {
	if idType == idTypeIMDb {
		return IMDbTitle | IMDbPerson
	}

	return Numeric
}

// SearchExactArgs are the arguments of search_movies_exact and
// search_shows_exact.
type SearchExactArgs struct {
	PageArgs
	Query string `json:"query" jsonschema:"title to match exactly, up to 200 characters"`
}

func registerSearchExtraTools(s *Server) {
	const pageNote = " Takes limit and page."

	Register(s, Spec{
		Name: "search_by_id", Title: "Search by id", Access: Read,
		Description: "Find the movies, TV shows, episodes and people that carry an id of another system (Trakt, IMDb, TMDB or TVDB). Optionally keep one kind of result." + pageNote + untrustedNote,
	}, searchByID)
	Register(s, Spec{
		Name: "search_movies_exact", Title: "Search movies by exact title", Access: Read,
		Description: "Search movies whose title matches the query exactly. Returns each match's relevance score, title, year and ids." + pageNote,
	}, searchMoviesExact)
	Register(s, Spec{
		Name: "search_shows_exact", Title: "Search TV shows by exact title", Access: Read,
		Description: "Search TV series whose title matches the query exactly. Returns each match's relevance score, title, year and ids." + pageNote,
	}, searchShowsExact)
}

func searchByID(ctx context.Context, c *trakt.UserClient, in SearchByIDArgs) (Listing[IDMatch], error) {
	idType, err := oneOf(fieldIDType, in.IDType, "", lookupIDTypes)
	if err != nil || idType == "" {
		return Listing[IDMatch]{}, invalidArgument(fieldIDType)
	}

	id, err := idArg(fieldID, in.ID, lookupIDKinds(idType))
	if err != nil {
		return Listing[IDMatch]{}, err
	}

	typ, err := oneOf(fieldType, in.Type, "", lookupTypes)
	if err != nil {
		return Listing[IDMatch]{}, err
	}

	var typePtr *string
	if typ != "" {
		typePtr = &typ
	}

	return paginateDecoded(ctx, in.PageArgs, trimLookupHit, func(page, limit int) (*http.Response, error) {
		return c.API().GetSearchLookup(ctx, idType, string(id), &api.GetSearchLookupParams{Type: typePtr, Page: &page, Limit: &limit})
	})
}

// searchExact runs the exact-title search of one media type.
func searchExact[R, T any](ctx context.Context, c *trakt.UserClient, in SearchExactArgs, typ string, trim func(R) T) (Listing[T], error) {
	query, err := queryArg(in.Query)
	if err != nil {
		return Listing[T]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trim, func(page, limit int) (*http.Response, error) {
		return c.API().GetSearchExact(ctx, api.GetSearchExactParamsType(typ), &api.GetSearchExactParams{Query: &query, Page: &page, Limit: &limit})
	})
}

func searchMoviesExact(ctx context.Context, c *trakt.UserClient, in SearchExactArgs) (Listing[SearchedMovie], error) {
	return searchExact(ctx, c, in, itemMovie, trimMovieHit)
}

func searchShowsExact(ctx context.Context, c *trakt.UserClient, in SearchExactArgs) (Listing[SearchedShow], error) {
	return searchExact(ctx, c, in, itemShow, trimSearchHit)
}
