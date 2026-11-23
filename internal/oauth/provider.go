package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/ory/fosite"
	"github.com/ory/fosite/compose"

	"github.com/samber/trakt-mcp/internal/config"
	"github.com/samber/trakt-mcp/internal/crypto"
	"github.com/samber/trakt-mcp/internal/google"
	"github.com/samber/trakt-mcp/internal/obs"
	"github.com/samber/trakt-mcp/internal/store"
)

const (
	// accessTokenTTL, authCodeTTL and refreshTokenTTL are the lifespans fixed by the spec (§6.4).
	accessTokenTTL  = time.Hour
	authCodeTTL     = 10 * time.Minute
	refreshTokenTTL = 180 * 24 * time.Hour
	// pendingTTL bounds the time between our /authorize and Google's callback.
	pendingTTL = 10 * time.Minute

	// hmacLabel is the HKDF label deriving fosite's HMAC secret from the keyring (spec §6.4).
	hmacLabel = "fosite-hmac"
	// MCPScope is the only scope this server grants.
	MCPScope = "contacts"
	// mcpPath is the MCP endpoint; <base-url>/mcp is the token audience (RFC 8707).
	mcpPath = "/mcp"

	// googleIssuer is the ID token issuer; go-oidc also accepts its scheme-less form.
	googleIssuer = "https://accounts.google.com"
	// googleJWKSURL publishes the keys signing Google ID tokens.
	googleJWKSURL = "https://www.googleapis.com/oauth2/v3/certs"
	// jwksFetchTimeout keeps a cold key fetch inside the callback's latency budget.
	jwksFetchTimeout = 10 * time.Second

	// randomTokenBytes of randomness make pending ids and CSRF tokens unguessable (256 bits).
	randomTokenBytes = 32
	// uidKey is the auth.TokenInfo.Extra key holding our internal user id.
	uidKey = "uid"
)

// Paths served by Mount (spec §5.1).
const (
	pathASMetadata       = "/.well-known/oauth-authorization-server"
	pathResourceMetadata = "/.well-known/oauth-protected-resource"
	pathAuthorize        = "/oauth/authorize"
	pathConsent          = "/oauth/consent"
	pathCallback         = "/oauth/google/callback"
	pathToken            = "/oauth/token" //nolint:gosec // G101: an endpoint path, not a credential
	pathRegister         = "/oauth/register"
	pathRevoke           = "/oauth/revoke"
)

// Deps are the collaborators of a Server.
type Deps struct {
	Store    *store.Store
	Clients  *ClientResolver
	Upstream *google.Upstream
	Keyring  *crypto.Keyring
	// IDTokenVerifier checks Google ID tokens; nil selects Google's
	// production issuer and keys.
	IDTokenVerifier *oidc.IDTokenVerifier
	// Logger defaults to slog.Default().
	Logger *slog.Logger
	// Metrics counts issued grants; nil disables it.
	Metrics *obs.Metrics
	// Now defaults to time.Now; tests replace it to expire pending requests.
	Now func() time.Time
}

// Server is this server's OAuth 2.1 authorization server toward MCP clients,
// logging users in through Google.
type Server struct {
	provider fosite.OAuth2Provider
	// fositeConfig is read by fosite on every request; tests shorten lifespans through it.
	fositeConfig *fosite.Config
	st           *store.Store
	clients      *ClientResolver
	up           *google.Upstream
	idTokens     *oidc.IDTokenVerifier
	log          *slog.Logger
	metrics      *obs.Metrics
	now          func() time.Time
	templates    *template.Template
	// pendingLimit is the global budget of pending authorizations.
	pendingLimit *bucket

	issuer         string
	resource       string
	enableDCR      bool
	allowedDomains []string
}

// New builds a Server from the validated configuration.
func New(cfg config.Config, deps Deps) (*Server, error) {
	if cfg.BaseURL == nil {
		return nil, errors.New("oauth: base URL is required")
	}

	if deps.Store == nil || deps.Clients == nil || deps.Upstream == nil || deps.Keyring == nil {
		return nil, errors.New("oauth: store, clients, upstream and keyring are required")
	}

	issuer := strings.TrimSuffix(cfg.BaseURL.String(), "/")

	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}

	s := &Server{
		st:           deps.Store,
		clients:      deps.Clients,
		up:           deps.Upstream,
		idTokens:     deps.IDTokenVerifier,
		log:          deps.Logger,
		metrics:      deps.Metrics,
		now:          deps.Now,
		templates:    tmpl,
		pendingLimit: newBucket(pendingRate, pendingBurst),
		issuer:       issuer,
		resource:     issuer + mcpPath,
		enableDCR:    cfg.EnableDCR,
	}
	if s.log == nil {
		s.log = slog.Default()
	}

	if s.now == nil {
		s.now = time.Now
	}

	if s.idTokens == nil {
		ctx := oidc.ClientContext(context.Background(), &http.Client{Timeout: jwksFetchTimeout})
		s.idTokens = oidc.NewVerifier(googleIssuer, oidc.NewRemoteKeySet(ctx, googleJWKSURL),
			&oidc.Config{ClientID: cfg.TraktClientID})
	}

	secret, rotated := deps.Keyring.HMACSecrets(hmacLabel)
	fcfg := &fosite.Config{
		AccessTokenLifespan:            accessTokenTTL,
		AuthorizeCodeLifespan:          authCodeTTL,
		RefreshTokenLifespan:           refreshTokenTTL,
		GlobalSecret:                   secret,
		RotatedGlobalSecrets:           rotated,
		ScopeStrategy:                  fosite.ExactScopeStrategy,
		EnforcePKCE:                    true,
		EnablePKCEPlainChallengeMethod: false,
		// Empty, not nil: every grant gets a refresh token without asking for
		// an offline scope, which MCP clients never request.
		RefreshTokenScopes: []string{},
		// fosite's strict checker refuses private-use schemes, which
		// isAllowedRedirect accepts alongside https and loopback http.
		RedirectSecureChecker: func(_ context.Context, u *url.URL) bool { return isAllowedRedirect(u) },
		TokenURL:              issuer + pathToken,
		AccessTokenIssuer:     issuer,
	}
	s.fositeConfig = fcfg
	storage := &fositeStorage{Store: deps.Store, clients: deps.Clients, audience: s.resource}
	s.provider = compose.Compose(fcfg, storage, compose.NewOAuth2HMACStrategy(fcfg),
		compose.OAuth2AuthorizeExplicitFactory,
		compose.OAuth2RefreshTokenGrantFactory,
		compose.OAuth2TokenRevocationFactory,
		compose.OAuth2TokenIntrospectionFactory,
		compose.OAuth2PKCEFactory,
	)

	return s, nil
}

// Mount registers the OAuth endpoints of spec §5.1 on mux. The registration
// endpoint is mounted only with DCR enabled.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+pathASMetadata, s.serveASMetadata)
	prm := s.resourceMetadataHandler()
	mux.Handle(pathResourceMetadata, prm)
	// RFC 9728 §3.1 inserts the resource path after the well-known prefix;
	// clients that build the URL from <base-url>/mcp look there.
	mux.Handle(pathResourceMetadata+mcpPath, prm)
	mux.HandleFunc("GET "+pathAuthorize, s.authorize)
	mux.HandleFunc("POST "+pathConsent, s.consent)
	mux.HandleFunc("GET "+pathCallback, s.callback)
	mux.HandleFunc("POST "+pathToken, s.token)
	mux.HandleFunc("POST "+pathRevoke, s.revoke)

	if s.enableDCR {
		mux.Handle(pathRegister, s.clients.RegisterHandler())
	}
}

// ResourceMetadataURL is the RFC 9728 metadata URL that 401 answers on /mcp
// point clients to (spec §5.2).
func (s *Server) ResourceMetadataURL() string {
	return s.issuer + pathResourceMetadata
}

// Verifier returns the go-sdk bearer token verifier: the token must be one of
// our live access tokens, for audience <base-url>/mcp with scope contacts.
func (s *Server) Verifier() auth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		use, ar, err := s.provider.IntrospectToken(ctx, token, fosite.AccessToken, new(fosite.DefaultSession))
		if err != nil {
			if isInvalidTokenError(err) {
				return nil, fmt.Errorf("%w: token is unknown, expired or revoked", auth.ErrInvalidToken)
			}
			// fosite wraps storage failures in request_unauthorized too; only the
			// cause tells them apart. A 401 here would make every client re-run
			// OAuth during a database hiccup.
			s.log.ErrorContext(ctx, "oauth: introspect access token", slog.Any("error", fosite.ErrorToRFC6749Error(err).Cause()))

			return nil, errVerifierUnavailable
		}

		if use != fosite.AccessToken {
			return nil, fmt.Errorf("%w: not an access token", auth.ErrInvalidToken)
		}

		if !slices.Contains(ar.GetGrantedAudience(), s.resource) {
			return nil, fmt.Errorf("%w: token audience is not %s", auth.ErrInvalidToken, s.resource)
		}

		if !slices.Contains(ar.GetGrantedScopes(), MCPScope) {
			return nil, fmt.Errorf("%w: token lacks scope %s", auth.ErrInvalidToken, MCPScope)
		}

		sess := ar.GetSession()

		uid := sess.GetSubject()
		if uid == "" {
			return nil, fmt.Errorf("%w: token has no subject", auth.ErrInvalidToken)
		}

		return &auth.TokenInfo{
			Scopes:     ar.GetGrantedScopes(),
			Expiration: sess.GetExpiresAt(fosite.AccessToken),
			UserID:     uid,
			Extra:      map[string]any{uidKey: uid},
		}, nil
	}
}

// errVerifierUnavailable is sent to the client (go-sdk writes it into a
// 500 body) instead of the storage error, which can name internals.
var errVerifierUnavailable = errors.New("oauth: token verification is temporarily unavailable")

// invalidTokenCauses are the fosite errors meaning the token itself is bad:
// unknown signature, revoked or rotated, expired, or malformed.
var invalidTokenCauses = []error{
	fosite.ErrNotFound,
	fosite.ErrInactiveToken,
	fosite.ErrTokenExpired,
	fosite.ErrTokenSignatureMismatch,
	fosite.ErrInvalidTokenFormat,
}

func isInvalidTokenError(err error) bool {
	for _, cause := range invalidTokenCauses {
		if errors.Is(err, cause) {
			return true
		}
	}

	return false
}

// UserID returns our internal user id from the token info that
// auth.RequireBearerToken stored in ctx.
func UserID(ctx context.Context) (string, bool) {
	ti := auth.TokenInfoFromContext(ctx)
	if ti == nil {
		return "", false
	}

	uid, ok := ti.Extra[uidKey].(string)

	return uid, ok && uid != ""
}

// fositeStorage is the fosite storage: the store, with clients resolved
// through ClientResolver so CIMD clients are fetched on first use.
type fositeStorage struct {
	*store.Store
	clients  *ClientResolver
	audience string
}

// approvedRedirectKey carries a redirect URI that authorize already checked
// with RedirectMatches, for the duration of one fosite authorize call.
type approvedRedirectKey struct{}

type approvedRedirect struct{ clientID, uri string }

// GetClient implements fosite.ClientManager.
func (f *fositeStorage) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	c, err := f.clients.GetClient(ctx, id)
	if err != nil {
		return nil, err
	}

	pc := &providerClient{Client: c, audience: f.audience}
	if a, ok := ctx.Value(approvedRedirectKey{}).(approvedRedirect); ok && a.clientID == id {
		pc.approvedRedirect = a.uri
	}

	return pc, nil
}

// providerClient adapts a resolved client for fosite. Every client may only
// get the MCP audience, which fosite's refresh grant checks against
// GetAudience. fosite's own loopback rule covers IP literals only, so a
// localhost redirect on another port, already accepted by RedirectMatches,
// is added to the redirect list for that one authorize call.
type providerClient struct {
	fosite.Client
	audience         string
	approvedRedirect string
}

// GetAudience implements fosite.Client.
func (c *providerClient) GetAudience() fosite.Arguments { return fosite.Arguments{c.audience} }

// GetRedirectURIs implements fosite.Client.
func (c *providerClient) GetRedirectURIs() []string {
	uris := c.Client.GetRedirectURIs()
	if c.approvedRedirect == "" || slices.Contains(uris, c.approvedRedirect) {
		return uris
	}

	return append(slices.Clip(uris), c.approvedRedirect)
}

// name returns the client's display name, empty when it has none.
func (c *providerClient) name() string {
	if oc, ok := c.Client.(*store.OAuthClient); ok {
		return oc.Name
	}

	return ""
}

// randomToken returns a URL-safe random string.
func randomToken() (string, error) {
	b := make([]byte, randomTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: read random: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}
