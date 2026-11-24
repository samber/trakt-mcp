// Package tools serves the MCP endpoint: the Trakt tools, their per-call
// middleware (user, rate limits, write lock, error mapping, logging, metrics)
// and the bearer check in front of the stateless Streamable HTTP handler.
package tools

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samber/trakt-mcp/internal/limits"
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

// newServer builds the MCP server with the given tool groups registered in
// order; tests add their own groups.
func newServer(deps Deps, groups []func(*Server)) *Server {
	s := &Server{
		deps: deps,
		mcp: mcp.NewServer(&mcp.Implementation{Name: serverName, Version: deps.Version, Icons: serverIcons()}, &mcp.ServerOptions{
			Logger: deps.logger(),
			SupportedProtocolVersions: []string{
				protocolVersion20260728,
				protocolVersion20251125,
				protocolVersion20250618,
			},
		}),
	}
	for _, register := range groups {
		register(s)
	}

	return s
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
