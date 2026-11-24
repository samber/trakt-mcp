package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// statusAccountLimit is Trakt's non-standard status for a list, watchlist
	// or collection cap reached by a non-VIP account.
	statusAccountLimit = 420
	// maxMessageRunes bounds how much of Trakt's error_description reaches the
	// assistant, so an upstream message cannot flood the tool result.
	maxMessageRunes = 200
	// maxErrorBodyBytes bounds how much of an error response is read, for
	// error_description and the kept 409 body; Trakt error bodies, including a
	// check-in conflict, are well under 1 KiB.
	maxErrorBodyBytes = 16 << 10
	// maxRetryAfter clamps Retry-After so a bogus upstream value never tells
	// the assistant to wait for days; Trakt's windows are at most minutes.
	maxRetryAfter = time.Hour
	// revokedMessage is the fixed text for a token Trakt still refuses after a
	// refresh; it tells the user the one action that helps.
	revokedMessage = "Trakt access was revoked; reconnect this connector"
)

// ErrUpstreamAuth means Trakt rejected the user's token even after one
// refresh: the user must reconnect.
var ErrUpstreamAuth = errors.New("trakt access was revoked; reconnect this connector")

// Kind classifies a Trakt failure for the tool layer.
type Kind int

// Failure kinds. KindUnknown covers statuses without a dedicated meaning
// (400, 403, 422 and so on).
const (
	KindUnknown Kind = iota
	KindNotFound
	KindRateLimited
	KindConflict
	KindAccountLimit
	KindLocked
	KindVIPRequired
	KindUnauthorized
	KindServer
	KindUnreachable
	KindCanceled
)

var kindNames = map[Kind]string{
	KindUnknown:      "unknown",
	KindNotFound:     "not_found",
	KindRateLimited:  "rate_limited",
	KindConflict:     "conflict",
	KindAccountLimit: "account_limit",
	KindLocked:       "locked",
	KindVIPRequired:  "vip_required",
	KindUnauthorized: "unauthorized",
	KindServer:       "server",
	KindUnreachable:  "unreachable",
	KindCanceled:     "canceled",
}

// String returns a stable snake_case name, usable as a metric label.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}

	return "kind(" + strconv.Itoa(int(k)) + ")"
}

// defaultMessages is used when Trakt sends no error_description.
var defaultMessages = map[Kind]string{
	KindUnknown:      "Trakt rejected the request",
	KindNotFound:     "not found",
	KindRateLimited:  "rate limited by Trakt",
	KindConflict:     "conflict with the current Trakt state",
	KindAccountLimit: "Trakt account limit reached",
	KindLocked:       "this Trakt account is locked",
	KindVIPRequired:  "Trakt VIP is required for this feature",
	KindUnauthorized: revokedMessage,
	KindServer:       "Trakt server error",
	KindUnreachable:  "Trakt is unreachable, retry later",
	KindCanceled:     "request canceled",
}

// Error is a Trakt failure. Message never contains the request URL or the
// response body: only a fixed text or Trakt's truncated error_description.
type Error struct {
	// Status is the HTTP status, or 0 when no response arrived.
	Status int
	Kind   Kind
	// Message is safe to show to the assistant.
	Message string
	// RetryAfter is set for KindRateLimited when Trakt sent Retry-After.
	RetryAfter time.Duration
	// cause is ErrUpstreamAuth for KindUnauthorized, or the network or context
	// error (without the url.Error wrapper that carries the URL) for
	// KindUnreachable and KindCanceled.
	cause error
	// conflictBody is the first maxErrorBodyBytes of a 409 response; nil for
	// every other kind.
	conflictBody []byte
}

// ConflictBody returns a copy of the 409 response body, at most
// maxErrorBodyBytes, or nil for any other kind. It is untrusted Trakt JSON,
// possibly cut at the cap: parse it defensively and never put it in logs.
func (e *Error) ConflictBody() []byte {
	if e.conflictBody == nil {
		return nil
	}

	return append([]byte(nil), e.conflictBody...)
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return "trakt: " + e.Message
	}

	return "trakt: status " + strconv.Itoa(e.Status) + ": " + e.Message
}

// Unwrap exposes ErrUpstreamAuth, context.Canceled or the network cause.
func (e *Error) Unwrap() error {
	return e.cause
}

// kindForStatus maps a non-2xx HTTP status to a Kind.
func kindForStatus(status int) Kind {
	switch {
	case status == http.StatusUnauthorized:
		return KindUnauthorized
	case status == http.StatusNotFound:
		return KindNotFound
	case status == http.StatusConflict:
		return KindConflict
	case status == statusAccountLimit:
		return KindAccountLimit
	case status == http.StatusLocked:
		return KindLocked
	case status == http.StatusUpgradeRequired:
		return KindVIPRequired
	case status == http.StatusTooManyRequests:
		return KindRateLimited
	case status >= http.StatusInternalServerError:
		return KindServer
	default:
		return KindUnknown
	}
}

// errorFromResponse builds an Error from a non-2xx response. It reads at most
// maxErrorBodyBytes of the body; the caller closes it.
func errorFromResponse(resp *http.Response, now time.Time) *Error {
	kind := kindForStatus(resp.StatusCode)
	e := &Error{Status: resp.StatusCode, Kind: kind, Message: defaultMessages[kind]}

	body := readErrorBody(resp.Body)
	// A refused token always gets revokedMessage: Trakt's own wording would
	// hide the one action that helps, reconnecting.
	if desc := errorDescription(body); desc != "" && kind != KindUnauthorized {
		e.Message = desc
	}

	if kind == KindConflict {
		e.conflictBody = body
	}

	if kind == KindRateLimited {
		e.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), now)
	}

	if kind == KindUnauthorized {
		e.cause = ErrUpstreamAuth
	}

	return e
}

// readErrorBody reads at most maxErrorBodyBytes. A read failure yields nil:
// the status alone still describes the error, and a partial body is not
// worth keeping.
func readErrorBody(body io.Reader) []byte {
	if body == nil {
		return nil
	}

	b, err := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	if err != nil {
		return nil
	}

	return b
}

// errorDescription returns Trakt's error_description, truncated, or "" when
// the body is empty, not JSON, or has no such field.
func errorDescription(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var payload struct {
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	return truncateRunes(strings.TrimSpace(strings.ToValidUTF8(payload.ErrorDescription, "")), maxMessageRunes)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	return string([]rune(s)[:n])
}

// parseRetryAfter accepts delay-seconds or an HTTP-date (RFC 9110 10.2.3).
// Missing, malformed or past values give 0; values above maxRetryAfter are
// clamped.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}

	if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
		return clampRetryAfter(secondsToDuration(secs))
	} else if errors.Is(err, strconv.ErrRange) && !strings.HasPrefix(v, "-") {
		return maxRetryAfter
	}

	t, err := http.ParseTime(v)
	if err != nil {
		return 0
	}

	return clampRetryAfter(t.Sub(now))
}

// secondsToDuration converts without overflowing time.Duration in either
// direction; zero and negative values give 0.
func secondsToDuration(secs int64) time.Duration {
	if secs <= 0 {
		return 0
	}

	if secs > math.MaxInt64/int64(time.Second) {
		return maxRetryAfter
	}

	return time.Duration(secs) * time.Second
}

func clampRetryAfter(d time.Duration) time.Duration {
	return min(max(d, 0), maxRetryAfter)
}

// transportError maps a failed round trip (no response) to KindCanceled when
// the caller's context was canceled, and to KindUnreachable otherwise. The
// url.Error wrapper, which carries the request URL, is dropped.
func transportError(ctx context.Context, err error) *Error {
	cause := err
	if inner := errors.Unwrap(err); inner != nil {
		cause = inner
	}

	if ctxErr := ctx.Err(); errors.Is(ctxErr, context.Canceled) {
		return &Error{Kind: KindCanceled, Message: defaultMessages[KindCanceled], cause: ctxErr}
	}

	return &Error{Kind: KindUnreachable, Message: defaultMessages[KindUnreachable], cause: cause}
}
