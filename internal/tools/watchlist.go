package tools

import (
	"context"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/samber/trakt-mcp/internal/trakt"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// maxNotesRunes is Trakt's limit on watchlist notes (spec §8.4).
	maxNotesRunes = 500
	fieldNotes    = "notes"
	fieldSortHow  = "sort_how"

	sortHowAsc  = "asc"
	sortHowDesc = "desc"
	// watchlistSortRank is the user's own order, the default.
	watchlistSortRank = "rank"
)

var (
	watchlistTypes = []string{mediaAll, mediaMovies, mediaShows, mediaSeasons, mediaEpisodes}
	watchlistSorts = []string{watchlistSortRank, "added", "title", "released", "runtime", "popularity", "percentage", "votes"}
	sortHows       = []string{sortHowAsc, sortHowDesc}
)

// WatchlistArgs are the arguments of list_watchlist.
type WatchlistArgs struct {
	Type    string `json:"type,omitempty"     jsonschema:"plural: all, movies, shows, seasons or episodes (default all)"`
	Sort    string `json:"sort,omitempty"     jsonschema:"rank, added, title, released, runtime, popularity, percentage or votes (default rank)"`
	SortHow string `json:"sort_how,omitempty" jsonschema:"asc or desc (default asc)"`
	PageArgs
}

// WatchlistItem is one entry of the user's watchlist.
type WatchlistItem struct {
	Rank       int    `json:"rank"`
	ListItemID int    `json:"list_item_id,omitempty" jsonschema:"id the update_watchlist_item and reorder_watchlist tools take"`
	ListedAt   string `json:"listed_at,omitempty"`
	Type       string `json:"type"            jsonschema:"movie, show, season or episode"`
	Notes      string `json:"notes,omitempty" jsonschema:"the user's own note"`
	MediaRef
}

// AddWatchlistArgs are the arguments of add_to_watchlist.
type AddWatchlistArgs struct {
	Items []ItemArg `json:"items"           jsonschema:"1 to 50 items; a show item covers the whole show, a season or episode item only that one"`
	Notes string    `json:"notes,omitempty" jsonschema:"note stored with every item, at most 500 characters; Trakt VIP only"`
}

func registerWatchlistTools(s *Server) {
	Register(s, Spec{
		Name:        "list_watchlist",
		Title:       "List watchlist",
		Description: "List the user's watchlist, optionally of one type, sorted by rank or another field, with the user's notes.",
		Access:      Read,
	}, fetchWatchlist)
	Register(s, Spec{
		Name:  "add_to_watchlist",
		Title: "Add to watchlist",
		Description: "Add up to 50 movies, shows, seasons or episodes to the user's watchlist, optionally with a note (Trakt VIP only). " +
			"Returns how many were added or already there, and which items Trakt did not find.",
		Access: Write,
	}, addToWatchlist)
	Register(s, Spec{
		Name:  "remove_from_watchlist",
		Title: "Remove from watchlist",
		Description: "Remove up to 50 movies, shows, seasons or episodes from the user's watchlist. " +
			"Returns how many were deleted and which items Trakt did not find.",
		Access: Destroy,
	}, removeFromWatchlist)
}

type traktWatchlistItem struct {
	Rank     int     `json:"rank"`
	ID       int     `json:"id"`
	ListedAt *string `json:"listed_at"`
	Type     string  `json:"type"`
	Notes    *string `json:"notes"`
	traktMedia
}

func trimWatchlistItem(w traktWatchlistItem) WatchlistItem {
	return WatchlistItem{
		Rank:       w.Rank,
		ListItemID: w.ID,
		ListedAt:   deref(w.ListedAt),
		Type:       w.Type,
		Notes:      boundText(deref(w.Notes), maxNotesRunes),
		MediaRef:   w.trim(),
	}
}

// syncListQuery is the validated path of a sync list route.
type syncListQuery struct{ typ, sortBy, sortHow string }

// parseSyncListQuery validates the type, sort field and direction of a sync
// list route (watchlist, favorites) against the types that list accepts.
func parseSyncListQuery(types []string, typ, sortBy, sortHow string) (syncListQuery, error) {
	var (
		q   syncListQuery
		err error
	)

	if q.typ, err = oneOf(fieldType, typ, mediaAll, types); err != nil {
		return q, err
	}

	if q.sortBy, err = oneOf(fieldSort, sortBy, watchlistSortRank, watchlistSorts); err != nil {
		return q, err
	}

	q.sortHow, err = oneOf(fieldSortHow, sortHow, sortHowAsc, sortHows)

	return q, err
}

// parseNotedItems validates a batch of items with parse and stamps notes on
// each of them.
func parseNotedItems(args []ItemArg, notes string, parse func([]ItemArg) ([]Item, error)) ([]Item, error) {
	items, err := parse(args)
	if err != nil {
		return nil, err
	}

	if !utf8.ValidString(notes) || utf8.RuneCountInString(notes) > maxNotesRunes {
		return nil, invalidArgument(fieldNotes)
	}

	for i := range items {
		items[i].Notes = notes
	}

	return items, nil
}

// fetchSyncList validates the route path of a sync list, then pages through
// get's answer and trims each entry. Watchlist and favorites share it.
func fetchSyncList[T any](
	ctx context.Context,
	pages PageArgs, types []string, typ, sortBy, sortHow string, trim func(traktWatchlistItem) T,
	get func(q syncListQuery, page, limit int) (*http.Response, error),
) (Listing[T], error) {
	q, err := parseSyncListQuery(types, typ, sortBy, sortHow)
	if err != nil {
		return Listing[T]{}, err
	}

	return paginateDecoded(ctx, pages, trim, func(page, limit int) (*http.Response, error) {
		return get(q, page, limit)
	})
}

func fetchWatchlist(ctx context.Context, c *trakt.UserClient, in WatchlistArgs) (Listing[WatchlistItem], error) {
	return fetchSyncList(ctx, in.PageArgs, watchlistTypes, in.Type, in.Sort, in.SortHow, trimWatchlistItem, func(q syncListQuery, page, limit int) (*http.Response, error) {
		return c.API().GetSyncWatchlistGet(ctx, q.typ, q.sortBy, q.sortHow, &api.GetSyncWatchlistGetParams{Page: &page, Limit: &limit})
	})
}

func addToWatchlist(ctx context.Context, c *trakt.UserClient, in AddWatchlistArgs) (WriteResult, error) {
	items, err := parseNotedItems(in.Items, in.Notes, parseItems)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncWatchlistAddWithBody(ctx, jsonContentType, body)
	})
}

func removeFromWatchlist(ctx context.Context, c *trakt.UserClient, in ItemsArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	return writeItems(items, func(body io.Reader) (*http.Response, error) {
		return c.API().PostSyncWatchlistRemoveWithBody(ctx, jsonContentType, body)
	})
}
