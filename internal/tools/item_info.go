package tools

import (
	"context"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// ListTranslationsArgs are the arguments of list_translations.
type ListTranslationsArgs struct {
	ItemRef
	Language string `json:"language,omitempty" jsonschema:"two-letter ISO 639-1 code such as fr; omit it for every language"`
}

// itemFetchers holds the handler of each media type for a tool whose result
// has the same shape for every type.
type itemFetchers[T any] struct {
	movie   func(context.Context, *trakt.UserClient, MovieRef) (T, error)
	show    func(context.Context, *trakt.UserClient, ShowRef) (T, error)
	season  func(context.Context, *trakt.UserClient, SeasonRef) (T, error)
	episode func(context.Context, *trakt.UserClient, EpisodeRef) (T, error)
}

// fetch validates ref, then calls the handler of its type.
func (f itemFetchers[T]) fetch(ctx context.Context, c *trakt.UserClient, ref ItemRef) (T, error) {
	if err := ref.check(typeMovie, typeShow, typeSeason, typeEpisode); err != nil {
		var zero T

		return zero, err
	}

	switch ref.Type {
	case typeMovie:
		return f.movie(ctx, c, ref.movieRef())
	case typeShow:
		return f.show(ctx, c, ref.showRef())
	case typeSeason:
		return f.season(ctx, c, ref.seasonRef())
	default:
		return f.episode(ctx, c, ref.episodeRef())
	}
}

var (
	ratingFetchers = itemFetchers[Rating]{fetchMovieRatings, fetchShowRatings, fetchSeasonRatings, fetchEpisodeRatings}
	statsFetchers  = itemFetchers[Stats]{fetchMovieStats, fetchShowStats, fetchSeasonStats, fetchEpisodeStats}
	watchFetchers  = itemFetchers[Watching]{fetchMovieWatching, fetchShowWatching, fetchSeasonWatching, fetchEpisodeWatching}
)

// itemTypesNote explains the type, id, season and episode arguments in the
// description of every tool taking an ItemRef of any type.
const itemTypesNote = "type is movie, show (a TV show), season (a TV show season; needs season, 0 is the specials) " +
	"or episode (a TV show episode; needs season and episode). id is the movie's or the TV show's id."

func registerItemInfoTools(s *Server) {
	Register(s, Spec{
		Name: "get_ratings", Title: "Get ratings", Access: Read,
		Description: "Get a movie's, TV show's, TV show season's or TV show episode's average rating, vote count and " +
			"the number of votes for each rating from 1 to 10. " + itemTypesNote,
	}, getRatings)
	Register(s, Spec{
		Name: "get_stats", Title: "Get stats", Access: Read,
		Description: "Get a movie's, TV show's, TV show season's or TV show episode's counters: watchers, plays, " +
			"collectors, comments, lists and votes. " + itemTypesNote,
	}, getStats)
	Register(s, Spec{
		Name: "list_watching", Title: "List watchers", Access: Read,
		Description: "Count the users watching a movie, TV show, TV show season or TV show episode right now and list " +
			"their usernames, up to 50. Private accounts are counted, not listed. " + itemTypesNote,
	}, listWatching)
	Register(s, Spec{
		Name: "list_translations", Title: "List translations", Access: Read,
		Description: "Get a movie's, TV show's, TV show season's or TV show episode's title and overview in one " +
			"language (language, a two-letter code) or in every language. " + itemTypesNote,
	}, listTranslations)
}

func getRatings(ctx context.Context, c *trakt.UserClient, in ItemRef) (Rating, error) {
	return ratingFetchers.fetch(ctx, c, in)
}

func getStats(ctx context.Context, c *trakt.UserClient, in ItemRef) (Stats, error) {
	return statsFetchers.fetch(ctx, c, in)
}

func listWatching(ctx context.Context, c *trakt.UserClient, in ItemRef) (Watching, error) {
	return watchFetchers.fetch(ctx, c, in)
}

func listTranslations(ctx context.Context, c *trakt.UserClient, in ListTranslationsArgs) (Listing[Translation], error) {
	return itemFetchers[Listing[Translation]]{
		movie: func(ctx context.Context, c *trakt.UserClient, r MovieRef) (Listing[Translation], error) {
			return fetchMovieTranslations(ctx, c, MovieTranslationsArgs{MovieRef: r, Language: in.Language})
		},
		show: func(ctx context.Context, c *trakt.UserClient, r ShowRef) (Listing[Translation], error) {
			return fetchShowTranslations(ctx, c, ShowTranslationsArgs{ShowRef: r, Language: in.Language})
		},
		season: func(ctx context.Context, c *trakt.UserClient, r SeasonRef) (Listing[Translation], error) {
			return fetchSeasonTranslations(ctx, c, SeasonTranslationsArgs{SeasonRef: r, Language: in.Language})
		},
		episode: func(ctx context.Context, c *trakt.UserClient, r EpisodeRef) (Listing[Translation], error) {
			return fetchEpisodeTranslations(ctx, c, EpisodeTranslationsArgs{EpisodeRef: r, Language: in.Language})
		},
	}.fetch(ctx, c, in.ItemRef)
}
