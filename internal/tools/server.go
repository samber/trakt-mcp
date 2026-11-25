// Package tools serves the MCP endpoint: the Trakt tools, their per-call
// middleware (user, rate limits, write lock, error mapping, logging, metrics)
// and the bearer check in front of the stateless Streamable HTTP handler.
package tools

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/limits"
	"github.com/samber/trakt-mcp/internal/oauth"
	"github.com/samber/trakt-mcp/internal/obs"
	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	// serverName is the MCP implementation name advertised to clients.
	serverName = "trakt-mcp"
	// Protocol versions served (spec §5.2). The two legacy ones keep clients
	// that open with initialize working (go-sdk PR #1345).
	protocolVersion20260728 = "2026-07-28"
	protocolVersion20251125 = "2025-11-25"
	protocolVersion20250618 = "2025-06-18"
)

// TraktClients returns a Trakt client acting as one user; *trakt.Upstream
// implements it. It fails with trakt.ErrUpstreamAuth when the user has no
// usable Trakt grant.
type TraktClients interface {
	Client(ctx context.Context, userID string) (*trakt.UserClient, error)
}

// UserToucher records user activity; *store.Store implements it.
type UserToucher interface {
	TouchUser(ctx context.Context, id string) error
}

// Deps are the collaborators of the MCP endpoint.
type Deps struct {
	// Trakt hands out per-user Trakt clients; *trakt.Upstream in production.
	// Its HTTP client should use (*obs.Metrics).Transport so tool-call log
	// lines and metrics carry Trakt's status.
	Trakt   TraktClients
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

	// onlyTools, when non-nil, registers just the named tools. Tests that call
	// one tool set it: registering all 143 tools remarshals every input schema,
	// which costs minutes per suite under the race detector.
	onlyTools map[string]struct{}
}

// skipsTool reports whether Register must leave the tool out.
func (d Deps) skipsTool(name string) bool {
	if d.onlyTools == nil {
		return false
	}

	_, keep := d.onlyTools[name]

	return !keep
}

func (d Deps) logger() *slog.Logger {
	if d.Logger == nil {
		return slog.Default()
	}

	return d.Logger
}

// Server is the MCP server with the registry of the tools it serves.
type Server struct {
	mcp   *mcp.Server
	deps  Deps
	specs []Spec
}

// NewServer builds the MCP server with every catalog tool registered.
func NewServer(deps Deps) *Server {
	return newServer(deps, catalog)
}

// schemaCache shares inferred and resolved tool schemas across every Server in
// the process. The 143 tools use a fixed set of Go types, and resolving their
// schemas dominates the cost of newServer: production builds one server, but
// each test builds its own, and without the cache the race-enabled suite
// peaks around 3 GB on CI. Cached schemas are read-only after creation.
var schemaCache = mcp.NewSchemaCache()

// serverCapabilities advertises tools/listChanged explicitly: the SDK infers it
// only once a tool is registered. A non-nil Capabilities replaces the SDK
// default, so logging is restated here to keep it advertised.
func serverCapabilities() *mcp.ServerCapabilities {
	return &mcp.ServerCapabilities{
		Logging: &mcp.LoggingCapabilities{}, //nolint:staticcheck // SA1019: deprecated since protocol 2026-07-28 but still served to clients on older versions
		Tools:   &mcp.ToolCapabilities{ListChanged: true},
	}
}

// newServer builds the MCP server with the given tool groups registered in
// order; tests add their own groups.
func newServer(deps Deps, groups []func(*Server)) *Server {
	s := &Server{
		deps: deps,
		mcp: mcp.NewServer(&mcp.Implementation{Name: serverName, Version: deps.Version, Icons: serverIcons()}, &mcp.ServerOptions{
			Logger:       deps.logger(),
			SchemaCache:  schemaCache,
			Capabilities: serverCapabilities(),
			SupportedProtocolVersions: []string{
				protocolVersion20260728,
				protocolVersion20251125,
				protocolVersion20250618,
			},
		}),
	}
	s.mcp.AddReceivingMiddleware(s.recordRejectedInput)

	for _, register := range groups {
		register(s)
	}

	return s
}

// methodToolsCall is the JSON-RPC method of a tool call.
const methodToolsCall = "tools/call"

// handlerRanKey keys, in a tools/call context, the flag handle sets when a
// registered handler ran.
type handlerRanKey struct{}

// markHandlerRan tells recordRejectedInput that handle ran for this call.
func markHandlerRan(ctx context.Context) {
	if ran, ok := ctx.Value(handlerRanKey{}).(*atomic.Bool); ok {
		ran.Store(true)
	}
}

// recordRejectedInput gives a tool call that go-sdk refused against the
// input schema, before any handler ran, the same one log line and metric
// sample as other calls, with status invalid_input. Calls to unknown tools
// are protocol errors and stay unrecorded, so a client cannot grow the tool
// label with made-up names.
func (s *Server) recordRejectedInput(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != methodToolsCall {
			return next(ctx, method, req)
		}

		start := time.Now()
		ran := &atomic.Bool{}

		res, err := next(context.WithValue(ctx, handlerRanKey{}, ran), method, req)
		if err != nil || ran.Load() {
			return res, err
		}

		result, isResult := res.(*mcp.CallToolResult)
		call, isCall := req.(*mcp.CallToolRequest)

		if isResult && isCall && result.IsError && call.Params != nil && s.registered(call.Params.Name) {
			userID, _ := oauth.UserID(ctx)
			s.deps.finishCall(ctx, callRecord{
				tool:     call.Params.Name,
				userID:   userID,
				duration: time.Since(start),
				err:      &toolError{msg: msgInvalidInput, invalidInput: true},
			})
		}

		return res, nil
	}
}

// registered reports whether a tool named name is registered.
func (s *Server) registered(name string) bool {
	return slices.ContainsFunc(s.specs, func(spec Spec) bool { return spec.Name == name })
}

// MCP returns the underlying go-sdk server.
func (s *Server) MCP() *mcp.Server { return s.mcp }

// Specs returns the registered tools in registration order.
func (s *Server) Specs() []Spec {
	return append([]Spec(nil), s.specs...)
}

// NewHandler returns the POST /mcp handler: bearer check, then a stateless
// Streamable HTTP handler sharing one server across requests.
func NewHandler(deps Deps) http.Handler {
	return newHandler(deps, catalog)
}

func newHandler(deps Deps, groups []func(*Server)) http.Handler {
	srv := newServer(deps, groups).mcp
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{
		Stateless: true,
		Logger:    deps.logger(),
	})

	return auth.RequireBearerToken(deps.Verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: deps.ResourceMetadataURL,
	})(h)
}
