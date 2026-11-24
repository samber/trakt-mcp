package tools

import (
	"context"
	"encoding/json"

	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// defaultLimit is the item count of a list tool called without limit
	// (spec §8): enough to answer most questions, small enough for a context
	// window.
	defaultLimit = 10
	// maxLimit caps limit (spec §8): larger lists flood the model's context.
	maxLimit = 100
	// maxAutoPages bounds the Trakt requests of one auto-paginated call. The
	// call is charged one global-budget token, so a Trakt that returns short
	// pages must not turn it into maxLimit requests.
	maxAutoPages = 10
	// fieldLimit and fieldPage name the paging arguments in tool errors.
	fieldLimit = "limit"
	fieldPage  = "page"
)

// PageArgs are the paging arguments of every list tool; embed it in the
// tool's input struct.
type PageArgs struct {
	Limit int  `json:"limit,omitempty" jsonschema:"number of items to return, 1 to 100 (default 10)"`
	Page  *int `json:"page,omitempty"  jsonschema:"page number from 1; omit it to collect up to limit items across pages"`
}

// Listing is the output of a list tool. Page is set only when the caller
// asked for one page.
type Listing[T any] struct {
	Items []T         `json:"items"`
	Page  *trakt.Page `json:"page,omitempty"`
}

// pageJSON is trakt.Page as tools return it.
type pageJSON struct {
	Page      int `json:"page"`
	PageCount int `json:"page_count"`
	ItemCount int `json:"item_count"`
	Limit     int `json:"limit"`
}

// MarshalJSON writes items as [] when empty and the page in snake_case,
// matching the schema Register declares.
func (l Listing[T]) MarshalJSON() ([]byte, error) {
	wire := struct {
		Items []T       `json:"items"`
		Page  *pageJSON `json:"page,omitempty"`
	}{Items: NonNil(l.Items)}

	if l.Page != nil {
		wire.Page = &pageJSON{Page: l.Page.Page, PageCount: l.Page.PageCount, ItemCount: l.Page.ItemCount, Limit: l.Page.Limit}
	}

	return json.Marshal(wire)
}

// UnmarshalJSON reads what MarshalJSON writes.
func (l *Listing[T]) UnmarshalJSON(data []byte) error {
	var wire struct {
		Items []T       `json:"items"`
		Page  *pageJSON `json:"page"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err //nolint:wrapcheck // json.Unmarshal reports the failing field
	}

	l.Items = wire.Items
	l.Page = nil

	if wire.Page != nil {
		l.Page = &trakt.Page{Page: wire.Page.Page, PageCount: wire.Page.PageCount, ItemCount: wire.Page.ItemCount, Limit: wire.Page.Limit}
	}

	return nil
}

func (l Listing[T]) resultCount() int { return len(l.Items) }

// Paginate runs fetch for a list tool (spec §8). Without args.Page it
// requests pages of args.Limit items from page 1 until it has args.Limit
// items, reaches the last page, gets an empty page or has made maxAutoPages
// requests. With args.Page it requests that page only and returns its
// metadata; a page past the last one gives an empty list, not an error.
func Paginate[T any](ctx context.Context, args PageArgs, fetch func(page, limit int) ([]T, trakt.Page, error)) (Listing[T], error) {
	limit := args.Limit
	if limit == 0 {
		limit = defaultLimit
	}

	if limit < 1 || limit > maxLimit {
		return Listing[T]{}, invalidArgument(fieldLimit)
	}

	if args.Page != nil {
		return singlePage(*args.Page, limit, fetch)
	}

	items := []T{}

	for page := 1; page <= maxAutoPages; page++ {
		if err := ctx.Err(); err != nil {
			return Listing[T]{}, err //nolint:wrapcheck // mapError recognizes context errors
		}

		got, meta, err := fetch(page, limit)
		if err != nil {
			return Listing[T]{}, err
		}

		items = append(items, got...)
		// A zero PageCount means Trakt sent no pagination headers: the
		// endpoint is not paginated, so this page is the only one.
		if len(items) >= limit || len(got) == 0 || meta.PageCount == 0 || page >= meta.PageCount {
			break
		}
	}

	if len(items) > limit {
		items = items[:limit]
	}

	return Listing[T]{Items: items}, nil
}

func singlePage[T any](page, limit int, fetch func(page, limit int) ([]T, trakt.Page, error)) (Listing[T], error) {
	if page < 1 {
		return Listing[T]{}, invalidArgument(fieldPage)
	}

	items, meta, err := fetch(page, limit)
	if err != nil {
		return Listing[T]{}, err
	}

	if meta.PageCount > 0 && page > meta.PageCount {
		items = nil
	}

	return Listing[T]{Items: NonNil(items), Page: &meta}, nil
}
