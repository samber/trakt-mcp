package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	// maxOverviewRunes bounds free text copied from Trakt (overviews): a few
	// paragraphs answer any question, and a list of maxLimit items must not
	// flood the model's context.
	maxOverviewRunes = 600
	// maxListOverviewRunes bounds overviews inside list items: a sentence or
	// two tells movies apart, and a list of maxLimit items stays small; the
	// summary tool gives the longer text.
	maxListOverviewRunes = 200
	// truncationMark ends a bounded text so the model knows it was cut.
	truncationMark = "…"
)

// NonNil returns s, or an empty slice when s is nil, so a list is never
// JSON null in a tool output.
func NonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}

// boundText returns s as valid UTF-8 of at most n runes, ending with
// truncationMark when it was cut.
func boundText(s string, n int) string {
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	keep := n - utf8.RuneCountInString(truncationMark)

	return strings.TrimSpace(string([]rune(s)[:keep])) + truncationMark
}

// deref returns *p, or the zero value when p is nil.
func deref[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}

	return *p
}

// decodeBody parses a successful Trakt response body into v. The generated
// client also parses it, into anonymous structs; tools decode into their own
// small types instead, which keeps the mapping code short. The error never
// quotes the body.
func decodeBody(body []byte, v any) error {
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode trakt response: %w", err)
	}

	return nil
}
