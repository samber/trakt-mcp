package tools

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// fakePages serves total items numbered from 1, at most pageSize per page
// whatever limit the caller asks, as Trakt clamps limits per endpoint.
type fakePages struct {
	total, pageSize int
	calls           []int
}

func (f *fakePages) fetch(page, _ int) ([]int, trakt.Page, error) {
	f.calls = append(f.calls, page)

	pageCount := (f.total + f.pageSize - 1) / f.pageSize
	meta := trakt.Page{Page: page, PageCount: pageCount, ItemCount: f.total, Limit: f.pageSize}

	start := (page - 1) * f.pageSize
	if start >= f.total {
		// Trakt answers an empty array past the last page.
		return nil, meta, nil
	}

	var items []int
	for i := start; i < min(start+f.pageSize, f.total); i++ {
		items = append(items, i+1)
	}

	return items, meta, nil
}

func TestPaginateAutoUntilLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name             string
		total, pageSize  int
		limit            int
		wantItems, pages int
	}{
		{"spans pages", 20, 3, 7, 7, 3},
		{"stops at last page", 5, 3, 50, 5, 2},
		{"one page enough", 20, 50, 10, 10, 1},
		{"default limit", 30, 100, 0, defaultLimit, 1},
		{"empty", 0, 10, 10, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			f := &fakePages{total: c.total, pageSize: c.pageSize}

			got, err := Paginate(t.Context(), PageArgs{Limit: c.limit}, f.fetch)
			if err != nil {
				t.Fatal(err)
			}

			if len(got.Items) != c.wantItems || len(f.calls) != c.pages {
				t.Fatalf("items = %d over %d pages, want %d over %d", len(got.Items), len(f.calls), c.wantItems, c.pages)
			}

			for i, v := range got.Items {
				if v != i+1 {
					t.Fatalf("items = %v, want 1..n in order", got.Items)
				}
			}

			if got.Page != nil {
				t.Errorf("page = %+v, want nil when auto-paginating", got.Page)
			}

			if got.Items == nil {
				t.Error("items is nil, want an empty slice")
			}
		})
	}
}

func TestPaginateAutoBoundsUpstreamCalls(t *testing.T) {
	t.Parallel()

	// A Trakt that answers one item per page must not cost one request per item.
	f := &fakePages{total: maxLimit, pageSize: 1}

	got, err := Paginate(t.Context(), PageArgs{Limit: maxLimit}, f.fetch)
	if err != nil {
		t.Fatal(err)
	}

	if len(f.calls) != maxAutoPages || len(got.Items) != maxAutoPages {
		t.Fatalf("calls = %d, items = %d; want %d of each", len(f.calls), len(got.Items), maxAutoPages)
	}
}

func TestPaginateSinglePageMetadata(t *testing.T) {
	t.Parallel()

	f := &fakePages{total: 25, pageSize: 10}

	got, err := Paginate(t.Context(), PageArgs{Limit: 10, Page: new(2)}, f.fetch)
	if err != nil {
		t.Fatal(err)
	}

	if len(f.calls) != 1 || f.calls[0] != 2 {
		t.Fatalf("calls = %v, want exactly page 2", f.calls)
	}

	if len(got.Items) != 10 || got.Items[0] != 11 {
		t.Fatalf("items = %v, want 11..20", got.Items)
	}

	want := trakt.Page{Page: 2, PageCount: 3, ItemCount: 25, Limit: 10}
	if got.Page == nil || *got.Page != want {
		t.Fatalf("page = %+v, want %+v", got.Page, want)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	var decoded struct {
		Page map[string]int `json:"page"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	for key, v := range map[string]int{"page": 2, "page_count": 3, "item_count": 25, fieldLimit: 10} {
		if decoded.Page[key] != v {
			t.Errorf("JSON %s: %s = %d, want %d", raw, key, decoded.Page[key], v)
		}
	}
}

func TestPaginatePastLastPageEmpty(t *testing.T) {
	t.Parallel()

	f := &fakePages{total: 25, pageSize: 10}

	got, err := Paginate(t.Context(), PageArgs{Page: new(9)}, f.fetch)
	if err != nil {
		t.Fatalf("err = %v, want an empty page", err)
	}

	if got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("items = %#v, want an empty non-nil slice", got.Items)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}

	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	if string(decoded["items"]) != "[]" {
		t.Errorf("items JSON = %s, want []", decoded["items"])
	}
}

func TestPaginateRejectsBadArgs(t *testing.T) {
	t.Parallel()

	cases := []struct {
		args  PageArgs
		field string
	}{
		{PageArgs{Limit: -1}, "limit"},
		{PageArgs{Limit: maxLimit + 1}, "limit"},
		{PageArgs{Page: new(0)}, "page"},
		{PageArgs{Page: new(-3)}, "page"},
	}
	for _, c := range cases {
		f := &fakePages{total: 5, pageSize: 5}

		_, err := Paginate(t.Context(), c.args, f.fetch)
		if err == nil || err.Error() != "invalid argument: "+c.field {
			t.Errorf("Paginate(%+v) err = %v, want invalid argument: %s", c.args, err, c.field)
		}

		if len(f.calls) != 0 {
			t.Errorf("Paginate(%+v) called Trakt", c.args)
		}
	}

	f := &fakePages{total: 5, pageSize: 5}
	if _, err := Paginate(t.Context(), PageArgs{Limit: maxLimit}, f.fetch); err != nil {
		t.Errorf("limit %d refused: %v", maxLimit, err)
	}
}

func TestPaginateStopsOnErrorAndCancel(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	calls := 0

	_, err := Paginate(t.Context(), PageArgs{Limit: 10}, func(int, int) ([]int, trakt.Page, error) {
		calls++
		if calls == 2 {
			return nil, trakt.Page{}, boom
		}

		return []int{1}, trakt.Page{PageCount: 5}, nil
	})
	if !errors.Is(err, boom) || calls != 2 {
		t.Fatalf("err = %v after %d calls, want boom after 2", err, calls)
	}

	ctx, cancel := context.WithCancel(t.Context())
	calls = 0

	_, err = Paginate(ctx, PageArgs{Limit: 10}, func(int, int) ([]int, trakt.Page, error) {
		calls++

		cancel()

		return []int{1}, trakt.Page{PageCount: 5}, nil
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err = %v after %d calls, want context.Canceled after 1", err, calls)
	}
}
