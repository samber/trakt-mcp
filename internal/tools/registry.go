package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
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
	msgVIPRequired  = "Trakt VIP is required for this feature; do not retry"
	msgLocked       = "this Trakt account is locked; do not retry"
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
	// msgInvalidInput is logged for a call the input schema refused; the
	// client got go-sdk's own validation message.
	msgInvalidInput = "invalid input"
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
	// statusInvalidInput marks a call the input schema refused before any
	// handler ran.
	statusInvalidInput = "invalid_input"
)

// keyErrorClass is the log key of a failed call's error class: one of the
// errorClass* labels, never the raw error text, which may quote data.
const keyErrorClass = "error_class"

// Error classes of failed calls, in logs only.
const (
	errorClassPanic         = "panic"
	errorClassInternal      = "internal"
	errorClassNoUser        = "no_user"
	errorClassTraktRejected = "trakt_rejected"
	errorClassTraktKind     = "trakt_unexpected_kind"
	// Unreachable classes, from the network cause.
	errorClassTimeout     = "timeout"
	errorClassDNS         = "dns"
	errorClassRefused     = "connection_refused"
	errorClassNetwork     = "network"
	errorClassUnreachable = "unreachable"
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
// Register panics on an invalid spec, a name already registered (go-sdk
// would silently replace the earlier tool) or an output type without a JSON
// schema: all are programming errors caught by the registry tests.
func Register[In, Out any](s *Server, spec Spec, h func(ctx context.Context, c *trakt.UserClient, in In) (Out, error)) {
	if spec.Name == "" || spec.Title == "" || spec.Description == "" || spec.Access < Read || spec.Access > Destroy {
		panic(fmt.Sprintf("tools: invalid spec %+v", spec))
	}

	if s.registered(spec.Name) {
		panic("tools: tool registered twice: " + spec.Name)
	}

	if s.deps.skipsTool(spec.Name) {
		return
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

// outputSchemas maps each Out type to its inferred schema. go-sdk's schema
// cache keys provided schemas by pointer, so returning the same pointer per
// type is what lets schemaCache skip re-resolving it for every Server.
var outputSchemas sync.Map // reflect.Type -> *jsonschema.Schema

// outputSchema returns the schema of Out, inferring it once per type.
func outputSchema[Out any]() *jsonschema.Schema {
	rt := reflect.TypeFor[Out]()
	if cached, ok := outputSchemas.Load(rt); ok {
		if schema, isSchema := cached.(*jsonschema.Schema); isSchema {
			return schema
		}
	}

	// Another goroutine may have stored a schema first: keep that one, so
	// every caller shares one pointer.
	stored, _ := outputSchemas.LoadOrStore(rt, inferOutputSchema[Out]())

	schema, isSchema := stored.(*jsonschema.Schema)
	if !isSchema {
		panic(fmt.Sprintf("tools: cached output schema of %v has type %T", rt, stored))
	}

	return schema
}

// inferOutputSchema infers Out's schema, describing trakt.Page as Listing
// marshals it (the go-sdk validates every output against this schema).
func inferOutputSchema[Out any]() *jsonschema.Schema {
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
		markHandlerRan(ctx)

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
// still carry an error class and a stack, which handle logs and strips.
//
// A panicking handler becomes msgInternal. go-sdk runs handlers on its own
// goroutine and never recovers, so without this one nil dereference in any
// tool would crash the instance for every user. The recover is deferred
// first, so it runs last: the write lock is already released when it runs.
func runTool[In, Out any](ctx context.Context, deps Deps, userID string, access Access, h func(context.Context, *trakt.UserClient, In) (Out, error), in In) (res *mcp.CallToolResult, out Out, err error) {
	defer func() {
		if r := recover(); r != nil {
			var zero Out

			// Only the value's type is kept: its text may quote arguments
			// or results.
			res, out, err = nil, zero, &toolError{
				msg:     msgInternal,
				class:   errorClassPanic,
				errType: fmt.Sprintf("%T", r),
				stack:   string(debug.Stack()),
			}
		}
	}()

	var zero Out
	if userID == "" {
		// Unreachable behind RequireBearerToken with our verifier.
		return nil, zero, &toolError{msg: msgNoUser, class: errorClassNoUser}
	}

	if err := deps.Store.TouchUser(ctx, userID); err != nil {
		// last_seen only drives retention; a failed update must not block
		// the user's call.
		deps.logger().WarnContext(ctx, "tools: touch user", slog.Any("error", err))
	}

	// Rate limits before the write lock, so a refused write never waits. The
	// user's bucket comes first, so one user cannot drain the global budget
	// with calls their own bucket refuses; the price is that a call the
	// global bucket refuses has spent the user's token.
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

	out, err = h(ctx, c, in)
	if err != nil {
		return nil, zero, mapError(err)
	}

	// Clients such as claude.ai hand only `content` to the model and drop
	// `structuredContent`, so the serialized output travels in a text block
	// too (MCP 2025-06-18). Setting Content here keeps the result independent
	// of the SDK's fallback, which only applies while Content is nil.
	data, err := json.Marshal(out)
	if err != nil {
		return nil, zero, internalError(err)
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, out, nil
}

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

// finishCall writes the call's one log line and records its metrics. The
// line holds only metadata; arguments and results, which are personal data,
// are added only when the logger is enabled at debug (spec §12). A failure
// with an error class raises the line to error, or to warn for an
// unreachable Trakt (an outage to watch, not a bug in this server).
func (d Deps) finishCall(ctx context.Context, rec callRecord) {
	log := d.logger()
	status, count := callStatus(rec), resultCount(rec.out)

	if status != statusOK {
		count = 0
	}

	if status == statusRateLimited {
		if te, ok := errors.AsType[*toolError](rec.err); ok {
			d.Metrics.RateLimited(te.rateKind)
		}
	}

	attrs := []slog.Attr{
		slog.String("tool", rec.tool),
		slog.String("user_id", rec.userID),
		slog.Int64("duration_ms", rec.duration.Milliseconds()),
		slog.String("status", status),
		slog.Int("trakt_status", rec.traktStatus),
		slog.Int("result_count", count),
	}

	level, failure := failureAttrs(rec.err)
	attrs = append(attrs, failure...)

	if log.Enabled(ctx, slog.LevelDebug) {
		attrs = append(attrs, slog.Any("args", rec.args))
		if rec.err != nil {
			attrs = append(attrs, slog.String("error", publicError(rec.err).Error()))
		} else {
			attrs = append(attrs, slog.Any("result", rec.out))
		}
	}

	log.LogAttrs(ctx, level, msgToolCallLog, attrs...)
	d.Metrics.ToolCall(rec.tool, status, rec.duration)
}

// callStatus is the status label of a finished call.
func callStatus(rec callRecord) string {
	te, isToolError := errors.AsType[*toolError](rec.err)

	switch {
	case rec.err == nil:
		return statusOK
	case isToolError && te.rateKind != "":
		return statusRateLimited
	case isToolError && te.canceled:
		return statusCanceled
	case isToolError && te.invalidInput:
		return statusInvalidInput
	}

	return statusError
}

// failureAttrs returns the log level of a call that ended with err and the
// attributes describing its failure: info and none for success and expected
// outcomes, error (warn when Trakt is unreachable) with the error class, type
// and stack otherwise.
func failureAttrs(err error) (slog.Level, []slog.Attr) {
	te, ok := errors.AsType[*toolError](err)
	if !ok || te.class == "" {
		return slog.LevelInfo, nil
	}

	level := slog.LevelError
	if te.unreachable {
		level = slog.LevelWarn
	}

	attrs := []slog.Attr{slog.String(keyErrorClass, te.class)}
	if te.errType != "" {
		attrs = append(attrs, slog.String(keyErrorType, te.errType))
	}

	if te.stack != "" {
		attrs = append(attrs, slog.String(keyStack, te.stack))
	}

	return level, attrs
}

// msgToolCallLog is the message of the one log line each tool call writes.
const msgToolCallLog = "tool call"

// Log keys of a failed call besides keyErrorClass: the Go type of the
// underlying error or panic value (bounded by the code, never data), and a
// panic's stack trace (code locations only).
const (
	keyErrorType = "error_type"
	keyStack     = "stack"
)

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
	// class labels a failure worth logging above info (errorClass*); empty
	// for expected outcomes such as not found or a validation error.
	class string
	// errType is the Go type of the underlying error or panic value.
	errType string
	// stack is a panic's stack trace.
	stack string
	// rateKind is set when a rate limit refused the call.
	rateKind string
	// unreachable marks a call that got no answer from Trakt.
	unreachable bool
	// canceled marks a call whose context the client canceled.
	canceled bool
	// invalidInput marks a call the input schema refused.
	invalidInput bool
}

func (e *toolError) Error() string { return e.msg }

func newToolError(msg string) error { return &toolError{msg: msg} }

// internalError hides err behind msgInternal, keeping only its type for the
// log: its text may quote data.
func internalError(err error) error {
	return &toolError{msg: msgInternal, class: errorClassInternal, errType: fmt.Sprintf("%T", err)}
}

// invalidArgument names the rejected field without echoing its value.
func invalidArgument(field string) error {
	return newToolError(msgInvalidArgumentPrefix + field)
}

// publicError is err as the assistant sees it: a toolError without its log
// details, or err itself.
func publicError(err error) error {
	if te, ok := errors.AsType[*toolError](err); ok {
		return newToolError(te.msg)
	}

	return err
}

// mapError turns any error of a tool call into the message the assistant
// sees. Trakt failures are mapped by Kind only, never by their message.
// Errors whose text was not built for the assistant become msgInternal.
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
	case errors.Is(err, context.DeadlineExceeded):
		// A deadline on the call's context (Paginate checks it between
		// pages) means Trakt answered too slowly.
		return &toolError{msg: msgUnreachable, class: errorClassTimeout, unreachable: true}
	}

	return internalError(err)
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
		return &toolError{msg: msgUnreachable, class: networkClass(errors.Unwrap(e)), unreachable: true}
	case trakt.KindCanceled:
		return &toolError{msg: msgCanceled, canceled: true}
	case trakt.KindUnknown:
		// Usually a request this server built wrong; the status is on the log
		// line as trakt_status, and e.Message may quote Trakt's body.
		return &toolError{msg: fmt.Sprintf(msgUnknownFormat, e.Status), class: errorClassTraktRejected}
	}

	return &toolError{msg: msgInternal, class: errorClassTraktKind}
}

// networkClass labels the network cause of an unreachable Trakt without its
// text, which names hosts and addresses.
func networkClass(cause error) string {
	var (
		netErr net.Error
		dnsErr *net.DNSError
	)

	switch {
	case cause == nil:
		return errorClassUnreachable
	case errors.As(cause, &dnsErr):
		return errorClassDNS
	case errors.Is(cause, context.DeadlineExceeded), errors.As(cause, &netErr) && netErr.Timeout():
		return errorClassTimeout
	case errors.Is(cause, syscall.ECONNREFUSED):
		return errorClassRefused
	}

	return errorClassNetwork
}
