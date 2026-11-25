package tools

import (
	"context"

	"github.com/samber/trakt-mcp/internal/trakt"
)

// fieldKind is the name of the argument that picks which feed a merged tool
// reads (a calendar, a list ranking).
const fieldKind = "kind"

// kindHandler is the handler of one value of the kind argument. A tool lists
// its kinds in a slice of these, so a kind is looked up by name and a
// reordering of the slice cannot swap two handlers.
type kindHandler[A, R any] struct {
	kind string
	call func(context.Context, *trakt.UserClient, A) (R, error)
}

// callKind runs the handler whose kind equals kind. The match is exact: no
// default, no case folding, so "" and "Shows" are refused before any handler
// (and so any Trakt call) runs.
func callKind[A, R any](ctx context.Context, c *trakt.UserClient, kind string, handlers []kindHandler[A, R], in A) (R, error) {
	for _, h := range handlers {
		if h.kind == kind {
			return h.call(ctx, c, in)
		}
	}

	var zero R

	return zero, invalidArgument(fieldKind)
}
