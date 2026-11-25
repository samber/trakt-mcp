package tools

import (
	"context"

	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// rankingTypeNote ends the description of every ranking: the type
	// argument is the only choice between the movie and the TV show list.
	rankingTypeNote = " item.page holds the paging. type is movie or show (a TV show). Takes limit and page."
	// periodNote explains the period argument of the period-based rankings.
	periodNote = " period is daily, weekly, monthly, yearly or all (default weekly)."
)

// ListRankingArgs are the arguments of the rankings without a period.
type ListRankingArgs struct {
	TypeArg
	PageArgs
}

// ListPeriodRankingArgs are the arguments of the period-based rankings.
type ListPeriodRankingArgs struct {
	TypeArg
	PageArgs
	Period string `json:"period,omitempty" jsonschema:"daily, weekly, monthly, yearly or all (default weekly)"`
}

// period is a as the arguments of the legacy period handlers.
func (a ListPeriodRankingArgs) period() PeriodShowArgs {
	return PeriodShowArgs{PageArgs: a.PageArgs, Period: a.Period}
}

// The handler of each type of every ranking.
var (
	trendingHandlers = typeHandlers[PageArgs]{
		movie: widen(func(ctx context.Context, c *trakt.UserClient, p PageArgs) (Listing[TrendingMovie], error) {
			return fetchTrendingMovies(ctx, c, TrendingMoviesArgs{PageArgs: p})
		}),
		show: widen(func(ctx context.Context, c *trakt.UserClient, p PageArgs) (Listing[TrendingShow], error) {
			return fetchTrendingShows(ctx, c, ShowListArgs{PageArgs: p})
		}),
	}
	popularHandlers = typeHandlers[PageArgs]{
		movie: widen(func(ctx context.Context, c *trakt.UserClient, p PageArgs) (Listing[Movie], error) {
			return fetchPopularMovies(ctx, c, MovieListArgs{PageArgs: p})
		}),
		show: widen(func(ctx context.Context, c *trakt.UserClient, p PageArgs) (Listing[Show], error) {
			return fetchPopularShows(ctx, c, ShowListArgs{PageArgs: p})
		}),
	}
	anticipatedHandlers = typeHandlers[PageArgs]{
		movie: widen(func(ctx context.Context, c *trakt.UserClient, p PageArgs) (Listing[AnticipatedMovie], error) {
			return fetchAnticipatedMovies(ctx, c, MovieListArgs{PageArgs: p})
		}),
		show: widen(func(ctx context.Context, c *trakt.UserClient, p PageArgs) (Listing[AnticipatedShow], error) {
			return fetchAnticipatedShows(ctx, c, ShowListArgs{PageArgs: p})
		}),
	}
	favoritedHandlers = typeHandlers[PeriodShowArgs]{movie: widen(fetchFavoritedMovies), show: widen(fetchFavoritedShows)}
	playedHandlers    = typeHandlers[PeriodShowArgs]{movie: widen(fetchPlayedMovies), show: widen(fetchPlayedShows)}
	watchedHandlers   = typeHandlers[PeriodShowArgs]{movie: widen(fetchWatchedMovies), show: widen(fetchWatchedShows)}
)

func registerRankingTools(s *Server) {
	Register(s, Spec{
		Name: "list_trending", Title: "List trending", Access: Read,
		Description: "List the movies or TV shows most watched on Trakt right now, returned as {type, item}: item.items is a list of " +
			"{watchers, movie} (type movie) or {watchers, show} (type show), with how many people are watching each." + rankingTypeNote,
	}, listTrending)
	Register(s, Spec{
		Name: "list_popular", Title: "List popular", Access: Read,
		Description: "List the most popular movies or TV shows on Trakt, by rating and votes, returned as {type, item}: item.items is a list of " +
			"movies (type movie) or TV shows (type show)." + rankingTypeNote,
	}, listPopular)
	Register(s, Spec{
		Name: "list_anticipated", Title: "List anticipated", Access: Read,
		Description: "List the most anticipated upcoming movies or TV shows, returned as {type, item}: item.items is a list of " +
			"{list_count, movie} (type movie) or {list_count, show} (type show), with the number of lists each is on." + rankingTypeNote,
	}, listAnticipated)
	Register(s, Spec{
		Name: "list_favorited", Title: "List favorited", Access: Read,
		Description: "List the movies or TV shows favorited by the most users in a period, returned as {type, item}: item.items is a list of " +
			"{user_count, movie} (type movie) or {user_count, show} (type show)." + periodNote + rankingTypeNote,
	}, listFavorited)
	Register(s, Spec{
		Name: "list_played", Title: "List most played", Access: Read,
		Description: "List the movies or TV shows with the most plays in a period, returned as {type, item}: item.items is a list of " +
			"{watcher_count, play_count, collected_count, collector_count, movie} (type movie) or the same with show (type show)." +
			periodNote + rankingTypeNote,
	}, listPlayed)
	Register(s, Spec{
		Name: "list_watched", Title: "List most watched", Access: Read,
		Description: "List the movies or TV shows watched by the most users in a period, returned as {type, item}: item.items is a list of " +
			"{watcher_count, play_count, collected_count, collector_count, movie} (type movie) or the same with show (type show)." +
			periodNote + rankingTypeNote,
	}, listWatched)
}

func listTrending(ctx context.Context, c *trakt.UserClient, in ListRankingArgs) (Typed, error) {
	return trendingHandlers.call(ctx, c, in.Type, in.PageArgs)
}

func listPopular(ctx context.Context, c *trakt.UserClient, in ListRankingArgs) (Typed, error) {
	return popularHandlers.call(ctx, c, in.Type, in.PageArgs)
}

func listAnticipated(ctx context.Context, c *trakt.UserClient, in ListRankingArgs) (Typed, error) {
	return anticipatedHandlers.call(ctx, c, in.Type, in.PageArgs)
}

func listFavorited(ctx context.Context, c *trakt.UserClient, in ListPeriodRankingArgs) (Typed, error) {
	return favoritedHandlers.call(ctx, c, in.Type, in.period())
}

func listPlayed(ctx context.Context, c *trakt.UserClient, in ListPeriodRankingArgs) (Typed, error) {
	return playedHandlers.call(ctx, c, in.Type, in.period())
}

func listWatched(ctx context.Context, c *trakt.UserClient, in ListPeriodRankingArgs) (Typed, error) {
	return watchedHandlers.call(ctx, c, in.Type, in.period())
}
