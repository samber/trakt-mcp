package tools

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// Argument names of the watchlist edit tools, as they appear in tool errors.
const (
	fieldListItemID = "list_item_id"
	fieldRank       = "rank"
)

// maxReorderIDs bounds the ids of one reorder call. UNVERIFIED: the filtered
// spec states no limit; this keeps a mistaken call small while exceeding
// any watchlist size Trakt is known to allow.
const maxReorderIDs = 1000

// vipNotesNote tells the assistant that notes are a paid Trakt feature.
const vipNotesNote = "Watchlist notes need Trakt VIP."

// UpdateWatchlistItemArgs are the arguments of update_watchlist_item.
type UpdateWatchlistItemArgs struct {
	ListItemID int    `json:"list_item_id" jsonschema:"list_item_id of a fetch_watchlist item"`
	Notes      string `json:"notes"        jsonschema:"new note, at most 500 characters; empty clears it; Trakt VIP only"`
}

// ReorderWatchlistArgs are the arguments of reorder_watchlist.
type ReorderWatchlistArgs struct {
	Rank []int `json:"rank" jsonschema:"list_item_id values of fetch_watchlist items in the wanted order, first is rank 1; no duplicates"`
}

// UpdateWatchlistItemResult confirms an updated watchlist item.
type UpdateWatchlistItemResult struct {
	Updated bool `json:"updated"`
}

func (UpdateWatchlistItemResult) resultCount() int { return 1 }

// ReorderWatchlistResult is the outcome of a reorder.
type ReorderWatchlistResult struct {
	Updated    int    `json:"updated"`
	SkippedIDs []int  `json:"skipped_ids" jsonschema:"ids Trakt did not reorder"`
	ItemCount  int    `json:"item_count"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

func (r ReorderWatchlistResult) resultCount() int { return r.Updated }

type updateWatchlistItemBody struct {
	Notes string `json:"notes"`
}

type reorderWatchlistBody struct {
	Rank []int `json:"rank"`
}

type traktReorderResult struct {
	Updated    int   `json:"updated"`
	SkippedIDs []int `json:"skipped_ids"`
	List       struct {
		ItemCount int     `json:"item_count"`
		UpdatedAt *string `json:"updated_at"`
	} `json:"list"`
}

func registerWatchlistEditTools(s *Server) {
	Register(s, Spec{
		Name: "update_watchlist_item", Title: "Update watchlist item", Access: Write,
		Description: "Set or clear the note of one watchlist entry, addressed by the list_item_id that fetch_watchlist returns. " + vipNotesNote,
	}, updateWatchlistItem)
	Register(s, Spec{
		Name: "reorder_watchlist", Title: "Reorder watchlist", Access: Write,
		Description: "Reorder the user's whole watchlist by sending list_item_id values from fetch_watchlist in the wanted order. " +
			"Returns how many entries moved and which ids Trakt skipped. " + vipNotesNote,
	}, reorderWatchlist)
}

func updateWatchlistItem(ctx context.Context, c *trakt.UserClient, in UpdateWatchlistItemArgs) (UpdateWatchlistItemResult, error) {
	if in.ListItemID <= 0 {
		return UpdateWatchlistItemResult{}, invalidArgument(fieldListItemID)
	}

	if !utf8.ValidString(in.Notes) || utf8.RuneCountInString(in.Notes) > maxNotesRunes {
		return UpdateWatchlistItemResult{}, invalidArgument(fieldNotes)
	}

	if _, err := sendJSON(updateWatchlistItemBody{Notes: in.Notes}, func(b io.Reader) (*http.Response, error) {
		return c.API().PutSyncWatchlistUpdateItemWithBody(ctx, strconv.Itoa(in.ListItemID), jsonContentType, b)
	}); err != nil {
		return UpdateWatchlistItemResult{}, err
	}

	return UpdateWatchlistItemResult{Updated: true}, nil
}

func reorderWatchlist(ctx context.Context, c *trakt.UserClient, in ReorderWatchlistArgs) (ReorderWatchlistResult, error) {
	return sendReorder(in.Rank, func(b io.Reader) (*http.Response, error) {
		return c.API().PostSyncWatchlistReorderWithBody(ctx, jsonContentType, b)
	})
}

// sendReorder validates the list item ids of a reorder (1 to maxReorderIDs,
// positive, distinct), posts them in order with send and decodes Trakt's
// outcome. Watchlist and favorites share it.
func sendReorder(ids []int, send func(io.Reader) (*http.Response, error)) (ReorderWatchlistResult, error) {
	if len(ids) == 0 || len(ids) > maxReorderIDs {
		return ReorderWatchlistResult{}, invalidArgument(fieldRank)
	}

	seen := make(map[int]struct{}, len(ids))

	for _, id := range ids {
		if _, dup := seen[id]; dup || id <= 0 {
			return ReorderWatchlistResult{}, invalidArgument(fieldRank)
		}

		seen[id] = struct{}{}
	}

	r, err := sendJSON(reorderWatchlistBody{Rank: ids}, send)
	if err != nil {
		return ReorderWatchlistResult{}, err
	}

	raw, err := decodeAs[traktReorderResult](r.body)
	if err != nil {
		return ReorderWatchlistResult{}, err
	}

	skipped := raw.SkippedIDs
	if skipped == nil {
		skipped = []int{}
	}

	return ReorderWatchlistResult{
		Updated: raw.Updated, SkippedIDs: skipped, ItemCount: raw.List.ItemCount, UpdatedAt: deref(raw.List.UpdatedAt),
	}, nil
}
