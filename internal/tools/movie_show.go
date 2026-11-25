package tools

import (
	"context"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// movieShowTypesNote explains the type and id arguments in the description of
// every tool that takes a movie or a TV show and nothing else.
const movieShowTypesNote = "type is movie or show (a TV show). id is the movie's or the TV show's id."

// ListRelatedArgs are the arguments of list_related.
type ListRelatedArgs struct {
	ItemRef
	PageArgs
}

// aliasFetchers and studioFetchers hold the handler of each type that has
// alternate titles or production studios.
var (
	aliasFetchers  = itemFetchers[Listing[Alias]]{movie: fetchMovieAliases, show: fetchShowAliases}
	studioFetchers = itemFetchers[Listing[Studio]]{movie: fetchMovieStudios, show: fetchShowStudios}
)

func registerMovieShowTools(s *Server) {
	Register(s, Spec{
		Name: "list_aliases", Title: "List aliases", Access: Read,
		Description: "List the alternate titles of a movie or a TV show with the country each is used in. " + movieShowTypesNote,
	}, listAliases)
	Register(s, Spec{
		Name: "list_studios", Title: "List studios", Access: Read,
		Description: "List the production studios of a movie or a TV show with country and ids. " + movieShowTypesNote,
	}, listStudios)
	Register(s, Spec{
		Name: "list_related", Title: "List related", Access: Read,
		Description: "List the movies related to a movie or the TV shows related to a TV show, returned as {type, item}: " +
			"item is a list of movies (type movie) or TV shows (type show). Takes limit and page. " + movieShowTypesNote,
	}, listRelated)
}

func listAliases(ctx context.Context, c *trakt.UserClient, in ItemRef) (Listing[Alias], error) {
	return aliasFetchers.fetch(ctx, c, in)
}

func listStudios(ctx context.Context, c *trakt.UserClient, in ItemRef) (Listing[Studio], error) {
	return studioFetchers.fetch(ctx, c, in)
}

func listRelated(ctx context.Context, c *trakt.UserClient, in ListRelatedArgs) (Typed, error) {
	if err := in.check(typeMovie, typeShow); err != nil {
		return Typed{}, err
	}

	if in.Type == typeMovie {
		item, err := fetchRelatedMovies(ctx, c, RelatedMoviesArgs{PageArgs: in.PageArgs, MovieRef: in.movieRef()})

		return typed(in.Type, item, err)
	}

	item, err := fetchRelatedShows(ctx, c, RelatedShowsArgs{PageArgs: in.PageArgs, ShowRef: in.showRef()})

	return typed(in.Type, item, err)
}
