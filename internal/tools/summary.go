package tools

import (
	"context"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// fieldExtended names the extended argument in tool errors.
const fieldExtended = "extended"

// Typed is the output of a tool whose result shape depends on the type
// argument: Type repeats it, and Item holds the movie, TV show, season or
// episode in the same shape the type's own handler returns.
type Typed struct {
	Type string `json:"type" jsonschema:"repeats the type argument"`
	Item any    `json:"item" jsonschema:"the result for that type"`
}

// resultCount reports the item's own count, so logs and metrics read the
// same as for the handler that built it.
func (t Typed) resultCount() int { return resultCount(t.Item) }

// typed wraps a handler's result in a Typed, keeping its error.
func typed[T any](typ string, item T, err error) (Typed, error) {
	if err != nil {
		return Typed{}, err
	}

	return Typed{Type: typ, Item: item}, nil
}

// GetSummaryArgs are the arguments of get_summary.
type GetSummaryArgs struct {
	ItemRef
	Extended *bool `json:"extended,omitempty" jsonschema:"type movie or show only: include overview, rating, runtime, certification, genres and, for a TV show, network and status (default true)"`
}

func registerSummaryTools(s *Server) {
	Register(s, Spec{
		Name: "get_summary", Title: "Get summary", Access: Read,
		Description: "Get one item's details, returned as {type, item}. " +
			"type movie: year, ids, rating, overview, runtime, certification, genres and release date. " +
			"type show: a TV show's year, ids, rating, overview, runtime, certification, network, status, first air date and genres. " +
			"type season: a TV show season's number, title, overview, rating, episode count and first air date; needs season (0 is the specials). " +
			"type episode: a TV show episode's title, overview, rating, runtime, air date, episode type and ids; needs season and episode. " +
			"id is the movie's or the TV show's id. extended applies to movie and show only.",
	}, getSummary)
}

func getSummary(ctx context.Context, c *trakt.UserClient, in GetSummaryArgs) (Typed, error) {
	if err := in.check(typeMovie, typeShow, typeSeason, typeEpisode); err != nil {
		return Typed{}, err
	}

	switch in.Type {
	case typeMovie:
		args := in.movieArgs()
		args.Extended = in.Extended
		item, err := fetchMovieSummary(ctx, c, args)

		return typed(in.Type, item, err)
	case typeShow:
		item, err := fetchShowSummary(ctx, c, ShowSummaryArgs{ShowRef: in.showRef(), Extended: in.Extended})

		return typed(in.Type, item, err)
	}

	// Seasons and episodes have no short form: refuse extended rather than
	// ignore it, so the caller learns it had no effect.
	if in.Extended != nil {
		return Typed{}, invalidArgument(fieldExtended)
	}

	if in.Type == typeSeason {
		item, err := fetchSeasonInfo(ctx, c, in.seasonRef())

		return typed(in.Type, item, err)
	}

	item, err := fetchEpisodeSummary(ctx, c, in.episodeRef())

	return typed(in.Type, item, err)
}
