package tools

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

// favoriteTypes are the media types Trakt favorites hold: movies and shows only.
var favoriteTypes = []string{mediaAll, mediaMovies, mediaShows}

// favoritesNature is the description sentence shared by every favorites tool.
const favoritesNature = "The favorites are the user's own top list on Trakt (like a desert-island list) of movies and TV shows, not a sync with other platforms. "

// FavoritesArgs are the arguments of list_favorites.
type FavoritesArgs struct {
	Type    string `json:"type,omitempty"     jsonschema:"plural: all, movies or shows (default all)"`
	Sort    string `json:"sort,omitempty"     jsonschema:"rank, added, title, released, runtime, popularity, percentage or votes (default rank)"`
	SortHow string `json:"sort_how,omitempty" jsonschema:"asc or desc (default asc)"`
	PageArgs
}

// FavoriteItem is one entry of the user's favorites.
type FavoriteItem struct {
	Rank       int    `json:"rank"`
	ListItemID int    `json:"list_item_id,omitempty" jsonschema:"id the update_favorite and reorder_favorites tools take"`
	ListedAt   string `json:"listed_at,omitempty"`
	Type       string `json:"type"            jsonschema:"movie or show"`
	Notes      string `json:"notes,omitempty" jsonschema:"the user's own note"`
	MediaRef
}

// AddFavoritesArgs are the arguments of add_to_favorites.
type AddFavoritesArgs struct {
	Items []ItemArg `json:"items"           jsonschema:"1 to 50 items; movies or shows only"`
	Notes string    `json:"notes,omitempty" jsonschema:"note stored with every item, at most 500 characters"`
}

// RemoveFavoritesArgs are the arguments of remove_from_favorites.
type RemoveFavoritesArgs struct {
	Items []ItemArg `json:"items" jsonschema:"1 to 50 items; movies or shows only"`
}

// UpdateFavoriteArgs are the arguments of update_favorite.
type UpdateFavoriteArgs struct {
	ListItemID int     `json:"list_item_id"    jsonschema:"list_item_id of a list_favorites item"`
	Notes      *string `json:"notes,omitempty" jsonschema:"new note, at most 500 characters; empty clears it"`
	Rank       *int    `json:"rank,omitempty"  jsonschema:"new position, 1 or more"`
}

// ReorderFavoritesArgs are the arguments of reorder_favorites.
type ReorderFavoritesArgs struct {
	Rank []int `json:"rank" jsonschema:"list_item_id values of list_favorites items in the wanted order, first is rank 1; no duplicates"`
}

// UpdateFavoriteResult confirms an updated favorite.
type UpdateFavoriteResult struct {
	Updated bool `json:"updated"`
}

func (UpdateFavoriteResult) resultCount() int { return 1 }

// ReorderFavoritesResult is the outcome of a reorder.
type ReorderFavoritesResult struct {
	Updated    int    `json:"updated"`
	SkippedIDs []int  `json:"skipped_ids" jsonschema:"ids Trakt did not reorder"`
	ItemCount  int    `json:"item_count"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

func (r ReorderFavoritesResult) resultCount() int { return r.Updated }

// updateFavoriteBody is built as a map so that an absent field is left out
// while a cleared note is sent as JSON null.
type updateFavoriteBody map[string]any

func registerFavoriteTools(s *Server) {
	Register(s, Spec{
		Name: "list_favorites", Title: "List favorites", Access: Read,
		Description: favoritesNature + "List the user's favorites, optionally of one type, sorted by rank or another field, with the user's notes. " +
			"Takes limit and page." + untrustedNote,
	}, fetchFavorites)
	Register(s, Spec{
		Name: "add_to_favorites", Title: "Add to favorites", Access: Write,
		Description: favoritesNature + "Add up to 50 movies or shows to the favorites, optionally with a note. Seasons and episodes are not accepted. " +
			"Trakt caps the number of favorites: past the cap it answers 420, reported as an account limit error. " +
			"Returns how many were added or already there, and which items Trakt did not find.",
	}, addToFavorites)
	Register(s, Spec{
		Name: "update_favorite", Title: "Update favorite", Access: Write,
		Description: favoritesNature + "Update one favorite: set or clear its note and/or move it to a new rank. It is addressed by the list_item_id that list_favorites returns. " +
			"Give at least one of notes and rank.",
	}, updateFavorite)
	Register(s, Spec{
		Name: "reorder_favorites", Title: "Reorder favorites", Access: Write,
		Description: favoritesNature + "Reorder favorites by sending list_item_id values from list_favorites in the wanted order. " +
			"Returns how many entries moved and which ids Trakt skipped.",
	}, reorderFavorites)
	Register(s, Spec{
		Name: "remove_from_favorites", Title: "Remove from favorites", Access: Destroy,
		Description: favoritesNature + "Remove up to 50 movies or shows from the favorites. " +
			"Returns how many were deleted and which items Trakt did not find.",
	}, removeFromFavorites)
}

func trimFavoriteItem(w traktWatchlistItem) FavoriteItem {
	return FavoriteItem{
		Rank:       w.Rank,
		ListItemID: w.ID,
		ListedAt:   deref(w.ListedAt),
		Type:       w.Type,
		Notes:      boundText(deref(w.Notes), maxNotesRunes),
		MediaRef:   w.trim(),
	}
}

func fetchFavorites(ctx context.Context, c *trakt.UserClient, in FavoritesArgs) (Listing[FavoriteItem], error) {
	return fetchSyncList(ctx, in.PageArgs, favoriteTypes, in.Type, in.Sort, in.SortHow, trimFavoriteItem, func(q syncListQuery, page, limit int) (*http.Response, error) {
		return c.API().GetSyncFavoritesGet(ctx, q.typ, q.sortBy, q.sortHow, &api.GetSyncFavoritesGetParams{Page: &page, Limit: &limit})
	})
}

// parseFavoriteItems validates the items of a favorites write: movies and
// shows only, since Trakt favorites hold nothing else.
func parseFavoriteItems(args []ItemArg) ([]Item, error) {
	items, err := parseItems(args)
	if err != nil {
		return nil, err
	}

	for _, it := range items {
		if it.Type != itemMovie && it.Type != itemShow {
			return nil, invalidArgument(fieldItems)
		}
	}

	return items, nil
}

func addToFavorites(ctx context.Context, c *trakt.UserClient, in AddFavoritesArgs) (WriteResult, error) {
	items, err := parseNotedItems(in.Items, in.Notes, parseFavoriteItems)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncFavoritesAddWithBody(ctx, jsonContentType, body)
	})
}

func removeFromFavorites(ctx context.Context, c *trakt.UserClient, in RemoveFavoritesArgs) (WriteResult, error) {
	items, err := parseFavoriteItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncFavoritesRemoveWithBody(ctx, jsonContentType, body)
	})
}

func updateFavorite(ctx context.Context, c *trakt.UserClient, in UpdateFavoriteArgs) (UpdateFavoriteResult, error) {
	if in.ListItemID <= 0 {
		return UpdateFavoriteResult{}, invalidArgument(fieldListItemID)
	}

	if in.Notes == nil && in.Rank == nil {
		return UpdateFavoriteResult{}, invalidArgument(fieldNotesOrRank)
	}

	if in.Notes != nil && (!utf8.ValidString(*in.Notes) || utf8.RuneCountInString(*in.Notes) > maxNotesRunes) {
		return UpdateFavoriteResult{}, invalidArgument(fieldNotes)
	}

	if in.Rank != nil && *in.Rank <= 0 {
		return UpdateFavoriteResult{}, invalidArgument(fieldRank)
	}

	body := updateFavoriteBody{}
	if in.Notes != nil {
		body[fieldNotes] = notesOrNull(*in.Notes)
	}

	if in.Rank != nil {
		body[fieldRank] = *in.Rank
	}

	if _, err := sendJSON(body, func(b io.Reader) (*http.Response, error) {
		return c.API().PutSyncFavoritesUpdateItemWithBody(ctx, strconv.Itoa(in.ListItemID), jsonContentType, b)
	}); err != nil {
		return UpdateFavoriteResult{}, err
	}

	return UpdateFavoriteResult{Updated: true}, nil
}

func reorderFavorites(ctx context.Context, c *trakt.UserClient, in ReorderFavoritesArgs) (ReorderFavoritesResult, error) {
	r, err := sendReorder(in.Rank, func(b io.Reader) (*http.Response, error) {
		return c.API().PostSyncFavoritesReorderWithBody(ctx, jsonContentType, b)
	})

	return ReorderFavoritesResult(r), err
}
