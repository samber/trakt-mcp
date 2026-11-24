package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/ory/fosite"
	"github.com/ory/fosite/handler/oauth2"
	"github.com/ory/fosite/handler/pkce"
)

// Compile-time checks that Store is a complete fosite storage backend.
var (
	_ fosite.ClientManager          = (*Store)(nil)
	_ oauth2.CoreStorage            = (*Store)(nil)
	_ oauth2.TokenRevocationStorage = (*Store)(nil)
	_ pkce.PKCERequestStorage       = (*Store)(nil)
	_ fosite.Client                 = (*OAuthClient)(nil)
)

const (
	// ClientKindCIMD marks a client resolved from a Client ID Metadata Document.
	ClientKindCIMD = "cimd"
	// ClientKindDCR marks a client created by Dynamic Client Registration.
	ClientKindDCR = "dcr"

	// mcpScope is the only scope this server grants.
	mcpScope = "trakt"
	// grantAuthorizationCode and grantRefreshToken are the only grants a public MCP client needs.
	grantAuthorizationCode = "authorization_code"
	grantRefreshToken      = "refresh_token"
	// responseTypeCode is the only response type: implicit and hybrid flows are not offered.
	responseTypeCode = "code"
)

// Values of oauth_tokens.kind.
const (
	kindCode    = "code"
	kindAccess  = "access"
	kindRefresh = "refresh"
	kindPKCE    = "pkce"
)

// Fallback lifespans, used only when fosite left the session expiry unset,
// so DeleteExpired never sees a zero expires_at and drops a live row.
const (
	// fallbackCodeTTL matches the 10 min authorization code lifespan.
	fallbackCodeTTL = 10 * time.Minute
	// fallbackAccessTTL matches the 1 h access token lifespan.
	fallbackAccessTTL = time.Hour
	// fallbackRefreshTTL matches the 180-day refresh token lifespan.
	fallbackRefreshTTL = 180 * 24 * time.Hour
)

// storedFormKeys are the authorize-request parameters fosite reads back from
// storage. Everything else is dropped so codes, verifiers and refresh tokens
// in a token-request form never reach disk.
var storedFormKeys = []string{"redirect_uri", "code_challenge", "code_challenge_method", "scope", "resource"}

// OAuthClient is an MCP client allowed to use this authorization server. All
// clients are public: they authenticate with PKCE, never with a secret.
type OAuthClient struct {
	ID, Kind, Name string
	RedirectURIs   []string
	// ExpiresAt is when cached metadata must be refreshed; zero when it never expires.
	ExpiresAt time.Time
}

// GetID implements fosite.Client.
func (c *OAuthClient) GetID() string { return c.ID }

// GetHashedSecret implements fosite.Client; public clients have no secret.
func (c *OAuthClient) GetHashedSecret() []byte { return nil }

// GetRedirectURIs implements fosite.Client.
func (c *OAuthClient) GetRedirectURIs() []string { return c.RedirectURIs }

// GetGrantTypes implements fosite.Client.
func (c *OAuthClient) GetGrantTypes() fosite.Arguments {
	return fosite.Arguments{grantAuthorizationCode, grantRefreshToken}
}

// GetResponseTypes implements fosite.Client.
func (c *OAuthClient) GetResponseTypes() fosite.Arguments {
	return fosite.Arguments{responseTypeCode}
}

// GetScopes implements fosite.Client.
func (c *OAuthClient) GetScopes() fosite.Arguments { return fosite.Arguments{mcpScope} }

// IsPublic implements fosite.Client.
func (c *OAuthClient) IsPublic() bool { return true }

// GetAudience implements fosite.Client; audience is not restricted per client.
func (c *OAuthClient) GetAudience() fosite.Arguments { return nil }

// clientMetadata is the JSON stored in oauth_clients.metadata.
type clientMetadata struct {
	Name         string   `json:"name"`
	RedirectURIs []string `json:"redirect_uris"`
}

// SaveClient inserts or replaces a client. CIMD rows also record the fetch time.
func (s *Store) SaveClient(ctx context.Context, c *OAuthClient) error {
	if c == nil || c.ID == "" {
		return errors.New("store: save client: empty id")
	}

	if c.Kind != ClientKindCIMD && c.Kind != ClientKindDCR {
		return fmt.Errorf("store: save client: unknown kind %q", c.Kind)
	}

	meta, err := json.Marshal(clientMetadata{Name: c.Name, RedirectURIs: c.RedirectURIs})
	if err != nil {
		return fmt.Errorf("store: save client: %w", err)
	}

	var fetchedAt sql.NullInt64
	if c.Kind == ClientKindCIMD {
		fetchedAt = sql.NullInt64{Int64: s.now().Unix(), Valid: true}
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO oauth_clients (id, kind, metadata, fetched_at, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			kind = excluded.kind, metadata = excluded.metadata,
			fetched_at = excluded.fetched_at, expires_at = excluded.expires_at`,
		c.ID, c.Kind, string(meta), fetchedAt, nullUnix(c.ExpiresAt)); err != nil {
		return fmt.Errorf("store: save client: %w", err)
	}

	return nil
}

// ClientByID returns a stored client, or ErrNotFound.
func (s *Store) ClientByID(ctx context.Context, id string) (*OAuthClient, error) {
	var (
		kind, meta string
		expiresAt  sql.NullInt64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT kind, metadata, expires_at FROM oauth_clients WHERE id = ?`, id).
		Scan(&kind, &meta, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("store: client by id: %w", err)
	}

	var m clientMetadata
	if err := json.Unmarshal([]byte(meta), &m); err != nil {
		return nil, fmt.Errorf("store: client by id: decode metadata: %w", err)
	}

	c := &OAuthClient{ID: id, Kind: kind, Name: m.Name, RedirectURIs: m.RedirectURIs}
	if expiresAt.Valid {
		c.ExpiresAt = time.Unix(expiresAt.Int64, 0)
	}

	return c, nil
}

// DeleteClient removes a client row; a missing row is not an error.
func (s *Store) DeleteClient(ctx context.Context, id string) error {
	// bearer:disable go_gosec_sql_concat_sqli
	if _, err := s.db.ExecContext(ctx, `DELETE FROM oauth_clients WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: delete client: %w", err)
	}

	return nil
}

// GetClient implements fosite.ClientManager.
func (s *Store) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	c, err := s.ClientByID(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, fosite.ErrNotFound
	}

	if err != nil {
		return nil, err
	}

	return c, nil
}

// ClientAssertionJWTValid implements fosite.ClientManager. No client
// authenticates with a JWT assertion, so there is no JTI to track.
func (s *Store) ClientAssertionJWTValid(context.Context, string) error { return nil }

// SetClientAssertionJWT implements fosite.ClientManager; see ClientAssertionJWTValid.
func (s *Store) SetClientAssertionJWT(context.Context, string, time.Time) error { return nil }

// PurgeUnusedClients deletes clients last used before usedBefore, and clients
// never used at all and created before neverUsedBefore, keeping any client
// that still holds a live token so its refresh tokens keep resolving. The
// shorter never-used window bounds the rows that unauthenticated DCR
// registrations and CIMD lookups can leave behind.
func (s *Store) PurgeUnusedClients(ctx context.Context, usedBefore, neverUsedBefore time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM oauth_clients
		WHERE (coalesce(last_used_at, created_at) < ?
		       OR (last_used_at IS NULL AND created_at < ?))
		  AND NOT EXISTS (
			SELECT 1 FROM oauth_tokens t
			WHERE t.client_id = oauth_clients.id AND t.active = 1 AND t.expires_at >= ?)`,
		usedBefore.Unix(), neverUsedBefore.Unix(), s.now().Unix())
	if err != nil {
		return 0, fmt.Errorf("store: purge clients: %w", err)
	}

	return rowsAffected(res, "purge clients")
}

// storedRequest is the JSON form of a fosite.Requester in oauth_tokens.data.
// The client is stored as oauth_tokens.client_id and resolved on read.
type storedRequest struct {
	ID                string          `json:"id"`
	RequestedAt       time.Time       `json:"requested_at"`
	RequestedScope    []string        `json:"requested_scope"`
	GrantedScope      []string        `json:"granted_scope"`
	RequestedAudience []string        `json:"requested_audience"`
	GrantedAudience   []string        `json:"granted_audience"`
	Form              url.Values      `json:"form"`
	Session           json.RawMessage `json:"session"`
}

func filterForm(form url.Values) url.Values {
	out := url.Values{}

	for _, k := range storedFormKeys {
		if v, ok := form[k]; ok {
			out[k] = v
		}
	}

	return out
}

// tokenKindExpiry names the session expiry fosite sets for a token kind and
// the lifetime used when the session carries none.
type tokenKindExpiry struct {
	tokenType fosite.TokenType
	fallback  time.Duration
}

// tokenExpiry maps a row kind to its tokenKindExpiry.
var tokenExpiry = map[string]tokenKindExpiry{
	kindCode:    {fosite.AuthorizeCode, fallbackCodeTTL},
	kindPKCE:    {fosite.AuthorizeCode, fallbackCodeTTL},
	kindAccess:  {fosite.AccessToken, fallbackAccessTTL},
	kindRefresh: {fosite.RefreshToken, fallbackRefreshTTL},
}

// tokenExpiryAndOwner derives the expiry and owning user of a token from its
// session. A missing session or zero expiry falls back to now plus the kind's
// default lifetime; an empty subject leaves the owner NULL.
func tokenExpiryAndOwner(sess fosite.Session, exp tokenKindExpiry, now time.Time) (time.Time, sql.NullString) {
	var (
		expiresAt time.Time
		userID    sql.NullString
	)

	if sess != nil {
		expiresAt = sess.GetExpiresAt(exp.tokenType)
		if sub := sess.GetSubject(); sub != "" {
			userID = sql.NullString{String: sub, Valid: true}
		}
	}

	if expiresAt.IsZero() {
		expiresAt = now.Add(exp.fallback)
	}

	return expiresAt, userID
}

// createToken stores req under (kind, signature). The session subject becomes
// user_id so DeleteUser finds the row. Issuing a code or refresh token also
// marks the client as used, which PurgeUnusedClients relies on.
func (s *Store) createToken(ctx context.Context, kind, signature string, req fosite.Requester) (err error) {
	client := req.GetClient()
	if client == nil {
		return fmt.Errorf("store: create %s token: request has no client", kind)
	}

	sess := req.GetSession()

	sessJSON, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("store: create %s token: encode session: %w", kind, err)
	}

	data, err := json.Marshal(storedRequest{
		ID:                req.GetID(),
		RequestedAt:       req.GetRequestedAt(),
		RequestedScope:    req.GetRequestedScopes(),
		GrantedScope:      req.GetGrantedScopes(),
		RequestedAudience: req.GetRequestedAudience(),
		GrantedAudience:   req.GetGrantedAudience(),
		Form:              filterForm(req.GetRequestForm()),
		Session:           sessJSON,
	})
	if err != nil {
		return fmt.Errorf("store: create %s token: encode request: %w", kind, err)
	}

	now := s.now()
	expiresAt, userID := tokenExpiryAndOwner(sess, tokenExpiry[kind], now)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: create %s token: begin: %w", kind, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO oauth_tokens (signature, kind, request_id, user_id, client_id, data, expires_at, active)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)`,
		signature, kind, req.GetID(), userID, client.GetID(), data, expiresAt.Unix()); err != nil {
		return fmt.Errorf("store: create %s token: %w", kind, err)
	}

	if kind == kindCode || kind == kindRefresh {
		if _, err := tx.ExecContext(ctx,
			`UPDATE oauth_clients SET last_used_at = ? WHERE id = ?`, now.Unix(), client.GetID()); err != nil {
			return fmt.Errorf("store: create %s token: mark client used: %w", kind, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: create %s token: commit: %w", kind, err)
	}

	return nil
}

// getToken loads the request stored under (kind, signature) and reports
// whether the row is still active. A missing row is fosite.ErrNotFound. The
// session is decoded into session when non-nil, else into a new DefaultSession.
func (s *Store) getToken(ctx context.Context, kind, signature string, session fosite.Session) (fosite.Requester, bool, error) {
	var (
		clientID string
		data     []byte
		active   bool
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT client_id, data, active FROM oauth_tokens WHERE kind = ? AND signature = ?`, kind, signature).
		Scan(&clientID, &data, &active)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, fosite.ErrNotFound
	}

	if err != nil {
		return nil, false, fmt.Errorf("store: get %s token: %w", kind, err)
	}

	var sr storedRequest
	if err := json.Unmarshal(data, &sr); err != nil {
		return nil, false, fmt.Errorf("store: get %s token: decode request: %w", kind, err)
	}

	if session == nil {
		session = new(fosite.DefaultSession)
	}

	if err := json.Unmarshal(sr.Session, session); err != nil {
		return nil, false, fmt.Errorf("store: get %s token: decode session: %w", kind, err)
	}

	client, err := s.GetClient(ctx, clientID)
	if err != nil {
		return nil, false, err
	}

	return &fosite.Request{
		ID:                sr.ID,
		RequestedAt:       sr.RequestedAt,
		Client:            client,
		RequestedScope:    sr.RequestedScope,
		GrantedScope:      sr.GrantedScope,
		RequestedAudience: sr.RequestedAudience,
		GrantedAudience:   sr.GrantedAudience,
		Form:              sr.Form,
		Session:           session,
	}, active, nil
}

func (s *Store) deleteToken(ctx context.Context, kind, signature string) error {
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM oauth_tokens WHERE kind = ? AND signature = ?`, kind, signature); err != nil {
		return fmt.Errorf("store: delete %s token: %w", kind, err)
	}

	return nil
}

// deactivate marks inactive every row of the given kinds issued for requestID.
func (s *Store) deactivate(ctx context.Context, requestID string, kinds ...string) error {
	return deactivate(ctx, s.db, requestID, kinds...)
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func deactivate(ctx context.Context, db execer, requestID string, kinds ...string) error {
	for _, kind := range kinds {
		if _, err := db.ExecContext(ctx,
			`UPDATE oauth_tokens SET active = 0 WHERE request_id = ? AND kind = ?`, requestID, kind); err != nil {
			return fmt.Errorf("store: revoke %s tokens: %w", kind, err)
		}
	}

	return nil
}

// CreateAuthorizeCodeSession implements oauth2.AuthorizeCodeStorage.
func (s *Store) CreateAuthorizeCodeSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.createToken(ctx, kindCode, signature, req)
}

// GetAuthorizeCodeSession implements oauth2.AuthorizeCodeStorage. An
// invalidated code returns the request with fosite.ErrInvalidatedAuthorizeCode,
// which fosite needs to revoke the tokens issued from a reused code.
func (s *Store) GetAuthorizeCodeSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	req, active, err := s.getToken(ctx, kindCode, signature, session)
	if err != nil {
		return nil, err
	}

	if !active {
		return req, fosite.ErrInvalidatedAuthorizeCode
	}

	return req, nil
}

// InvalidateAuthorizeCodeSession implements oauth2.AuthorizeCodeStorage.
// Only an active code is flipped, so of two concurrent exchanges of one code
// exactly one succeeds; the other gets fosite.ErrInvalidatedAuthorizeCode,
// which fosite turns into a rejected token request before storing any token.
func (s *Store) InvalidateAuthorizeCodeSession(ctx context.Context, signature string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE oauth_tokens SET active = 0 WHERE kind = ? AND signature = ? AND active = 1`, kindCode, signature)
	if err != nil {
		return fmt.Errorf("store: invalidate code: %w", err)
	}

	n, err := rowsAffected(res, "invalidate code")
	if err != nil {
		return err
	}

	if n > 0 {
		return nil
	}

	var exists int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count(*) FROM oauth_tokens WHERE kind = ? AND signature = ?`, kindCode, signature).Scan(&exists); err != nil {
		return fmt.Errorf("store: invalidate code: %w", err)
	}

	if exists == 0 {
		return fosite.ErrNotFound
	}

	return fosite.ErrInvalidatedAuthorizeCode
}

// CreateAccessTokenSession implements oauth2.AccessTokenStorage.
func (s *Store) CreateAccessTokenSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.createToken(ctx, kindAccess, signature, req)
}

// GetAccessTokenSession implements oauth2.AccessTokenStorage. A revoked token
// returns the request with fosite.ErrInactiveToken.
func (s *Store) GetAccessTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.getActive(ctx, kindAccess, signature, session)
}

// DeleteAccessTokenSession implements oauth2.AccessTokenStorage.
func (s *Store) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	return s.deleteToken(ctx, kindAccess, signature)
}

// CreateRefreshTokenSession implements oauth2.RefreshTokenStorage. The access
// token signature is not needed: revocation works per request id.
func (s *Store) CreateRefreshTokenSession(ctx context.Context, signature, _ string, req fosite.Requester) error {
	return s.createToken(ctx, kindRefresh, signature, req)
}

// GetRefreshTokenSession implements oauth2.RefreshTokenStorage. A revoked or
// rotated token returns the request with fosite.ErrInactiveToken, which fosite
// treats as reuse and answers by revoking the whole family.
func (s *Store) GetRefreshTokenSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	return s.getActive(ctx, kindRefresh, signature, session)
}

func (s *Store) getActive(ctx context.Context, kind, signature string, session fosite.Session) (fosite.Requester, error) {
	req, active, err := s.getToken(ctx, kind, signature, session)
	if err != nil {
		return nil, err
	}

	if !active {
		return req, fosite.ErrInactiveToken
	}

	return req, nil
}

// DeleteRefreshTokenSession implements oauth2.RefreshTokenStorage.
func (s *Store) DeleteRefreshTokenSession(ctx context.Context, signature string) error {
	return s.deleteToken(ctx, kindRefresh, signature)
}

// RotateRefreshToken implements oauth2.RefreshTokenStorage with no grace
// period: the used refresh token and every token of its family are revoked
// at once, and fosite then stores the new pair under the same request id.
// The used token is claimed first with an active = 1 guard, in the same
// transaction, so of two concurrent refreshes with one token exactly one
// rotates; the other gets fosite.ErrSerializationFailure, which fosite
// reports as concurrent use of the same token.
func (s *Store) RotateRefreshToken(ctx context.Context, requestID, signature string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: rotate refresh token: begin: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	res, err := tx.ExecContext(ctx,
		`UPDATE oauth_tokens SET active = 0 WHERE kind = ? AND signature = ? AND request_id = ? AND active = 1`,
		kindRefresh, signature, requestID)
	if err != nil {
		return fmt.Errorf("store: rotate refresh token: %w", err)
	}

	n, err := rowsAffected(res, "rotate refresh token")
	if err != nil {
		return err
	}

	if n == 0 {
		return fosite.ErrSerializationFailure
	}

	if err := deactivate(ctx, tx, requestID, kindRefresh, kindAccess); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: rotate refresh token: commit: %w", err)
	}

	return nil
}

// RevokeRefreshToken implements oauth2.TokenRevocationStorage: every refresh
// token issued for requestID becomes inactive.
func (s *Store) RevokeRefreshToken(ctx context.Context, requestID string) error {
	return s.deactivate(ctx, requestID, kindRefresh)
}

// RevokeAccessToken implements oauth2.TokenRevocationStorage: every access
// token issued for requestID becomes inactive.
func (s *Store) RevokeAccessToken(ctx context.Context, requestID string) error {
	return s.deactivate(ctx, requestID, kindAccess)
}

// CreatePKCERequestSession implements pkce.PKCERequestStorage.
func (s *Store) CreatePKCERequestSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.createToken(ctx, kindPKCE, signature, req)
}

// GetPKCERequestSession implements pkce.PKCERequestStorage.
func (s *Store) GetPKCERequestSession(ctx context.Context, signature string, session fosite.Session) (fosite.Requester, error) {
	req, _, err := s.getToken(ctx, kindPKCE, signature, session)
	if err != nil {
		return nil, err
	}

	return req, nil
}

// DeletePKCERequestSession implements pkce.PKCERequestStorage.
func (s *Store) DeletePKCERequestSession(ctx context.Context, signature string) error {
	return s.deleteToken(ctx, kindPKCE, signature)
}

// SavePending stores an in-flight authorization (between our /authorize and
// the Trakt callback) under id for ttl.
func (s *Store) SavePending(ctx context.Context, id string, data []byte, ttl time.Duration) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO pending_authorizations (id, data, expires_at) VALUES (?, ?, ?)`,
		id, data, s.now().Add(ttl).Unix()); err != nil {
		return fmt.Errorf("store: save pending: %w", err)
	}

	return nil
}

// TakePending returns and deletes a pending authorization in one statement,
// so a callback replay finds nothing. An expired entry is deleted and
// reported as ErrNotFound.
func (s *Store) TakePending(ctx context.Context, id string) ([]byte, error) {
	var (
		data      []byte
		expiresAt int64
	)

	err := s.db.QueryRowContext(ctx,
		`DELETE FROM pending_authorizations WHERE id = ? RETURNING data, expires_at`, id).
		Scan(&data, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}

	if err != nil {
		return nil, fmt.Errorf("store: take pending: %w", err)
	}

	if expiresAt < s.now().Unix() {
		return nil, ErrNotFound
	}

	return data, nil
}

// DeleteExpired removes fosite rows and pending authorizations that expired
// strictly before now, and returns how many rows it deleted.
func (s *Store) DeleteExpired(ctx context.Context, now time.Time) (int, error) {
	total := 0

	// Two literal statements, not a table name concatenated into SQL, so static
	// analysers see that no input reaches the query text.
	// bearer:disable go_gosec_sql_concat_sqli
	res, err := s.db.ExecContext(ctx, `DELETE FROM oauth_tokens WHERE expires_at < ?`, now.Unix())
	if err != nil {
		return total, fmt.Errorf("store: delete expired oauth_tokens: %w", err)
	}

	n, err := rowsAffected(res, "delete expired oauth_tokens")
	if err != nil {
		return total, err
	}

	total += n

	// bearer:disable go_gosec_sql_concat_sqli
	res, err = s.db.ExecContext(ctx, `DELETE FROM pending_authorizations WHERE expires_at < ?`, now.Unix())
	if err != nil {
		return total, fmt.Errorf("store: delete expired pending_authorizations: %w", err)
	}

	n, err = rowsAffected(res, "delete expired pending_authorizations")
	if err != nil {
		return total, err
	}

	return total + n, nil
}

func rowsAffected(res sql.Result, op string) (int, error) {
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: %s: %w", op, err)
	}

	return int(n), nil
}

func nullUnix(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}

	return sql.NullInt64{Int64: t.Unix(), Valid: true}
}
