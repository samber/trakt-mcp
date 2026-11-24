package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/oauth"
	"github.com/samber/trakt-mcp/internal/obs"
	"github.com/samber/trakt-mcp/internal/trakt"
)

// Access says what a tool does to the user's Trakt account; it selects the
// rate-limit bucket, the write lock and the annotation hints.
type Access int

const (
	// Read tools never change anything: readOnlyHint true.
	Read Access = iota
	// Write tools add or change data: readOnlyHint false, destructiveHint false.
	Write
	// Destroy tools remove or clear data: readOnlyHint false, destructiveHint true.
	Destroy
)

// writeWarning ends the description of every Write and Destroy tool (spec §8).
const writeWarning = "This changes the user's real Trakt account."

// Spec describes one tool.
type Spec struct {
	Name, Title, Description string
	Access                   Access
}

// annotations returns the hints for a, all explicit: Claude's and OpenAI's
// directories reject tools without them. Every tool reaches Trakt, so every
// tool is open-world.
func (a Access) annotations(title string) *mcp.ToolAnnotations {
	hintTrue, hintFalse := true, false
	destructive := &hintFalse

	if a == Destroy {
		destructive = &hintTrue
	}

	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    a == Read,
		DestructiveHint: destructive,
		OpenWorldHint:   &hintTrue,
	}
}

func (a Access) writes() bool { return a == Write || a == Destroy }

// Tool error messages returned to the assistant (spec §8.6). They never carry
// tool arguments or Trakt response bodies, since tool errors are logged.
const (
	msgNotFound     = "not found"
	msgConflict     = "conflict"
	msgAccountLimit = "Trakt account limit reached; do not retry"
	msgVIPRequired  = "Trakt VIP is required for this feature"
	msgLocked       = "this Trakt account is locked"
	msgUpstreamAuth = "Trakt access was revoked; reconnect this connector"
	// msgUnreachable covers timeouts, refused connections and DNS failures.
	// After a timeout Trakt may already have applied a write, so callers must
	// re-read before retrying one.
	msgUnreachable = "Trakt is unreachable, retry later"
	// msgCanceled answers a call the client canceled; it rarely reaches the
	// client, which stopped waiting.
	msgCanceled = "canceled"
	// msgInternal replaces any error whose text was not built to be shown;
	// the middleware logs the real cause.
	msgInternal = "internal error"
	msgNoUser   = "internal error: no authenticated user"
	// msgRateLimitedNoDelay answers a Trakt 429 without a usable Retry-After.
	msgRateLimitedNoDelay = "rate limited, retry later"
	// Prefixes completed with a field name, a delay or a status.
	msgInvalidArgumentPrefix = "invalid argument: "
	msgRateLimitedPrefix     = "rate limited, retry after "
	msgRateLimitedSuffix     = " s"
	msgServerFormat          = "Trakt returned an error (status %d)"
	msgUnknownFormat         = "Trakt rejected the request (status %d)"
)

// Tool call statuses, in logs and in the tool_calls metric.
const (
	statusOK          = "ok"
	statusError       = "error"
	statusRateLimited = "rate_limited"
	statusCanceled    = "canceled"
)

// Rate-limit kinds of the rate_limited metric: the per-user read and write
// buckets, the application-wide bucket, and Trakt's own 429.
const (
	rateKindRead   = "read"
	rateKindWrite  = "write"
	rateKindGlobal = "global"
	rateKindTrakt  = "trakt"
)

// Register adds a tool to s. Every call goes through the same middleware, in
// order: resolve the user, touch it, take one token from the user's read or
// write bucket and one from the global bucket, take the user's write lock for
// Write and Destroy, get the user's Trakt client, run h, map errors to tool
// errors. The result carries structured content plus one text block with the
// same JSON. Every call ends with one log line and one metric sample.
//
// Register panics on an invalid spec or an output type without a JSON
// schema: both are programming errors caught by the registry tests.
func Register[In, Out any](s *Server, spec Spec, h func(ctx context.Context, c *trakt.UserClient, in In) (Out, error)) {
	if spec.Name == "" || spec.Title == "" || spec.Description == "" || spec.Access < Read || spec.Access > Destroy {
		panic(fmt.Sprintf("tools: invalid spec %+v", spec))
	}

	if spec.Access.writes() && !strings.Contains(spec.Description, writeWarning) {
		spec.Description = strings.TrimSpace(spec.Description) + " " + writeWarning
	}

	mcp.AddTool(s.mcp, &mcp.Tool{
		Name:         spec.Name,
		Title:        spec.Title,
		Description:  spec.Description,
		Annotations:  spec.Access.annotations(spec.Title),
		OutputSchema: outputSchema[Out](),
	}, handle(s.deps, spec, h))
	s.specs = append(s.specs, spec)
}

// outputSchema infers Out's schema, describing trakt.Page as Listing
// marshals it (the go-sdk validates every output against this schema).
func outputSchema[Out any]() *jsonschema.Schema {
	pageSchema, err := jsonschema.For[pageJSON](nil)
	if err != nil {
		panic(fmt.Sprintf("tools: page schema: %v", err))
	}

	schema, err := jsonschema.For[Out](&jsonschema.ForOptions{
		TypeSchemas: map[reflect.Type]*jsonschema.Schema{reflect.TypeFor[trakt.Page](): pageSchema},
	})
	if err != nil {
		panic(fmt.Sprintf("tools: output schema of %v: %v", reflect.TypeFor[Out](), err))
	}

	return schema
}

// handle wraps a tool body with the per-call middleware described on Register.
func handle[In, Out any](deps Deps, spec Spec, h func(context.Context, *trakt.UserClient, In) (Out, error)) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		ctx, traktStatus := obs.WithCallRecorder(ctx)
		userID, _ := oauth.UserID(ctx)
		res, out, err := runTool(ctx, deps, userID, spec.Access, h, in)
		deps.finishCall(ctx, callRecord{
			tool:        spec.Name,
			userID:      userID,
			duration:    time.Since(start),
			traktStatus: traktStatus(),
			args:        in,
			out:         out,
			err:         err,
		})

		if err != nil {
			var zero Out
			return nil, zero, publicError(err)
		}

		return res, out, nil
	}
}

// runTool is the body of handle. Its errors are mapped by mapError but may
// still carry an internal cause, which handle logs and strips.
func runTool[In, Out any](ctx context.Context, deps Deps, userID string, access Access, h func(context.Context, *trakt.UserClient, In) (Out, error), in In) (*mcp.CallToolResult, Out, error) {
	var zero Out
	if userID == "" {
		// Unreachable behind RequireBearerToken with our verifier.
		return nil, zero, &toolError{msg: msgNoUser, cause: errNoUser}
	}

	if err := deps.Store.TouchUser(ctx, userID); err != nil {
		// last_seen only drives retention; a failed update must not block
		// the user's call.
		deps.logger().WarnContext(ctx, "tools: touch user", slog.Any("error", err))
	}

	// Rate limits before the write lock, so a refused write never waits. The
	// user's bucket comes first, so one user cannot drain the global budget
	// with calls their own bucket refuses.
	if allowed, retry := deps.Limiter.Allow(userID, access.writes()); !allowed {
		kind := rateKindRead
		if access.writes() {
			kind = rateKindWrite
		}

		return nil, zero, &toolError{msg: rateLimitedMessage(retry), rateKind: kind}
	}

	if allowed, retry := deps.Limiter.AllowGlobal(); !allowed {
		return nil, zero, &toolError{msg: rateLimitedMessage(retry), rateKind: rateKindGlobal}
	}

	if access.writes() {
		// Trakt mutations of one user run one at a time (spec §7).
		unlock := deps.Limiter.WriteLock(userID)
		defer unlock()
	}

	c, err := deps.Trakt.Client(ctx, userID)
	if err != nil {
		return nil, zero, mapError(err)
	}

	out, err := h(ctx, c, in)
	if err != nil {
		return nil, zero, mapError(err)
	}

	// Clients such as claude.ai hand only `content` to the model and drop
	// `structuredContent`, so the serialized output travels in a text block
	// too (MCP 2025-06-18). Setting Content here keeps the result independent
	// of the SDK's fallback, which only applies while Content is nil.
	data, err := json.Marshal(out)
	if err != nil {
		return nil, zero, &toolError{msg: msgInternal, cause: err}
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, out, nil
}

// errNoUser is the logged cause of a call that reached a tool without an
// authenticated user.
var errNoUser = errors.New("tools: call without authenticated user")

// rateLimitedMessage tells the assistant how long to wait, in whole seconds
// rounded up so it never retries too early.
func rateLimitedMessage(retry time.Duration) string {
	if retry <= 0 {
		return msgRateLimitedNoDelay
	}

	secs := int64(math.Ceil(retry.Seconds()))

	return msgRateLimitedPrefix + strconv.FormatInt(secs, 10) + msgRateLimitedSuffix
}

// callRecord is everything logged about one finished tool call.
type callRecord struct {
	tool, userID string
	duration     time.Duration
	traktStatus  int
	args, out    any
	err          error
}

// finishCall logs the call and records its metrics. The info line holds
// only metadata; arguments and results, which are personal data, are added
// only when the logger is enabled at debug (spec §12).
func (d Deps) finishCall(ctx context.Context, rec callRecord) {
	log := d.logger()
	status, count := statusOK, resultCount(rec.out)

	te, isToolError := errors.AsType[*toolError](rec.err)

	switch {
	case isToolError && te.rateKind != "":
		status, count = statusRateLimited, 0

		d.Metrics.RateLimited(te.rateKind)
	case isToolError && te.canceled:
		status, count = statusCanceled, 0
	case rec.err != nil:
		status, count = statusError, 0
	}

	if isToolError && te.cause != nil {
		// The cause comes from our code, a Trakt status or a network error
		// stripped of its URL; never from tool arguments, a token or a Trakt
		// response body. An unreachable Trakt is an outage to watch, not a
		// bug in this server.
		level := slog.LevelError
		if te.unreachable {
			level = slog.LevelWarn
		}

		log.Log(ctx, level, "tool call failed",
			slog.String("tool", rec.tool), slog.String("user_id", rec.userID), slog.Any("error", te.cause))
	}

	attrs := []slog.Attr{
		slog.String("tool", rec.tool),
		slog.String("user_id", rec.userID),
		slog.Int64("duration_ms", rec.duration.Milliseconds()),
		slog.String("status", status),
		slog.Int("trakt_status", rec.traktStatus),
		slog.Int("result_count", count),
	}
	if log.Enabled(ctx, slog.LevelDebug) {
		attrs = append(attrs, slog.Any("args", rec.args))
		if rec.err != nil {
			attrs = append(attrs, slog.String("error", publicError(rec.err).Error()))
		} else {
			attrs = append(attrs, slog.Any("result", rec.out))
		}
	}

	log.LogAttrs(ctx, slog.LevelInfo, "tool call", attrs...)
	d.Metrics.ToolCall(rec.tool, status, rec.duration)
}

// resultCounter is implemented by tool outputs that report how many items
// they return.
type resultCounter interface{ resultCount() int }

// resultCount is the number of items in a tool output, 0 for outputs that
// return none.
func resultCount(out any) int {
	if rc, ok := out.(resultCounter); ok {
		return rc.resultCount()
	}

	return 0
}

// toolError is an error whose message is final and safe for the assistant.
type toolError struct {
	msg string
	// cause is the internal error behind msg: logged, never shown.
	cause error
	// rateKind is set when a rate limit refused the call.
	rateKind string
	// unreachable marks a call that got no answer from Trakt.
	unreachable bool
	// canceled marks a call whose context the client canceled.
	canceled bool
}

func (e *toolError) Error() string { return e.msg }

func newToolError(msg string) error { return &toolError{msg: msg} }

// invalidArgument names the rejected field without echoing its value.
func invalidArgument(field string) error {
	return newToolError(msgInvalidArgumentPrefix + field)
}

// publicError is err as the assistant sees it: a toolError without its
// cause, or err itself.
func publicError(err error) error {
	if te, ok := errors.AsType[*toolError](err); ok {
		return newToolError(te.msg)
	}

	return err
}

// mapError turns any error of a tool call into the message the assistant
// sees. Trakt failures are mapped by Kind only, never by their message.
// Errors whose text was not built for the assistant become msgInternal,
// keeping the original as cause for the log.
func mapError(err error) error {
	if te, ok := errors.AsType[*toolError](err); ok {
		return te
	}

	if tre, ok := errors.AsType[*trakt.Error](err); ok {
		return fromTraktError(tre)
	}

	switch {
	case errors.Is(err, trakt.ErrUpstreamAuth):
		return newToolError(msgUpstreamAuth)
	case errors.Is(err, context.Canceled):
		return &toolError{msg: msgCanceled, canceled: true}
	}

	return &toolError{msg: msgInternal, cause: err}
}

// fromTraktError maps a Trakt failure by its Kind (spec §8.6).
func fromTraktError(e *trakt.Error) error {
	switch e.Kind {
	case trakt.KindRateLimited:
		return &toolError{msg: rateLimitedMessage(e.RetryAfter), rateKind: rateKindTrakt}
	case trakt.KindNotFound:
		return newToolError(msgNotFound)
	case trakt.KindConflict:
		return newToolError(msgConflict)
	case trakt.KindAccountLimit:
		return newToolError(msgAccountLimit)
	case trakt.KindVIPRequired:
		return newToolError(msgVIPRequired)
	case trakt.KindLocked:
		return newToolError(msgLocked)
	case trakt.KindUnauthorized:
		return newToolError(msgUpstreamAuth)
	case trakt.KindServer:
		return newToolError(fmt.Sprintf(msgServerFormat, e.Status))
	case trakt.KindUnreachable:
		// Unwrap gives the network cause without the url.Error wrapper, so
		// the request URL never reaches the log.
		cause := errors.Unwrap(e)
		if cause == nil {
			cause = errors.New(e.Kind.String())
		}

		return &toolError{msg: msgUnreachable, cause: cause, unreachable: true}
	case trakt.KindCanceled:
		return &toolError{msg: msgCanceled, canceled: true}
	case trakt.KindUnknown:
		// Usually a request this server built wrong: log the status only,
		// since e.Message may quote Trakt's response body.
		return &toolError{msg: fmt.Sprintf(msgUnknownFormat, e.Status), cause: fmt.Errorf("trakt status %d", e.Status)}
	}

	return &toolError{msg: msgInternal, cause: fmt.Errorf("trakt kind %s, status %d", e.Kind, e.Status)}
}
