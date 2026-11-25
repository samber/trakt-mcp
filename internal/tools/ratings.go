package tools

import (
	"context"
	"io"
	"net/http"
	"strconv"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// minRating and maxRating bound Trakt's rating scale.
	minRating   = 1
	maxRating   = 10
	fieldRating = "rating"
)

var ratingTypes = []string{mediaAll, mediaMovies, mediaShows, mediaSeasons, mediaEpisodes}

// RatingsArgs are the arguments of list_user_ratings.
type RatingsArgs struct {
	Type   string `json:"type,omitempty"   jsonschema:"plural: all, movies, shows, seasons or episodes (shows are TV shows; singular movie, show, season or episode also accepted); omit for all"`
	Rating *int   `json:"rating,omitempty" jsonschema:"only items rated this, 1 to 10"`
	PageArgs
}

// RatedItem is one item the user rated.
type RatedItem struct {
	Rating  int    `json:"rating"`
	RatedAt string `json:"rated_at,omitempty"`
	Type    string `json:"type" jsonschema:"movie, show (a TV show), season or episode"`
	MediaRef
}

// AddRatingsArgs are the arguments of add_ratings.
type AddRatingsArgs struct {
	Items  []ItemArg `json:"items"  jsonschema:"1 to 50 items to rate"`
	Rating int       `json:"rating" jsonschema:"rating given to every item, 1 to 10"`
}

func registerRatingTools(s *Server) {
	Register(s, Spec{
		Name:        "list_user_ratings",
		Title:       "List my ratings",
		Description: "List what the user rated (movies, TV shows, seasons, episodes), optionally of one type or one rating value, with the rating and when it was given. Trakt-wide average ratings are in get_ratings.",
		Access:      Read,
	}, fetchRatings)
	Register(s, Spec{
		Name:  "add_ratings",
		Title: "Add ratings",
		Description: "Rate up to 50 movies, TV shows, seasons or episodes 1 to 10, replacing any earlier rating. " +
			"Returns how many were rated and which items Trakt did not find.",
		Access: Write,
	}, addRatings)
	Register(s, Spec{
		Name:  "remove_ratings",
		Title: "Remove ratings",
		Description: "Remove the user's rating of up to 50 movies, TV shows, seasons or episodes. " +
			"Returns how many were deleted and which items Trakt did not find.",
		Access: Destroy,
	}, removeRatings)
}

type traktRated struct {
	Rating  int     `json:"rating"`
	RatedAt *string `json:"rated_at"`
	Type    string  `json:"type"`
	traktMedia
}

func trimRated(r traktRated) RatedItem {
	return RatedItem{Rating: r.Rating, RatedAt: deref(r.RatedAt), Type: r.Type, MediaRef: r.trim()}
}

// ratingArg validates a rating value.
func ratingArg(r int) error {
	if r < minRating || r > maxRating {
		return invalidArgument(fieldRating)
	}

	return nil
}

func fetchRatings(ctx context.Context, c *trakt.UserClient, in RatingsArgs) (Listing[RatedItem], error) {
	typ, err := oneOf(fieldType, normalizeMediaFilter(in.Type), "", ratingTypes)
	if err != nil {
		return Listing[RatedItem]{}, err
	}

	if in.Rating != nil {
		if err := ratingArg(*in.Rating); err != nil {
			return Listing[RatedItem]{}, err
		}

		// The full form needs a type in its path.
		if typ == "" {
			typ = mediaAll
		}
	}

	return paginateDecoded(ctx, in.PageArgs, trimRated, func(page, limit int) (*http.Response, error) {
		switch {
		case in.Rating != nil:
			return ratingsByValue(ctx, c, typ, *in.Rating, page, limit)
		case typ != "":
			return ratingsByType(ctx, c, typ, page, limit)
		}

		return ratingsAll(ctx, c, page, limit)
	})
}

// ratingsAll is GET /sync/ratings, Trakt's form without a type; whether
// Trakt answers it is confirmed only against the live API.
func ratingsAll(ctx context.Context, c *trakt.UserClient, page, limit int) (*http.Response, error) {
	//nolint:wrapcheck // *trakt.Error is mapped by Kind
	return c.API().GetSyncRatingsAll(ctx, &api.GetSyncRatingsAllParams{Page: &page, Limit: &limit})
}

// ratingsByType is GET /sync/ratings/{type}.
func ratingsByType(ctx context.Context, c *trakt.UserClient, typ string, page, limit int) (*http.Response, error) {
	//nolint:wrapcheck // *trakt.Error is mapped by Kind
	return c.API().GetSyncRatingsByType(ctx, typ, &api.GetSyncRatingsByTypeParams{Page: &page, Limit: &limit})
}

// ratingsByValue is GET /sync/ratings/{type}/{rating}, the full form.
func ratingsByValue(ctx context.Context, c *trakt.UserClient, typ string, rating, page, limit int) (*http.Response, error) {
	//nolint:wrapcheck // *trakt.Error is mapped by Kind
	return c.API().GetSyncRatingsGet(ctx, typ, strconv.Itoa(rating), &api.GetSyncRatingsGetParams{Page: &page, Limit: &limit})
}

func addRatings(ctx context.Context, c *trakt.UserClient, in AddRatingsArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	if err := ratingArg(in.Rating); err != nil {
		return WriteResult{}, err
	}

	for i := range items {
		rating := in.Rating
		items[i].Rating = &rating
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncRatingsAddWithBody(ctx, jsonContentType, body)
	})
}

func removeRatings(ctx context.Context, c *trakt.UserClient, in ItemsArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncRatingsRemoveWithBody(ctx, jsonContentType, body)
	})
}
