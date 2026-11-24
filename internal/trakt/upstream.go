package trakt

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/samber/trakt-mcp/internal/store"
	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	// defaultAuthURL is Trakt's consent page.
	defaultAuthURL = "https://trakt.tv/oauth/authorize"
	// defaultTokenURL issues and refreshes Trakt tokens.
	defaultTokenURL = "https://api.trakt.tv/oauth/token" //nolint:gosec // G101: an endpoint URL, not a credential
	// defaultRevokeURL revokes a Trakt token and its grant.
	defaultRevokeURL = "https://api.trakt.tv/oauth/revoke"

	// grantAuthorizationCode and grantRefreshToken are the OAuth grant types
	// sent to the token endpoint (RFC 6749 §4.1.3, §6).
	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"
	// responseTypeCode asks Trakt for an authorization code.
	responseTypeCode = "code"
	// pkceMethodS256 is the only PKCE method this server uses.
	pkceMethodS256 = "S256"
	// oauthErrInvalidGrant is the OAuth error for a revoked, expired or already
	// used refresh token (RFC 6749 §5.2).
	oauthErrInvalidGrant = "invalid_grant"
	// reasonTraktRevoked is the "user disconnected" log reason when Trakt
	// reports the grant revoked.
	reasonTraktRevoked = "trakt_revoked"

	// expiryMargin treats an access token as expired slightly early, so a call
	// started just before expiry does not reach Trakt with a dead token.
	expiryMargin = time.Minute
	// refreshTimeout bounds one shared refresh, saves included. It runs
	// detached from the caller's context: once Trakt rotated the refresh
	// token, the new one must be saved even if the caller gave up.
	refreshTimeout = 30 * time.Second
	// maxRefreshAttempts allows one retry when a new consent replaced the
	// grant mid-refresh; a second change in the same instant is not worth
	// waiting for.
	maxRefreshAttempts = 2
	// maxForcedFlights allows one more flight when a forced refresh joined a
	// flight started without the rejected token, which can hand that same
	// token back.
	maxForcedFlights = 2
	// maxSaveAttempts retries a failed save of a rotated pair: the old refresh
	// token is already spent at Trakt, so losing the new one disconnects the
	// user, while SQLite failures are usually a brief lock or I/O hiccup.
	maxSaveAttempts = 3
	// saveRetryBackoff spaces save attempts enough to outlast a short SQLite
	// lock while keeping the whole refresh well under refreshTimeout.
	saveRetryBackoff = 100 * time.Millisecond
	// maxTokenBodyBytes bounds a token endpoint response; a real one is a few
	// hundred bytes.
	maxTokenBodyBytes = 16 << 10
	// maxSettingsBodyBytes bounds /users/settings, which carries every
	// browsing preference of the account and stays well under this.
	maxSettingsBodyBytes = 1 << 20
	// headerAccept names the response format asked of the OAuth endpoints.
	headerAccept = "Accept"
	// codeVerifierBytes is the entropy of a PKCE verifier; 32 bytes encode to
	// 43 characters, the minimum length RFC 7636 §4.1 allows.
	codeVerifierBytes = 32
	// redacted replaces the client secret wherever the config is printed.
	redacted = "[redacted]"
)

// OAuth parameter names shared by the consent URL and the JSON bodies sent to
// the token and revoke endpoints (RFC 6749, RFC 7636, RFC 7009).
const (
	paramClientID      = "client_id"
	paramClientSecret  = "client_secret"
	paramRedirectURI   = "redirect_uri"
	paramGrantType     = "grant_type"
	paramCode          = "code"
	paramCodeVerifier  = "code_verifier"
	paramRefreshToken  = "refresh_token"
	paramToken         = "token"
	paramResponseType  = "response_type"
	paramState         = "state"
	paramChallenge     = "code_challenge"
	paramChallengeMeth = "code_challenge_method"
)

// oauthErrorCode matches the shape of an RFC 6749 error code. Anything else
// in the error field is dropped rather than copied into an error message.
var oauthErrorCode = regexp.MustCompile(`^[a-z_]{1,64}$`)

// UpstreamConfig configures the Trakt OAuth side. Empty URLs select the
// production endpoints; tests point them at a fake.
// Printing or logging it never shows ClientSecret.
type UpstreamConfig struct {
	ClientID, ClientSecret, RedirectURL      string
	AuthURL, TokenURL, RevokeURL, APIBaseURL string
	// UserAgent identifies the project and version, never the user; it
	// defaults to "trakt-mcp".
	UserAgent string
	// HTTP defaults to a client with defaultHTTPTimeout. Redirects are never
	// followed.
	HTTP *http.Client
	// Logger defaults to slog.Default().
	Logger *slog.Logger
}

// String implements fmt.Stringer without the client secret, so %v and %+v
// of the config are safe to log.
func (c UpstreamConfig) String() string {
	return fmt.Sprintf("trakt.UpstreamConfig{ClientID:%q ClientSecret:%s RedirectURL:%q AuthURL:%q TokenURL:%q "+
		"RevokeURL:%q APIBaseURL:%q UserAgent:%q}",
		c.ClientID, redacted, c.RedirectURL, c.AuthURL, c.TokenURL, c.RevokeURL, c.APIBaseURL, c.UserAgent)
}

// GoString implements fmt.GoStringer without the client secret, for %#v.
func (c UpstreamConfig) GoString() string {
	return c.String()
}

// LogValue implements slog.LogValuer without the client secret.
func (c UpstreamConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("client_id", c.ClientID),
		slog.String("client_secret", redacted),
		slog.String("redirect_url", c.RedirectURL),
		slog.String("auth_url", c.AuthURL),
		slog.String("token_url", c.TokenURL),
		slog.String("revoke_url", c.RevokeURL),
		slog.String("api_base_url", c.APIBaseURL),
		slog.String("user_agent", c.UserAgent),
	)
}

// Profile is the Trakt account behind an access token.
type Profile struct {
	TraktID, Slug string
	VIP           bool
}

// tokenStore is the part of the store the upstream uses for Trakt tokens.
type tokenStore interface {
	TraktToken(ctx context.Context, userID string) (store.TraktToken, error)
	SaveTraktTokenIf(ctx context.Context, userID, expectedRefresh string, t store.TraktToken) (bool, error)
	DeleteUserIfRefresh(ctx context.Context, userID, expectedRefresh string) (bool, error)
}

// Upstream owns the Trakt OAuth client and every user's token source.
// Printing it never shows the client secret.
type Upstream struct {
	clientID, clientSecret, redirectURL string
	authURL, tokenURL, revokeURL        string
	userAgent                           string

	st   tokenStore
	http *http.Client
	api  *Client
	log  *slog.Logger
	now  func() time.Time

	// refreshes allows one in-flight refresh per user id. Trakt refresh
	// tokens are single-use, so two concurrent refreshes would make one fail
	// and could lose the grant.
	refreshes singleflight.Group
}

// NewUpstream returns an Upstream backed by st.
func NewUpstream(cfg UpstreamConfig, st *store.Store) *Upstream {
	return newUpstream(cfg, st)
}

func newUpstream(cfg UpstreamConfig, st tokenStore) *Upstream {
	hc := &http.Client{Timeout: defaultHTTPTimeout}

	if cfg.HTTP != nil {
		// A shallow copy, so the policy below never changes the caller's client.
		copied := *cfg.HTTP
		hc = &copied
	}
	// A redirected POST would lose its body, and the client secret could be
	// sent to another host.
	hc.CheckRedirect = noRedirect

	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}

	ua := orDefault(cfg.UserAgent, defaultUserAgent)

	return &Upstream{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURL:  cfg.RedirectURL,
		authURL:      orDefault(cfg.AuthURL, defaultAuthURL),
		tokenURL:     orDefault(cfg.TokenURL, defaultTokenURL),
		revokeURL:    orDefault(cfg.RevokeURL, defaultRevokeURL),
		userAgent:    ua,
		st:           st,
		http:         hc,
		api:          New(Config{BaseURL: cfg.APIBaseURL, APIKey: cfg.ClientID, UserAgent: ua, HTTP: cfg.HTTP}),
		log:          log,
		now:          time.Now,
	}
}

// String implements fmt.Stringer without the client secret; %v of the
// struct would otherwise print every field.
func (u *Upstream) String() string {
	return fmt.Sprintf("trakt.Upstream{ClientID:%q ClientSecret:%s TokenURL:%q}", u.clientID, redacted, u.tokenURL)
}

// GoString implements fmt.GoStringer without the client secret, for %#v.
func (u *Upstream) GoString() string {
	return u.String()
}

// NewCodeVerifier returns a PKCE code verifier: the base64url encoding,
// without padding, of codeVerifierBytes random bytes (RFC 7636 §4.1).
func NewCodeVerifier() (string, error) {
	b := make([]byte, codeVerifierBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("trakt: code verifier: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(b), nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}

// AuthCodeURL returns the Trakt consent URL with an S256 PKCE challenge
// derived from codeVerifier. The caller must pass a verifier of 43 to 128
// characters of the RFC 7636 unreserved set, carrying at least 32 random
// bytes, such as one from NewCodeVerifier; it is not checked here.
func (u *Upstream) AuthCodeURL(state, codeVerifier string) string {
	sum := sha256.Sum256([]byte(codeVerifier))

	q := url.Values{
		paramResponseType:  {responseTypeCode},
		paramClientID:      {u.clientID},
		paramRedirectURI:   {u.redirectURL},
		paramState:         {state},
		paramChallenge:     {base64.RawURLEncoding.EncodeToString(sum[:])},
		paramChallengeMeth: {pkceMethodS256},
	}

	return u.authURL + "?" + q.Encode()
}

// Exchange trades an authorization code for Trakt tokens. codeVerifier is
// the one passed to AuthCodeURL; see there for its required form.
func (u *Upstream) Exchange(ctx context.Context, code, codeVerifier string) (store.TraktToken, error) {
	tok, err := u.tokenRequest(ctx, map[string]string{
		paramGrantType:    grantAuthorizationCode,
		paramCode:         code,
		paramCodeVerifier: codeVerifier,
		paramClientID:     u.clientID,
		paramClientSecret: u.clientSecret,
		paramRedirectURI:  u.redirectURL,
	})
	if err != nil {
		return store.TraktToken{}, fmt.Errorf("trakt code exchange: %w", err)
	}

	if tok.RefreshToken == "" {
		return store.TraktToken{}, errors.New("trakt code exchange: response has no refresh token")
	}

	return tok, nil
}

// Revoke revokes a Trakt token, which ends the whole grant.
func (u *Upstream) Revoke(ctx context.Context, token string) error {
	status, _, err := u.postJSON(ctx, u.revokeURL, map[string]string{
		paramToken:        token,
		paramClientID:     u.clientID,
		paramClientSecret: u.clientSecret,
	})
	if err != nil {
		return fmt.Errorf("trakt revoke: %w", err)
	}

	if status != http.StatusOK {
		return fmt.Errorf("trakt revoke: HTTP %d", status)
	}

	return nil
}

// Profile reads the Trakt account behind accessToken from /users/settings.
// Only the user object is decoded: the generated response type maps every
// browsing preference, so one unexpected preference would fail the login.
func (u *Upstream) Profile(ctx context.Context, accessToken string) (Profile, error) {
	resp, err := u.api.For(staticToken(accessToken)).API().GetUsersSettings(ctx, &api.GetUsersSettingsParams{})
	if err != nil {
		return Profile{}, fmt.Errorf("trakt profile: %w", err)
	}

	var settings struct {
		User struct {
			IDs struct {
				Trakt *int64 `json:"trakt"`
				Slug  string `json:"slug"`
			} `json:"ids"`
			VIP bool `json:"vip"`
		} `json:"user"`
	}

	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, maxSettingsBodyBytes)).Decode(&settings)
	closeErr := resp.Body.Close()

	if decodeErr != nil {
		// The decoder error can quote the body, so it is dropped.
		return Profile{}, errors.New("trakt profile: malformed settings JSON")
	}

	if closeErr != nil {
		return Profile{}, fmt.Errorf("trakt profile: %w", closeErr)
	}

	ids := settings.User.IDs
	if ids.Trakt == nil || ids.Slug == "" {
		return Profile{}, errors.New("trakt profile: response lacks the user id or slug")
	}

	return Profile{TraktID: strconv.FormatInt(*ids.Trakt, 10), Slug: ids.Slug, VIP: settings.User.VIP}, nil
}

// Client returns a client acting as userID. It fails with ErrUpstreamAuth when
// the user has no stored Trakt grant.
func (u *Upstream) Client(ctx context.Context, userID string) (*UserClient, error) {
	if _, err := u.loadToken(ctx, userID); err != nil {
		return nil, err
	}

	return u.api.For(u.tokenSource(userID)), nil
}

func (u *Upstream) tokenSource(userID string) *userTokenSource {
	return &userTokenSource{up: u, userID: userID}
}

// loadToken reads the stored Trakt token of userID.
func (u *Upstream) loadToken(ctx context.Context, userID string) (store.TraktToken, error) {
	t, err := u.st.TraktToken(ctx, userID)
	if errors.Is(err, store.ErrNotFound) {
		return store.TraktToken{}, ErrUpstreamAuth
	}

	if err != nil {
		return store.TraktToken{}, err
	}

	return t, nil
}

// valid reports whether t can be sent now. A zero expiry is unknown and
// counts as valid until Trakt rejects the token.
func (u *Upstream) valid(t store.TraktToken) bool {
	if t.AccessToken == "" {
		return false
	}

	return t.Expiry.IsZero() || u.now().Add(expiryMargin).Before(t.Expiry)
}

// refresh refreshes userID's access token and stores the result. A non-empty
// rejected names an access token Trakt refused: a stored token equal to it is
// refreshed even though it has not expired. Each attempt re-reads the store
// first: a refresh or a new consent that finished just before already left a
// valid token there. The store is only changed while it still holds the
// refresh token this attempt used, so a new consent that lands mid-refresh is
// never overwritten or deleted; the next attempt then picks up the newer grant.
func (u *Upstream) refresh(ctx context.Context, userID, rejected string) (store.TraktToken, error) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()

	for range maxRefreshAttempts {
		tok, done, err := u.refreshOnce(ctx, userID, rejected)
		if err != nil || done {
			return tok, err
		}
	}

	return store.TraktToken{}, errors.New("trakt token refresh: grant changed during every attempt")
}

// refreshOnce runs one refresh attempt. done is false when the stored grant
// changed during the attempt and the caller should try again.
func (u *Upstream) refreshOnce(ctx context.Context, userID, rejected string) (tok store.TraktToken, done bool, err error) {
	stored, err := u.loadToken(ctx, userID)
	if err != nil {
		return store.TraktToken{}, true, err
	}

	if u.valid(stored) && stored.AccessToken != rejected {
		return stored, true, nil
	}

	fresh, err := u.tokenRequest(ctx, map[string]string{
		paramGrantType:    grantRefreshToken,
		paramRefreshToken: stored.RefreshToken,
		paramClientID:     u.clientID,
		paramClientSecret: u.clientSecret,
		paramRedirectURI:  u.redirectURL,
	})
	if err != nil {
		if te, ok := errors.AsType[*tokenError](err); !ok || te.code != oauthErrInvalidGrant {
			return store.TraktToken{}, true, fmt.Errorf("trakt token refresh: %w", err)
		}

		return u.forgetRevoked(ctx, userID, stored.RefreshToken)
	}

	// Trakt refresh tokens are single-use: the stored one is spent, so a
	// response without a replacement cannot be kept.
	if fresh.RefreshToken == "" {
		return store.TraktToken{}, true, errors.New("trakt token refresh: response has no refresh token")
	}

	saved, err := u.saveRotated(ctx, userID, stored.RefreshToken, fresh)
	if err != nil {
		return store.TraktToken{}, true, err
	}

	return fresh, saved, nil
}

// forgetRevoked deletes a user whose grant Trakt refused, so the MCP client
// restarts the OAuth flow, unless a new consent replaced the failing grant
// meanwhile; done is false in that case.
func (u *Upstream) forgetRevoked(ctx context.Context, userID, refresh string) (store.TraktToken, bool, error) {
	deleted, err := u.st.DeleteUserIfRefresh(ctx, userID, refresh)
	if err != nil {
		return store.TraktToken{}, true, errors.Join(ErrUpstreamAuth, err)
	}

	if !deleted {
		return store.TraktToken{}, false, nil
	}

	u.log.InfoContext(ctx, "user disconnected",
		slog.String("user_id", userID), slog.String("reason", reasonTraktRevoked))

	return store.TraktToken{}, true, ErrUpstreamAuth
}

// saveRotated stores a pair Trakt just issued, retrying failed saves. When
// every attempt fails, the old row stays untouched: its refresh token is
// already spent at Trakt, so the user will have to reconnect, but deleting
// the row here would also lose a grant that a concurrent consent may have
// stored. saved is false when such a consent replaced the grant.
func (u *Upstream) saveRotated(ctx context.Context, userID, oldRefresh string, fresh store.TraktToken) (bool, error) {
	var lastErr error

	for attempt := range maxSaveAttempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				lastErr = errors.Join(lastErr, ctx.Err())

				return false, u.saveFailed(ctx, userID, lastErr)
			case <-time.After(saveRetryBackoff):
			}
		}

		saved, err := u.st.SaveTraktTokenIf(ctx, userID, oldRefresh, fresh)
		if err == nil {
			return saved, nil
		}

		lastErr = err
	}

	return false, u.saveFailed(ctx, userID, lastErr)
}

func (u *Upstream) saveFailed(ctx context.Context, userID string, err error) error {
	u.log.ErrorContext(ctx, "trakt token save failed after refresh; user must reconnect",
		slog.String("user_id", userID))

	return fmt.Errorf("trakt token refresh: save rotated token: %w", err)
}

// tokenError is a token endpoint refusal. It keeps the status and the OAuth
// error code only, never the body, which can echo request values.
type tokenError struct {
	status int
	code   string
}

func (e *tokenError) Error() string {
	if e.code == "" {
		return fmt.Sprintf("token endpoint returned HTTP %d", e.status)
	}

	return fmt.Sprintf("token endpoint returned HTTP %d (%s)", e.status, e.code)
}

// tokenRequest posts a grant to the token endpoint and decodes the issued
// pair. Errors never carry the response body or any request value.
func (u *Upstream) tokenRequest(ctx context.Context, body map[string]string) (store.TraktToken, error) {
	status, raw, err := u.postJSON(ctx, u.tokenURL, body)
	if err != nil {
		return store.TraktToken{}, err
	}

	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		var oe struct {
			Error string `json:"error"`
		}
		// A body that is not JSON just leaves the code empty.
		if json.Unmarshal(raw, &oe) != nil || !oauthErrorCode.MatchString(oe.Error) {
			oe.Error = ""
		}

		return store.TraktToken{}, &tokenError{status: status, code: oe.Error}
	}

	var tr struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		CreatedAt    int64  `json:"created_at"`
	}

	if err := json.Unmarshal(raw, &tr); err != nil {
		// The decoder error can quote the body, so it is dropped.
		return store.TraktToken{}, fmt.Errorf("token endpoint returned malformed JSON (HTTP %d)", status)
	}

	if tr.AccessToken == "" {
		return store.TraktToken{}, fmt.Errorf("token endpoint returned no access token (HTTP %d)", status)
	}

	tok := store.TraktToken{AccessToken: tr.AccessToken, RefreshToken: tr.RefreshToken}

	if tr.ExpiresIn > 0 {
		tok.Expiry = u.expiry(ctx, tr.CreatedAt, time.Duration(tr.ExpiresIn)*time.Second)
	}

	return tok, nil
}

// expiry returns created+lifetime, or now+lifetime when created is absent or
// would make the token look expired already. A skewed clock, or a refresh
// response repeating the original grant's created_at, would otherwise mark
// every new token expired and start a new refresh on each call, rotating the
// grant again and again.
func (u *Upstream) expiry(ctx context.Context, created int64, lifetime time.Duration) time.Time {
	now := u.now()
	fromNow := now.Add(lifetime)

	if created <= 0 {
		return fromNow
	}

	fromCreated := time.Unix(created, 0).Add(lifetime)
	if fromCreated.After(now.Add(expiryMargin)) {
		return fromCreated
	}

	u.log.DebugContext(ctx, "trakt token created_at gives a past expiry; counting from now")

	return fromNow
}

// postJSON posts body as JSON to target and returns the status and a bounded
// copy of the response body.
func (u *Upstream) postJSON(ctx context.Context, target string, body map[string]string) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("encode request: %w", err)
	}

	// target is a configured Trakt endpoint, never user input.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(payload)) //nolint:gosec // G704: see above
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set(headerCType, jsonContentType)
	req.Header.Set(headerAccept, jsonContentType)
	req.Header.Set(headerUserAgent, u.userAgent)

	resp, err := u.http.Do(req) //nolint:gosec // G704: target is a configured endpoint
	if err != nil {
		return 0, nil, fmt.Errorf("request failed: %w", redactURLError(err))
	}

	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxTokenBodyBytes))
	closeErr := resp.Body.Close()

	if err := errors.Join(readErr, closeErr); err != nil {
		return 0, nil, fmt.Errorf("read response (HTTP %d): %w", resp.StatusCode, err)
	}

	return resp.StatusCode, raw, nil
}

// redactURLError keeps the operation and cause of a transport error. The
// URL is a configured endpoint without secrets, but dropping it keeps errors
// short and stable.
func redactURLError(err error) error {
	if ue, ok := errors.AsType[*url.Error](err); ok {
		return fmt.Errorf("%s: %w", ue.Op, ue.Err)
	}

	return err
}

// userTokenSource yields a valid access token for one user, refreshing it
// through the store so every client of the user in this process shares one
// refresh.
type userTokenSource struct {
	up     *Upstream
	userID string
}

// AccessToken implements TokenSource.
func (s *userTokenSource) AccessToken(ctx context.Context) (string, error) {
	tok, err := s.up.loadToken(ctx, s.userID)
	if err != nil {
		return "", err
	}

	if s.up.valid(tok) {
		return tok.AccessToken, nil
	}

	return s.shared(ctx, "")
}

// Rejected implements TokenSource. A refresh that finds the grant revoked
// deletes the user and returns ErrUpstreamAuth. An empty rejected matches no
// stored token, so it returns the current token, refreshed only if expired,
// like AccessToken.
func (s *userTokenSource) Rejected(ctx context.Context, rejected string) (string, error) {
	for range maxForcedFlights {
		tok, err := s.shared(ctx, rejected)
		if err != nil {
			return "", err
		}

		if tok != rejected {
			return tok, nil
		}
	}

	return "", ErrUpstreamAuth
}

// shared runs the refresh in the user's single flight. The flight runs
// detached from ctx so that one caller giving up neither cancels the others
// nor interrupts the save of a rotated pair; ctx only bounds this caller's
// wait.
func (s *userTokenSource) shared(ctx context.Context, rejected string) (string, error) {
	detached := context.WithoutCancel(ctx)

	ch := s.up.refreshes.DoChan(s.userID, func() (any, error) {
		return s.up.refresh(detached, s.userID, rejected)
	})

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}

		tok, ok := res.Val.(store.TraktToken)
		if !ok {
			return "", fmt.Errorf("trakt token refresh: unexpected result %T", res.Val)
		}

		return tok.AccessToken, nil
	}
}

// staticToken is a TokenSource for one access token that is never refreshed,
// used while the user has no stored grant yet.
type staticToken string

func (t staticToken) AccessToken(context.Context) (string, error) { return string(t), nil }

func (t staticToken) Rejected(context.Context, string) (string, error) {
	return "", ErrUpstreamAuth
}
