package tools

import (
	"context"
	"io"
	"net/http"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// mediaMedia is the collection type of every collected item with its
// media details.
const mediaMedia = "media"

var collectionTypes = []string{mediaMovies, mediaShows, mediaEpisodes, mediaMedia}

// CollectionArgs are the arguments of list_collection.
type CollectionArgs struct {
	Type string `json:"type" jsonschema:"plural: movies, shows, episodes or media (singular movie, show or episode also accepted)"`
	PageArgs
}

// CollectedItem is one entry of the user's collection. For a show,
// CollectedEpisodes counts its collected episodes.
type CollectedItem struct {
	CollectedAt       string `json:"collected_at,omitempty"`
	UpdatedAt         string `json:"updated_at,omitempty"`
	CollectedEpisodes int    `json:"collected_episodes,omitempty"`
	MediaRef
}

// AddCollectionArgs are the arguments of add_to_collection.
type AddCollectionArgs struct {
	Items       []ItemArg `json:"items"                  jsonschema:"1 to 50 items; a show item covers every episode, a season or episode item only that one"`
	CollectedAt string    `json:"collected_at,omitempty" jsonschema:"when they were collected, ISO 8601 (2024-01-31T20:00:00.000Z); default now"`
}

func registerCollectionTools(s *Server) {
	Register(s, Spec{
		Name:        "list_collection",
		Title:       "List collection",
		Description: "List what the user owns in their Trakt collection: movies, TV shows (with the number of collected episodes), episodes or media.",
		Access:      Read,
	}, fetchCollection)
	Register(s, Spec{
		Name:  "add_to_collection",
		Title: "Add to collection",
		Description: "Add up to 50 movies, shows, seasons or episodes to the user's collection, now or at collected_at. " +
			"Returns how many were added or already there, and which items Trakt did not find.",
		Access: Write,
	}, addToCollection)
	Register(s, Spec{
		Name:  "remove_from_collection",
		Title: "Remove from collection",
		Description: "Remove up to 50 movies, shows, seasons or episodes from the user's collection. " +
			"Returns how many were deleted and which items Trakt did not find.",
		Access: Destroy,
	}, removeFromCollection)
}

type traktCollected struct {
	CollectedAt     *string `json:"collected_at"`
	LastCollectedAt *string `json:"last_collected_at"`
	UpdatedAt       *string `json:"updated_at"`
	LastUpdatedAt   *string `json:"last_updated_at"`
	Seasons         []struct {
		Episodes []struct{} `json:"episodes"`
	} `json:"seasons"`
	traktMedia
}

// trimCollected keeps a show's count of collected episodes, not the
// per-episode detail, which is large for long shows.
func trimCollected(c traktCollected) CollectedItem {
	episodes := 0
	for _, s := range c.Seasons {
		episodes += len(s.Episodes)
	}

	collectedAt := deref(c.CollectedAt)
	if collectedAt == "" {
		collectedAt = deref(c.LastCollectedAt)
	}

	updatedAt := deref(c.UpdatedAt)
	if updatedAt == "" {
		updatedAt = deref(c.LastUpdatedAt)
	}

	return CollectedItem{CollectedAt: collectedAt, UpdatedAt: updatedAt, CollectedEpisodes: episodes, MediaRef: c.trim()}
}

func fetchCollection(ctx context.Context, c *trakt.UserClient, in CollectionArgs) (Listing[CollectedItem], error) {
	typ, err := oneOf(fieldType, normalizeMediaFilter(in.Type), "", collectionTypes)
	if err != nil || typ == "" {
		return Listing[CollectedItem]{}, invalidArgument(fieldType)
	}

	return paginateDecoded(ctx, in.PageArgs, trimCollected, func(page, limit int) (*http.Response, error) {
		return c.API().GetSyncCollectionAll(ctx, typ, &api.GetSyncCollectionAllParams{Page: &page, Limit: &limit})
	})
}

func addToCollection(ctx context.Context, c *trakt.UserClient, in AddCollectionArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	var collectedAt string

	if in.CollectedAt != "" {
		if collectedAt, err = parseTimestamp(fieldCollectedAt, in.CollectedAt); err != nil {
			return WriteResult{}, err
		}
	}

	for i := range items {
		items[i].CollectedAt = collectedAt
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncCollectionAddWithBody(ctx, jsonContentType, body)
	})
}

func removeFromCollection(ctx context.Context, c *trakt.UserClient, in ItemsArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncCollectionRemoveWithBody(ctx, jsonContentType, body)
	})
}
