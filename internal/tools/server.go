// Package tools serves the MCP endpoint: the contact tools, their per-call
// middleware (user, rate limit, write lock, error mapping) and the bearer
// check in front of the stateless Streamable HTTP handler.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/google-contacts-mcp/internal/contacts"
	"github.com/samber/google-contacts-mcp/internal/limits"
	"github.com/samber/google-contacts-mcp/internal/oauth"
	"github.com/samber/google-contacts-mcp/internal/obs"
)

const (
	// serverName is the MCP implementation name advertised to clients.
	serverName = "google-contacts-mcp"
	// Protocol versions served (spec §5.2). The two legacy ones keep clients
	// that open with initialize working (go-sdk PR #1345).
	protocolVersion20260728 = "2026-07-28"
	protocolVersion20251125 = "2025-11-25"
	protocolVersion20250618 = "2025-06-18"
)

// Tool error messages returned to the assistant (spec §8.2). They never carry
// tool arguments or results, since tool errors are logged at info.
const (
	msgContactNotFound = "contact not found"
	msgGroupNotFound   = "group not found"
	msgUpstreamAuth    = "Google access was revoked; reconnect this connector"
	msgNoUser          = "internal error: no authenticated user"
	msgInvalidArgument = "invalid argument"
	// msgUnreachable covers timeouts, refused connections and DNS failures.
	// After a timeout or a dropped connection Google may already have applied
	// a write, so callers must re-read before retrying one.
	msgUnreachable = "Google is unreachable, retry later"
	// msgCanceled answers a call the client canceled; it rarely reaches the
	// client, which stopped waiting.
	msgCanceled = "canceled"
	// msgInternal replaces any error whose text was not built to be shown;
	// the middleware logs the real cause.
	msgInternal = "internal error"
)

// Tool call statuses, in logs and in the tool_calls metric.
const (
	statusOK          = "ok"
	statusError       = "error"
	statusRateLimited = "rate_limited"
	statusCanceled    = "canceled"
)

// Rate-limit kinds of the rate_limited metric: the local read and write
// buckets, and Google's own 429.
const (
	rateKindRead   = "read"
	rateKindWrite  = "write"
	rateKindGoogle = "google"
)

// PeopleFactory returns a People API client acting as one user;
// *google.Upstream implements it.
type PeopleFactory interface {
	People(ctx context.Context, userID string) (contacts.People, error)
}

// UserToucher records user activity; *store.Store implements it.
type UserToucher interface {
	TouchUser(ctx context.Context, id string) error
}

// Deps are the collaborators of the MCP endpoint.
type Deps struct {
	People  PeopleFactory
	Store   UserToucher
	Limiter *limits.Limiter
	// Verifier checks bearer tokens; (*oauth.Server).Verifier() in production.
	Verifier auth.TokenVerifier
	// ResourceMetadataURL goes into the WWW-Authenticate header of 401
	// answers; (*oauth.Server).ResourceMetadataURL() in production.
	ResourceMetadataURL string
	// Version is the server version advertised to clients.
	Version string
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Metrics records tool calls and rate-limit refusals; nil disables them.
	Metrics *obs.Metrics
}

func (d Deps) logger() *slog.Logger {
	if d.Logger == nil {
		return slog.Default()
	}

	return d.Logger
}

// NewServer builds the MCP server with every contact tool registered.
func NewServer(deps Deps) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: serverName, Version: deps.Version, Icons: serverIcons()}, &mcp.ServerOptions{
		Logger: deps.logger(),
		SupportedProtocolVersions: []string{
			protocolVersion20260728,
			protocolVersion20251125,
			protocolVersion20250618,
		},
	})
	registerTools(srv, deps)

	return srv
}

// NewHandler returns the POST /mcp handler: bearer check, then a stateless
// Streamable HTTP handler sharing one server across requests.
func NewHandler(deps Deps) http.Handler {
	srv := NewServer(deps)
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		Logger:    deps.logger(),
	})

	return auth.RequireBearerToken(deps.Verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: deps.ResourceMetadataURL,
	})(h)
}

// toolFunc is a tool body: it runs with the caller's People client once the
// middleware has admitted the call.
type toolFunc[In, Out any] func(ctx context.Context, p contacts.People, in In) (out Out, err error)

// handle wraps a tool body with the per-call middleware, in order: resolve
// the user, touch it, take a rate-limit token, take the write lock for
// writes, run, then map errors to assistant-facing messages. Every call ends
// with one log line and one metric sample.
func handle[In, Out any](deps Deps, write bool, fn toolFunc[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		start := time.Now()
		ctx, googleStatus := obs.WithCallRecorder(ctx)
		userID, _ := oauth.UserID(ctx)
		res, out, err := runTool(ctx, deps, userID, write, fn, in)
		deps.finishCall(ctx, callRecord{
			tool:         toolName(req),
			userID:       userID,
			duration:     time.Since(start),
			googleStatus: googleStatus(),
			args:         in,
			out:          out,
			err:          err,
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
func runTool[In, Out any](ctx context.Context, deps Deps, userID string, write bool, fn toolFunc[In, Out], in In) (*mcp.CallToolResult, Out, error) {
	var zero Out
	if userID == "" {
		// Unreachable behind RequireBearerToken with our verifier.
		return nil, zero, &toolError{msg: msgNoUser, cause: errNoUser}
	}

	if err := deps.Store.TouchUser(ctx, userID); err != nil {
		// last_seen only drives retention; a failed update must not
		// block the user's call.
		deps.logger().WarnContext(ctx, "tools: touch user", slog.Any("error", err))
	}
	// Rate limit before the write lock, so a refused write never waits.
	if allowed, retry := deps.Limiter.Allow(userID, write); !allowed {
		kind := rateKindRead
		if write {
			kind = rateKindWrite
		}

		return nil, zero, &toolError{msg: contacts.RateLimitedError{RetryAfter: retry}.Error(), rateKind: kind}
	}

	if write {
		unlock := deps.Limiter.WriteLock(userID)
		defer unlock()
	}

	p, err := deps.People.People(ctx, userID)
	if err != nil {
		return nil, zero, mapError(err, msgContactNotFound)
	}

	out, err := fn(ctx, p, in)
	if err != nil {
		return nil, zero, mapError(err, msgContactNotFound)
	}

	// Clients such as claude.ai hand only `content` to the model and drop
	// `structuredContent`, so the serialized output must travel in a text
	// block too (MCP 2025-06-18: a tool returning structured content SHOULD
	// also return its serialized JSON in a TextContent block). Setting
	// Content here keeps the result independent of the SDK's default
	// fallback, which only applies while Content is nil.
	data, err := json.Marshal(out)
	if err != nil {
		return nil, zero, &toolError{msg: msgInternal, cause: err}
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, out, nil
}

// errNoUser is the logged cause of a call that reached a tool without an
// authenticated user.
var errNoUser = errors.New("tools: call without authenticated user")

// callRecord is everything logged about one finished tool call.
type callRecord struct {
	tool, userID string
	duration     time.Duration
	googleStatus int
	args, out    any
	err          error
}

// finishCall logs the call and records its metrics. The info line holds
// only metadata; arguments and results, which are personal data, are added
// only when the logger is enabled at debug (spec §12).
func (d Deps) finishCall(ctx context.Context, rec callRecord) {
	log := d.logger()
	status, count := statusOK, resultCount(rec.out)

	var te *toolError

	isToolError := errors.As(rec.err, &te)
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
		// The cause comes from our code or Google's status, never from the
		// tool arguments or a token. An unreachable Google is an outage to
		// watch, not a bug in this server.
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
		slog.Int("google_status", rec.googleStatus),
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

// toolName is the called tool's name, "" when the SDK gives no request.
func toolName(req *mcp.CallToolRequest) string {
	if req == nil || req.Params == nil {
		return ""
	}

	return req.Params.Name
}

// resultCounter is implemented by tool outputs that report how many contacts
// or groups they return.
type resultCounter interface{ resultCount() int }

// resultCount is the number of contacts or groups in a tool output, 0 for
// outputs that return none.
func resultCount(out any) int {
	if rc, ok := out.(resultCounter); ok {
		return rc.resultCount()
	}

	return 0
}

// toolError is an error whose message is final and safe for the assistant.
type toolError struct {
	msg string
	// cause is the internal error behind msgInternal: logged, never shown.
	cause error
	// rateKind is set when a rate limit refused the call.
	rateKind string
	// unreachable marks a call that got no answer from Google.
	unreachable bool
	// canceled marks a call whose context the client canceled.
	canceled bool
}

func (e *toolError) Error() string { return e.msg }

func newToolError(msg string) error { return &toolError{msg: msg} }

// publicError is err as the assistant sees it: a toolError without its
// cause, or err itself.
func publicError(err error) error {
	if te, ok := errors.AsType[*toolError](err); ok {
		return newToolError(te.msg)
	}

	return err
}

// mapError turns a contacts or People error into the message the assistant
// sees. notFound is the wording for a bare contacts.ErrNotFound. Errors whose
// text was not built for the assistant become msgInternal, keeping the
// original as cause for the log.
func mapError(err error, notFound string) error {
	var (
		te *toolError
		rl contacts.RateLimitedError
		us contacts.UpstreamStatusError
		ge contacts.GroupError
		ue contacts.UnreachableError
	)
	switch {
	case errors.As(err, &te):
		return te
	case errors.Is(err, context.Canceled):
		return &toolError{msg: msgCanceled, canceled: true}
	case errors.As(err, &ue):
		return &toolError{msg: msgUnreachable, cause: ue, unreachable: true}
	case errors.As(err, &rl):
		return &toolError{msg: rl.Error(), rateKind: rateKindGoogle}
	case errors.Is(err, contacts.ErrUpstreamAuth):
		return newToolError(msgUpstreamAuth)
	case errors.Is(err, contacts.ErrConflict):
		return newToolError(contacts.ErrConflict.Error())
	case errors.Is(err, contacts.ErrNotFound):
		return newToolError(notFound)
	case errors.Is(err, contacts.ErrInvalidArgument):
		return newToolError(msgInvalidArgument)
	case errors.As(err, &us):
		// Op name and HTTP status only.
		return newToolError(us.Error())
	case errors.As(err, &ge):
		// Names only the group the caller gave.
		return newToolError(ge.Error())
	}

	return &toolError{msg: msgInternal, cause: err}
}
