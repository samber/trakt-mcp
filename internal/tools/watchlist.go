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

// WatchlistArgs are the arguments of fetch_watchlist.
type WatchlistArgs struct {
	Type    string `json:"type,omitempty"     jsonschema:"all, movies, shows, seasons or episodes (default all)"`
	Sort    string `json:"sort,omitempty"     jsonschema:"rank, added, title, released, runtime, popularity, percentage or votes (default rank)"`
	SortHow string `json:"sort_how,omitempty" jsonschema:"asc or desc (default asc)"`
	PageArgs
}

// WatchlistItem is one entry of the user's watchlist.
type WatchlistItem struct {
	Rank     int    `json:"rank"`
	ListedAt string `json:"listed_at,omitempty"`
	Type     string `json:"type"            jsonschema:"movie, show, season or episode"`
	Notes    string `json:"notes,omitempty" jsonschema:"the user's own note"`
	MediaRef
}

// AddWatchlistArgs are the arguments of add_to_watchlist.
type AddWatchlistArgs struct {
	Items []ItemArg `json:"items"           jsonschema:"1 to 50 items; a show item covers the whole show, a season or episode item only that one"`
	Notes string    `json:"notes,omitempty" jsonschema:"note stored with every item, at most 500 characters; Trakt VIP only"`
}

func registerWatchlistTools(s *Server) {
	Register(s, Spec{
		Name:        "fetch_watchlist",
		Title:       "Fetch watchlist",
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
	ListedAt *string `json:"listed_at"`
	Type     string  `json:"type"`
	Notes    *string `json:"notes"`
	traktMedia
}

func trimWatchlistItem(w traktWatchlistItem) WatchlistItem {
	return WatchlistItem{
		Rank:     w.Rank,
		ListedAt: deref(w.ListedAt),
		Type:     w.Type,
		Notes:    boundText(deref(w.Notes), maxNotesRunes),
		MediaRef: w.trim(),
	}
}

func fetchWatchlist(ctx context.Context, c *trakt.UserClient, in WatchlistArgs) (Listing[WatchlistItem], error) {
	typ, err := oneOf(fieldType, in.Type, mediaAll, watchlistTypes)
	if err != nil {
		return Listing[WatchlistItem]{}, err
	}

	sortBy, err := oneOf(fieldSort, in.Sort, watchlistSortRank, watchlistSorts)
	if err != nil {
		return Listing[WatchlistItem]{}, err
	}

	sortHow, err := oneOf(fieldSortHow, in.SortHow, sortHowAsc, sortHows)
	if err != nil {
		return Listing[WatchlistItem]{}, err
	}

	return paginateDecoded(ctx, in.PageArgs, trimWatchlistItem, func(page, limit int) (*http.Response, error) {
		return c.API().GetSyncWatchlistGet(ctx, typ, sortBy, sortHow, &api.GetSyncWatchlistGetParams{Page: &page, Limit: &limit})
	})
}

func addToWatchlist(ctx context.Context, c *trakt.UserClient, in AddWatchlistArgs) (WriteResult, error) {
	items, err := parseItems(in.Items)
	if err != nil {
		return WriteResult{}, err
	}

	if !utf8.ValidString(in.Notes) || utf8.RuneCountInString(in.Notes) > maxNotesRunes {
		return WriteResult{}, invalidArgument(fieldNotes)
	}

	for i := range items {
		items[i].Notes = in.Notes
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
