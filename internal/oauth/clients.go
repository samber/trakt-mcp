// Package oauth implements this server's OAuth 2.1 authorization server
// pieces around fosite: client resolution (CIMD and DCR) and redirect matching.
package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ory/fosite"
	"golang.org/x/sync/singleflight"

	"github.com/samber/trakt-mcp/internal/store"
)

const (
	// clientIDBytes of randomness make DCR client ids unguessable (256 bits).
	clientIDBytes = 32
	// maxRegistrationBody bounds a DCR request; real ones are under 1 KiB.
	maxRegistrationBody int64 = 64 << 10
	// cimdIDPrefix marks a client_id that is a metadata document URL.
	cimdIDPrefix = "https://"
	// schemeHTTP is allowed for DCR redirect URIs on loopback hosts only (RFC 8252).
	schemeHTTP = "http"
	// privateUseSchemeDot is the character a private-use scheme must contain:
	// RFC 8252 §7.1 requires a reverse domain name such as "com.example.app".
	// A dotless scheme cannot be javascript, data, file or any other scheme
	// that browsers or the OS treat as active content, so the dot rule doubles
	// as the deny-list.
	privateUseSchemeDot = "."

	// cacheControlNoStore keeps registration responses out of caches.
	cacheControlNoStore = "no-store"

	// RFC 7591 §3.2.2 error codes.
	errInvalidRedirectURI    = "invalid_redirect_uri"
	errInvalidClientMetadata = "invalid_client_metadata"
	// errTemporarilyUnavailable is the RFC 6749 code sent with a 429 when the
	// registration budget is spent.
	errTemporarilyUnavailable = "temporarily_unavailable"

	// maxRedirectURIs, maxRedirectURIBytes and maxClientNameRunes bound what an
	// unauthenticated caller can store per client row (DCR or CIMD). Real
	// clients register one to three short loopback or https URIs and a short
	// product name.
	maxRedirectURIs     = 10
	maxRedirectURIBytes = 2 << 10
	maxClientNameRunes  = 100
	// maxStaleness caps how long past its expiry a cached CIMD document is
	// still served when its host cannot be reached: a week rides out an
	// outage without trusting a dead client forever.
	maxStaleness = 7 * 24 * time.Hour

	// RFC 7591 application_type values; "web" is the default when absent.
	applicationTypeWeb    = "web"
	applicationTypeNative = "native"
)

// loopbackHosts are the hostnames treated as loopback for redirect matching
// and DCR http redirect URIs (url.URL.Hostname strips IPv6 brackets).
var loopbackHosts = []string{"127.0.0.1", "localhost", "::1"}

// supportedGrantTypes and supportedResponseTypes mirror store.OAuthClient.
var (
	supportedGrantTypes    = []string{"authorization_code", "refresh_token"}
	supportedResponseTypes = []string{"code"}
)

// ClientResolver resolves fosite clients: https:// ids through the CIMD cache
// and fetcher, every other id from DCR rows.
type ClientResolver struct {
	st     *store.Store
	f      *CIMDFetcher
	log    *slog.Logger
	flight singleflight.Group
	// dcrLimit and cimdLimit are the global budgets of DCR registrations and
	// CIMD fetches, the two ways an unauthenticated caller writes a client row.
	dcrLimit, cimdLimit *bucket
	// now is replaced in tests.
	now func() time.Time
}

// NewClientResolver returns a resolver backed by st and f.
func NewClientResolver(st *store.Store, f *CIMDFetcher) *ClientResolver {
	return &ClientResolver{
		st: st, f: f, log: slog.Default(), now: time.Now,
		dcrLimit:  newBucket(clientCreateRate, clientCreateBurst),
		cimdLimit: newBucket(clientCreateRate, clientCreateBurst),
	}
}

// GetClient implements fosite.ClientManager's lookup. Unknown non-URL ids
// return fosite.ErrNotFound.
func (r *ClientResolver) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	if strings.HasPrefix(id, cimdIDPrefix) {
		return r.cimdClient(ctx, id)
	}

	c, err := r.st.ClientByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fosite.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	if c.Kind != store.ClientKindDCR {
		return nil, fosite.ErrNotFound
	}

	return c, nil
}

func (r *ClientResolver) cimdClient(ctx context.Context, id string) (fosite.Client, error) {
	cached, err := r.st.ClientByID(ctx, id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		cached = nil
	case err != nil:
		return nil, err
	case cached.Kind != store.ClientKindCIMD:
		cached = nil
	case r.now().Before(cached.ExpiresAt):
		return cached, nil
	}

	// Concurrent cold lookups of one client share a single fetch. The shared
	// fetch must not die with whichever caller started it; the fetcher's own
	// timeout still bounds it.
	v, err, _ := r.flight.Do(id, func() (any, error) {
		if ok, _ := r.cimdLimit.take(r.now()); !ok {
			return nil, ErrClientRateLimited
		}

		c, _, ferr := r.f.Fetch(context.WithoutCancel(ctx), id)
		if ferr != nil {
			return nil, ferr
		}

		if serr := r.st.SaveClient(context.WithoutCancel(ctx), c); serr != nil {
			return nil, serr
		}

		return c, nil
	})
	if err != nil {
		return r.afterFetchFailure(ctx, id, cached, err)
	}

	c, ok := v.(*store.OAuthClient)
	if !ok {
		return nil, fmt.Errorf("oauth: resolve client %q: unexpected %T", id, v)
	}

	return c, nil
}

// afterFetchFailure decides what a failed CIMD (re)fetch yields. A transient
// failure (outage, timeout, spent fetch budget) serves the cached document for
// up to maxStaleness past its expiry, so token refresh keeps working for
// connected users. A definitive answer from the client's host (404, 410, 400)
// means the document is gone: the cached row is deleted and the client fails.
func (r *ClientResolver) afterFetchFailure(ctx context.Context, id string, cached *store.OAuthClient, err error) (fosite.Client, error) {
	var se *fetchStatusError
	if errors.As(err, &se) && se.definitive() {
		if cached != nil {
			if derr := r.st.DeleteClient(context.WithoutCancel(ctx), id); derr != nil {
				r.log.ErrorContext(ctx, "oauth: delete withdrawn client metadata",
					slog.String("client_id", id), slog.Any("error", derr))
			}

			r.log.InfoContext(ctx, "oauth: client metadata withdrawn, cached copy deleted",
				slog.String("client_id", id), slog.Int("status", se.status))
		}

		return nil, fmt.Errorf("oauth: resolve client %q: %w", id, err)
	}

	if cached != nil && r.now().Before(cached.ExpiresAt.Add(maxStaleness)) {
		r.log.WarnContext(ctx, "serving stale client metadata after refetch failure",
			slog.String("client_id", id), slog.Any("error", err))

		return cached, nil
	}

	return nil, fmt.Errorf("oauth: resolve client %q: %w", id, err)
}

// RedirectMatches reports whether got equals a registered redirect URI. For
// loopback hosts the port is ignored (RFC 8252 §7.3): native clients such as
// Claude Code listen on an ephemeral port.
func RedirectMatches(registered []string, got string) bool {
	if slices.Contains(registered, got) {
		return true
	}

	g, err := url.Parse(got)
	if err != nil || !isLoopbackHost(g.Hostname()) || g.User != nil || g.Fragment != "" {
		return false
	}

	for _, reg := range registered {
		r, err := url.Parse(reg)
		if err != nil || r.User != nil {
			continue
		}

		if r.Scheme == g.Scheme && r.Hostname() == g.Hostname() &&
			r.EscapedPath() == g.EscapedPath() && r.RawQuery == g.RawQuery {
			return true
		}
	}

	return false
}

func isLoopbackHost(host string) bool {
	return slices.Contains(loopbackHosts, strings.ToLower(host))
}

// registrationRequest holds the RFC 7591 fields this server reads.
type registrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name"`
	ApplicationType         string   `json:"application_type"`
}

type registrationResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name"`
}

type registrationError struct {
	Error       string `json:"error"`
	Description string `json:"error_description"`
}

// RegisterHandler serves RFC 7591 Dynamic Client Registration for public
// clients only.
func (r *ClientResolver) RegisterHandler() http.Handler {
	return http.HandlerFunc(r.register)
}

func (r *ClientResolver) register(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)

		return
	}

	var in registrationRequest

	dec := json.NewDecoder(http.MaxBytesReader(w, req.Body, maxRegistrationBody))
	if err := dec.Decode(&in); err != nil {
		if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
			r.writeError(w, req, errInvalidClientMetadata, "request body too large")
			return
		}

		r.writeError(w, req, errInvalidClientMetadata, "request body is not a JSON object")

		return
	}

	in.ClientName = strings.TrimSpace(in.ClientName)
	if code, desc := validateRegistration(&in); code != "" {
		r.writeError(w, req, code, desc)
		return
	}

	if ok, retry := r.dcrLimit.take(r.now()); !ok {
		r.log.WarnContext(req.Context(), "dcr: registration budget spent, refusing")
		w.Header().Set(headerRetryAfter, retryAfterValue(retry))
		r.writeJSON(w, req, http.StatusTooManyRequests, registrationError{
			Error: errTemporarilyUnavailable, Description: "too many registrations, retry later",
		})

		return
	}

	raw := make([]byte, clientIDBytes)
	if _, err := rand.Read(raw); err != nil {
		r.log.ErrorContext(req.Context(), "dcr: generate client id", slog.Any("error", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	name := in.ClientName
	if name == "" {
		// Validated above: the first redirect URI parses and is allowed.
		if u, err := url.Parse(in.RedirectURIs[0]); err == nil {
			name = redirectDisplayHost(u)
		}
	}

	c := &store.OAuthClient{
		ID:           hex.EncodeToString(raw),
		Kind:         store.ClientKindDCR,
		Name:         name,
		RedirectURIs: in.RedirectURIs,
	}
	if err := r.st.SaveClient(req.Context(), c); err != nil {
		r.log.ErrorContext(req.Context(), "dcr: save client", slog.Any("error", err))
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)

		return
	}

	r.log.InfoContext(req.Context(), "dcr: registered client",
		slog.String("client_id", c.ID), slog.String("client_name", c.Name))

	r.writeJSON(w, req, http.StatusCreated, registrationResponse{
		ClientID:                c.ID,
		ClientIDIssuedAt:        r.now().Unix(),
		RedirectURIs:            c.RedirectURIs,
		TokenEndpointAuthMethod: authMethodNone,
		GrantTypes:              supportedGrantTypes,
		ResponseTypes:           supportedResponseTypes,
		ClientName:              c.Name,
	})
}

// validateRegistration returns an RFC 7591 error code and description, or
// empty strings when in is acceptable.
func validateRegistration(in *registrationRequest) (code, desc string) {
	if in.TokenEndpointAuthMethod != "" && in.TokenEndpointAuthMethod != authMethodNone {
		return errInvalidClientMetadata, "only public clients are supported: token_endpoint_auth_method must be \"none\""
	}

	if in.ApplicationType != "" && in.ApplicationType != applicationTypeWeb && in.ApplicationType != applicationTypeNative {
		return errInvalidClientMetadata, "application_type must be \"web\" or \"native\""
	}

	for _, g := range in.GrantTypes {
		if !slices.Contains(supportedGrantTypes, g) {
			return errInvalidClientMetadata, fmt.Sprintf("unsupported grant_type %q", g)
		}
	}

	for _, rt := range in.ResponseTypes {
		if !slices.Contains(supportedResponseTypes, rt) {
			return errInvalidClientMetadata, fmt.Sprintf("unsupported response_type %q", rt)
		}
	}

	if utf8.RuneCountInString(in.ClientName) > maxClientNameRunes {
		return errInvalidClientMetadata, fmt.Sprintf("client_name must be at most %d characters", maxClientNameRunes)
	}

	return validateRedirectURIs(in.RedirectURIs)
}

// validateRedirectURIs applies the redirect_uris rules of validateRegistration.
func validateRedirectURIs(uris []string) (code, desc string) {
	if len(uris) == 0 {
		return errInvalidRedirectURI, "redirect_uris is required"
	}

	if len(uris) > maxRedirectURIs {
		return errInvalidRedirectURI, fmt.Sprintf("at most %d redirect_uris are accepted", maxRedirectURIs)
	}

	for _, ru := range uris {
		if len(ru) > maxRedirectURIBytes {
			return errInvalidRedirectURI, fmt.Sprintf("each redirect_uri must be at most %d bytes", maxRedirectURIBytes)
		}

		u, err := url.Parse(ru)
		if err != nil || !u.IsAbs() || u.Fragment != "" || strings.Contains(ru, "#") {
			return errInvalidRedirectURI, fmt.Sprintf("redirect_uri %q must be an absolute URI without fragment", ru)
		}

		if !isAllowedRedirect(u) {
			return errInvalidRedirectURI, fmt.Sprintf(
				"redirect_uri %q must use https, http on a loopback host, or a reverse-domain private-use scheme (RFC 8252 section 7.1) with a path, and no userinfo", ru)
		}
	}

	return "", ""
}

// isAllowedRedirect reports whether u may receive an authorization response:
// https, http on a loopback host (RFC 8252 §7.3), or a private-use URI scheme
// (RFC 8252 §7.1). Userinfo and fragments are refused for every kind.
func isAllowedRedirect(u *url.URL) bool {
	if u.User != nil || u.Fragment != "" {
		return false
	}

	switch u.Scheme {
	case schemeHTTPS:
		return u.Hostname() != ""
	case schemeHTTP:
		return u.Hostname() != "" && isLoopbackHost(u.Hostname())
	default:
		return isPrivateUseRedirect(u)
	}
}

// isPrivateUseRedirect reports whether u uses a reverse-domain private-use
// scheme and carries a host or a path, as in "com.example.app:/callback". An
// opaque form ("com.example.app:callback") has neither, so it is refused.
func isPrivateUseRedirect(u *url.URL) bool {
	return strings.Contains(u.Scheme, privateUseSchemeDot) && u.Opaque == "" && (u.Host != "" || u.Path != "")
}

// redirectDisplayHost names where a redirect leads, for people: the host, or
// the scheme for a private-use URI that has none.
func redirectDisplayHost(u *url.URL) string {
	if host := u.Hostname(); host != "" {
		return host
	}

	return u.Scheme
}

func (r *ClientResolver) writeError(w http.ResponseWriter, req *http.Request, code, desc string) {
	r.writeJSON(w, req, http.StatusBadRequest, registrationError{Error: code, Description: desc})
}

// writeJSON sends v with no-store caching (RFC 7591 §3.2.1). A failed write
// means the client went away; there is nothing left to send it.
func (r *ClientResolver) writeJSON(w http.ResponseWriter, req *http.Request, status int, v any) {
	w.Header().Set("Content-Type", mediaTypeJSON)
	w.Header().Set("Cache-Control", cacheControlNoStore)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		r.log.DebugContext(req.Context(), "dcr: write response", slog.Any("error", err))
	}
}
