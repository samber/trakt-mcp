package tools

import (
	"context"
	"io"
	"net/http"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// RecommendationArgs are the arguments of fetch_movie_recommendations and
// fetch_show_recommendations. Trakt does not paginate recommendations: one
// request returns up to limit items, so there is no page argument.
type RecommendationArgs struct {
	Limit             int   `json:"limit,omitempty"              jsonschema:"number of recommendations to return, 1 to 100 (default 10)"`
	IgnoreCollected   *bool `json:"ignore_collected,omitempty"   jsonschema:"leave out items already in the user's collection (Trakt's default is false)"`
	IgnoreWatchlisted *bool `json:"ignore_watchlisted,omitempty" jsonschema:"leave out items already on the user's watchlist (Trakt's default is false)"`
}

// HideRecommendationResult confirms a hidden recommendation.
type HideRecommendationResult struct {
	Hidden bool   `json:"hidden"`
	Type   string `json:"type"   jsonschema:"movie or show"`
	ID     string `json:"id"     jsonschema:"the id as the caller sent it"`
}

func (HideRecommendationResult) resultCount() int { return 1 }

// UnhideRecommendationResult is the outcome of an unhide.
type UnhideRecommendationResult struct {
	Unhidden bool   `json:"unhidden" jsonschema:"false when the item was not hidden or Trakt did not find it"`
	Type     string `json:"type"     jsonschema:"movie or show"`
	ID       string `json:"id"       jsonschema:"the id as the caller sent it"`
}

func (UnhideRecommendationResult) resultCount() int { return 1 }

func registerRecommendationTools(s *Server) {
	Register(s, Spec{
		Name:  "fetch_movie_recommendations",
		Title: "Fetch movie recommendations",
		Description: "List movies Trakt recommends to the user from their watching history, optionally leaving out " +
			"collected or watchlisted movies. Takes limit (no page: Trakt returns one list).",
		Access: Read,
	}, fetchMovieRecommendations)
	Register(s, Spec{
		Name:  "fetch_show_recommendations",
		Title: "Fetch show recommendations",
		Description: "List shows Trakt recommends to the user from their watching history, optionally leaving out " +
			"collected or watchlisted shows. Takes limit (no page: Trakt returns one list).",
		Access: Read,
	}, fetchShowRecommendations)
	Register(s, Spec{
		Name:        "hide_movie_recommendation",
		Title:       "Hide movie recommendation",
		Description: "Hide one movie from the user's future recommendations; unhide_movie_recommendation reverses it.",
		Access:      Write,
	}, hideMovieRecommendation)
	Register(s, Spec{
		Name:        "hide_show_recommendation",
		Title:       "Hide show recommendation",
		Description: "Hide one show from the user's future recommendations; unhide_show_recommendation reverses it.",
		Access:      Write,
	}, hideShowRecommendation)
	Register(s, Spec{
		Name:        "unhide_movie_recommendation",
		Title:       "Unhide movie recommendation",
		Description: "Let a movie the user hid from recommendations be recommended again.",
		Access:      Write,
	}, unhideMovieRecommendation)
	Register(s, Spec{
		Name:        "unhide_show_recommendation",
		Title:       "Unhide show recommendation",
		Description: "Let a show the user hid from recommendations be recommended again.",
		Access:      Write,
	}, unhideShowRecommendation)
}

// recommendationLimit validates limit with the same bounds as list tools.
func recommendationLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}

	if limit < 1 || limit > maxLimit {
		return 0, invalidArgument(fieldLimit)
	}

	return limit, nil
}

// capped keeps the first limit items: Trakt honours limit, but the tool
// must not return more than the caller asked for if it ever does not.
func capped[T any](l Listing[T], limit int) Listing[T] {
	if len(l.Items) > limit {
		l.Items = l.Items[:limit]
	}

	return l
}

// recommendations validates limit, makes the one Trakt call and trims the
// answer to at most limit items.
func recommendations[R, T any](in RecommendationArgs, trim func(R) T, call func(limit int) (*http.Response, error)) (Listing[T], error) {
	limit, err := recommendationLimit(in.Limit)
	if err != nil {
		return Listing[T]{}, err
	}

	out, err := fetchParsed(func() (*http.Response, error) { return call(limit) },
		func(body []byte) (Listing[T], error) { return unpaged(body, trim) })
	if err != nil {
		return Listing[T]{}, err
	}

	return capped(out, limit), nil
}

func fetchMovieRecommendations(ctx context.Context, c *trakt.UserClient, in RecommendationArgs) (Listing[Movie], error) {
	return recommendations(in, trimListMovie, func(limit int) (*http.Response, error) {
		return c.API().GetRecommendationsMoviesRecommend(ctx, &api.GetRecommendationsMoviesRecommendParams{
			Extended:          extendedFullPtr(),
			Limit:             limit,
			IgnoreCollected:   in.IgnoreCollected,
			IgnoreWatchlisted: in.IgnoreWatchlisted,
		})
	})
}

func fetchShowRecommendations(ctx context.Context, c *trakt.UserClient, in RecommendationArgs) (Listing[Show], error) {
	return recommendations(in, trimListShow, func(limit int) (*http.Response, error) {
		return c.API().GetRecommendationsShowsRecommend(ctx, &api.GetRecommendationsShowsRecommendParams{
			Extended:          extendedFullPtr(),
			Limit:             limit,
			IgnoreCollected:   in.IgnoreCollected,
			IgnoreWatchlisted: in.IgnoreWatchlisted,
		})
	})
}

func hideMovieRecommendation(ctx context.Context, c *trakt.UserClient, in MovieRef) (HideRecommendationResult, error) {
	id, err := in.path()
	if err != nil {
		return HideRecommendationResult{}, err
	}

	if _, err := readReply(func() (*http.Response, error) {
		return c.API().DeleteRecommendationsMoviesHide(ctx, id)
	}); err != nil {
		return HideRecommendationResult{}, err
	}

	return HideRecommendationResult{Hidden: true, Type: itemMovie, ID: id}, nil
}

func hideShowRecommendation(ctx context.Context, c *trakt.UserClient, in ShowRef) (HideRecommendationResult, error) {
	id, err := showPath(in.ShowID)
	if err != nil {
		return HideRecommendationResult{}, err
	}

	if _, err := readReply(func() (*http.Response, error) {
		return c.API().DeleteRecommendationsShowsHide(ctx, id)
	}); err != nil {
		return HideRecommendationResult{}, err
	}

	return HideRecommendationResult{Hidden: true, Type: itemShow, ID: id}, nil
}

func unhideMovieRecommendation(ctx context.Context, c *trakt.UserClient, in MovieRef) (UnhideRecommendationResult, error) {
	id, err := in.path()
	if err != nil {
		return UnhideRecommendationResult{}, err
	}

	return unhideRecommendation(ctx, c, itemMovie, id)
}

func unhideShowRecommendation(ctx context.Context, c *trakt.UserClient, in ShowRef) (UnhideRecommendationResult, error) {
	id, err := showPath(in.ShowID)
	if err != nil {
		return UnhideRecommendationResult{}, err
	}

	return unhideRecommendation(ctx, c, itemShow, id)
}

// unhideRecommendation removes one validated item from the user's hidden
// recommendations, through the same sync body as the batch writes.
func unhideRecommendation(ctx context.Context, c *trakt.UserClient, typ, id string) (UnhideRecommendationResult, error) {
	res, err := writeItems([]Item{{Type: typ, ID: ID(id)}}, func(body io.Reader) (*http.Response, error) {
		return c.API().PostUsersHiddenRemoveSectionWithBody(ctx, api.PostUsersHiddenRemoveSectionParamsSectionRecommendations, jsonContentType, body)
	})
	if err != nil {
		return UnhideRecommendationResult{}, err
	}

	return UnhideRecommendationResult{Unhidden: res.Deleted > 0, Type: typ, ID: id}, nil
}
