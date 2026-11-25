package tools

import (
	"context"
	"slices"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// Values of the type argument of tools that take one item of several media
// types. They are the singular words the batch write tools already accept,
// so a model uses one vocabulary everywhere.
const (
	typeMovie   = itemMovie
	typeShow    = itemShow
	typeSeason  = itemSeason
	typeEpisode = itemEpisode
)

// itemIDKinds are the identifier forms of the movie or TV show an ItemRef
// names: seasons and episodes have no id of their own here, they are
// numbered inside their show.
const itemIDKinds = Slug | Numeric | IMDbTitle

// TypeArg is the type argument of a tool that takes no id: the media type of
// the list it returns or acts on.
type TypeArg struct {
	Type string `json:"type" jsonschema:"movie or show; the tool description lists the values it accepts"`
}

// ItemRef names one movie, TV show, season or episode. Season is required
// for a season or an episode, Episode for an episode only; both are
// rejected for any other type, never ignored.
type ItemRef struct {
	Type    string `json:"type"              jsonschema:"movie, show, season or episode; the tool description lists the values it accepts"`
	ID      string `json:"id"                jsonschema:"Trakt slug (breaking-bad), numeric Trakt id (1388) or IMDb id (tt0903747) of the movie or TV show; for a season or an episode, the TV show's id"`
	Season  *int   `json:"season,omitempty"  jsonschema:"season number, 0 to 999 (0 is the specials); required for type season or episode, refused otherwise"`
	Episode *int   `json:"episode,omitempty" jsonschema:"episode number in the season, 1 to 9999; required for type episode, refused otherwise"`
}

// typeHandlers holds the handler of a movie and of a TV show for a tool that
// takes a bare type argument and whose result shape differs by type. The
// result travels in a Typed envelope.
type typeHandlers[A any] struct {
	movie func(context.Context, *trakt.UserClient, A) (any, error)
	show  func(context.Context, *trakt.UserClient, A) (any, error)
}

// call validates typ, then runs the handler of its type on in.
func (h typeHandlers[A]) call(ctx context.Context, c *trakt.UserClient, typ string, in A) (Typed, error) {
	if err := checkType(typ, typeMovie, typeShow); err != nil {
		return Typed{}, err
	}

	if typ == typeMovie {
		item, err := h.movie(ctx, c, in)

		return typed(typ, item, err)
	}

	item, err := h.show(ctx, c, in)

	return typed(typ, item, err)
}

// widen adapts a handler with a concrete result type to the result type of
// typeHandlers.
func widen[A, T any](f func(context.Context, *trakt.UserClient, A) (T, error)) func(context.Context, *trakt.UserClient, A) (any, error) {
	return func(ctx context.Context, c *trakt.UserClient, in A) (any, error) {
		return f(ctx, c, in)
	}
}

// checkType fails with an invalid-argument error naming the type field when
// value is not one of allowed. The match is exact: no default, no case
// folding, so "" and "Movie" are refused.
func checkType(value string, allowed ...string) error {
	if !slices.Contains(allowed, value) {
		return invalidArgument(fieldType)
	}

	return nil
}

// check validates r for a tool accepting the allowed types: the type, the
// id, then the season and episode the type requires or forbids.
func (r ItemRef) check(allowed ...string) error {
	if err := checkType(r.Type, allowed...); err != nil {
		return err
	}

	if _, err := idArg(fieldID, r.ID, itemIDKinds); err != nil {
		return err
	}

	needSeason := r.Type == typeSeason || r.Type == typeEpisode
	needEpisode := r.Type == typeEpisode

	if err := checkOptional(r.Season, needSeason, fieldSeason, checkSeason); err != nil {
		return err
	}

	return checkOptional(r.Episode, needEpisode, fieldEpisode, checkEpisode)
}

// checkOptional requires n when needed and refuses it otherwise; a present
// value must also pass inRange.
func checkOptional(n *int, needed bool, field string, inRange func(int) error) error {
	switch {
	case n == nil && needed, n != nil && !needed:
		return invalidArgument(field)
	case n == nil:
		return nil
	default:
		return inRange(*n)
	}
}

// movieArgs is r as the arguments of the movie summary handler.
func (r ItemRef) movieArgs() MovieSummaryArgs { return MovieSummaryArgs{MovieID: r.ID} }

// movieRef is r as a movie reference.
func (r ItemRef) movieRef() MovieRef { return MovieRef{MovieID: r.ID} }

// showRef is r as a TV show reference.
func (r ItemRef) showRef() ShowRef { return ShowRef{ShowID: r.ID} }

// seasonRef is r as a season reference; call it after check.
func (r ItemRef) seasonRef() SeasonRef {
	return SeasonRef{ShowRef: r.showRef(), Season: deref(r.Season)}
}

// episodeRef is r as an episode reference; call it after check.
func (r ItemRef) episodeRef() EpisodeRef {
	return EpisodeRef{SeasonRef: r.seasonRef(), Episode: deref(r.Episode)}
}
