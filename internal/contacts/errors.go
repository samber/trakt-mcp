package contacts

import (
	"errors"
	"fmt"
	"math"
	"time"
)

var (
	// ErrNotFound is returned when Google answers 404 for a resource name.
	ErrNotFound = errors.New("contact not found")
	// ErrConflict is returned when the etag sent on update is stale.
	ErrConflict = errors.New("contact changed, re-read it")
	// ErrUpstreamAuth is returned when the user's Google grant is revoked or
	// expired; callers answer HTTP 401 instead of a tool error.
	ErrUpstreamAuth = errors.New("google authorization is no longer valid")
	// ErrInvalidArgument is returned when Google rejects a request as
	// malformed (HTTP 400 other than a stale etag), e.g. a bad page token.
	ErrInvalidArgument = errors.New("invalid argument")
)

// RateLimitedError reports a Google 429 or a local rate-limit refusal.
// It uses a value receiver so errors.As works with a RateLimitedError target.
type RateLimitedError struct {
	RetryAfter time.Duration
}

func (e RateLimitedError) Error() string {
	// Round up so the caller never retries before the limit actually resets.
	return fmt.Sprintf("rate limited, retry after %ds", int64(math.Ceil(e.RetryAfter.Seconds())))
}

// UpstreamStatusError reports a People API answer with an HTTP status that
// maps to no other error. It carries only the operation and the status, never
// Google's response body, so it is safe to show to the assistant.
type UpstreamStatusError struct {
	Op     string
	Status int
}

func (e UpstreamStatusError) Error() string {
	return fmt.Sprintf("people %s: google returned HTTP %d", e.Op, e.Status)
}

// UnreachableError reports a People API call that got no answer from Google:
// a timeout, a refused or dropped connection, a DNS failure. Err is the
// network-level cause only, never the request URL, which can hold a search
// query.
type UnreachableError struct {
	Op  string
	Err error
}

func (e UnreachableError) Error() string {
	return fmt.Sprintf("people %s: google unreachable: %v", e.Op, e.Err)
}

func (e UnreachableError) Unwrap() error { return e.Err }

// GroupError reports a group name that matched no user group (Matches 0) or
// several. It names only the group the caller gave.
type GroupError struct {
	Group   string
	Matches int
}

func (e GroupError) Error() string {
	if e.Matches == 0 {
		return fmt.Sprintf("group %q not found", e.Group)
	}

	return fmt.Sprintf("group %q is ambiguous (%d matches)", e.Group, e.Matches)
}
