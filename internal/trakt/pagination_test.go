package trakt

import (
	"io"
	"net/http"
	"testing"
)

func TestPaginationHeaders(t *testing.T) {
	t.Parallel()

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Pagination-Page", "2")
		w.Header().Set("X-Pagination-Limit", "10")
		w.Header().Set("X-Pagination-Page-Count", "5")
		w.Header().Set("X-Pagination-Item-Count", "47")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[]")
	})

	rsp, err := c.GetMoviesTrendingWithResponse(t.Context(), nil)
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}

	got := ParsePage(rsp.HTTPResponse.Header)
	want := Page{Page: 2, PageCount: 5, ItemCount: 47, Limit: 10}

	if got != want {
		t.Fatalf("ParsePage = %+v, want %+v", got, want)
	}
}

func TestMalformedPaginationHeadersIgnored(t *testing.T) {
	t.Parallel()

	tests := map[string]http.Header{
		"missing": {},
		"garbage": {
			"X-Pagination-Page":       {"two"},
			"X-Pagination-Limit":      {""},
			"X-Pagination-Page-Count": {"1.5"},
			"X-Pagination-Item-Count": {"99999999999999999999999"},
		},
		"negative": {
			"X-Pagination-Page":       {"-1"},
			"X-Pagination-Limit":      {"-10"},
			"X-Pagination-Page-Count": {"-3"},
			"X-Pagination-Item-Count": {"-4"},
		},
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := ParsePage(h); got != (Page{}) {
				t.Fatalf("ParsePage = %+v, want zero", got)
			}
		})
	}

	if got := ParsePage(nil); got != (Page{}) {
		t.Fatalf("ParsePage(nil) = %+v, want zero", got)
	}
}
