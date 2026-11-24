package trakt

import (
	"net/http"
	"strconv"
)

// Trakt pagination response headers.
const (
	headerPage      = "X-Pagination-Page"
	headerLimit     = "X-Pagination-Limit"
	headerPageCount = "X-Pagination-Page-Count"
	headerItemCount = "X-Pagination-Item-Count"
)

// Page is Trakt's pagination state for a list response. A zero field means
// the header was missing or malformed.
type Page struct {
	Page      int
	PageCount int
	ItemCount int
	Limit     int
}

// ParsePage reads the X-Pagination-* headers. Missing, malformed or negative
// values give zero fields, never an error: pagination is advisory.
func ParsePage(h http.Header) Page {
	return Page{
		Page:      headerInt(h, headerPage),
		PageCount: headerInt(h, headerPageCount),
		ItemCount: headerInt(h, headerItemCount),
		Limit:     headerInt(h, headerLimit),
	}
}

func headerInt(h http.Header, key string) int {
	n, err := strconv.Atoi(h.Get(key))
	if err != nil || n < 0 {
		return 0
	}

	return n
}
