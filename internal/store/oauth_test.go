package store

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ory/fosite"
)

const (
	testClientID    = "https://client.example.com/metadata.json"
	testRedirectURI = "https://client.example.com/callback"
	// testRequestID is the fosite request id shared by the token-session tests.
	testRequestID = "req-1"
)

// testNow is a fixed clock so expiry assertions are exact.
var testNow = time.Unix(1_900_000_000, 0)

func setupOAuth(t *testing.T) (*Store, User) {
	t.Helper()

	ctx := context.Background()
	s := openTemp(t)
	s.now = func() time.Time { return testNow }

	u, err := s.UpsertUser(ctx, "1001", "alice")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := s.SaveClient(ctx, &OAuthClient{
		ID: testClientID, Kind: ClientKindCIMD, Name: "Test client",
		RedirectURIs: []string{testRedirectURI},
	}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	return s, u
}

func newRequest(reqID, subject string) *fosite.Request {
	sess := &fosite.DefaultSession{Subject: subject}
	sess.SetExpiresAt(fosite.AuthorizeCode, testNow.Add(10*time.Minute))
	sess.SetExpiresAt(fosite.AccessToken, testNow.Add(time.Hour))
	sess.SetExpiresAt(fosite.RefreshToken, testNow.Add(24*time.Hour))

	r := fosite.NewRequest()
	r.ID = reqID
	r.RequestedAt = testNow
	r.Client = &OAuthClient{ID: testClientID}
	r.Session = sess
	r.RequestedScope = fosite.Arguments{"trakt"}
	r.GrantedScope = fosite.Arguments{"trakt"}
	r.RequestedAudience = fosite.Arguments{"https://mcp.example.com/mcp"}
	r.GrantedAudience = fosite.Arguments{"https://mcp.example.com/mcp"}
	r.Form = url.Values{
		"redirect_uri":          {testRedirectURI},
		"code_challenge":        {"challenge"},
		"code_challenge_method": {"S256"},
		"code":                  {"raw-code-must-not-persist"},
		"code_verifier":         {"verifier-must-not-persist"},
	}

	return r
}

func countRows(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()

	var n int
	if err := s.db.QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}

	return n
}

func TestOAuthClient_FositeProperties(t *testing.T) {
	t.Parallel()

	c := &OAuthClient{ID: "id", RedirectURIs: []string{testRedirectURI}}
	if !c.IsPublic() {
		t.Error("IsPublic = false")
	}

	if c.GetHashedSecret() != nil {
		t.Error("GetHashedSecret not nil")
	}

	if c.GetAudience() != nil {
		t.Error("GetAudience not nil")
	}

	if !c.GetGrantTypes().Has("authorization_code", "refresh_token") || len(c.GetGrantTypes()) != 2 {
		t.Errorf("grant types = %v", c.GetGrantTypes())
	}

	if !c.GetResponseTypes().ExactOne("code") {
		t.Errorf("response types = %v", c.GetResponseTypes())
	}

	if !c.GetScopes().ExactOne("trakt") {
		t.Errorf("scopes = %v", c.GetScopes())
	}

	if got := c.GetRedirectURIs(); len(got) != 1 || got[0] != testRedirectURI {
		t.Errorf("redirect uris = %v", got)
	}
}

func TestClient_SaveGetRoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, _ := setupOAuth(t)
	exp := testNow.Add(time.Hour)

	want := &OAuthClient{ID: "dcr-1", Kind: ClientKindDCR, Name: "DCR", RedirectURIs: []string{"http://127.0.0.1/cb"}, ExpiresAt: exp}
	if err := s.SaveClient(ctx, want); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	got, err := s.ClientByID(ctx, "dcr-1")
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}

	if got.ID != want.ID || got.Kind != want.Kind || got.Name != want.Name ||
		len(got.RedirectURIs) != 1 || got.RedirectURIs[0] != "http://127.0.0.1/cb" || !got.ExpiresAt.Equal(exp) {
		t.Errorf("ClientByID = %+v, want %+v", got, want)
	}

	want.Name = "Renamed"
	if err := s.SaveClient(ctx, want); err != nil {
		t.Fatalf("SaveClient update: %v", err)
	}

	c, err := s.GetClient(ctx, "dcr-1")
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}

	renamed, ok := c.(*OAuthClient)
	if !ok {
		t.Fatalf("GetClient returned %T, want *OAuthClient", c)
	}

	if renamed.Name != "Renamed" {
		t.Errorf("name after update = %q", renamed.Name)
	}

	assertClientLookupFailures(t, s)
}

// assertClientLookupFailures checks the error paths of client lookup and save.
func assertClientLookupFailures(t *testing.T, s *Store) {
	t.Helper()

	ctx := context.Background()

	if _, err := s.ClientByID(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClientByID unknown err = %v, want ErrNotFound", err)
	}

	if _, err := s.GetClient(ctx, "nope"); !errors.Is(err, fosite.ErrNotFound) {
		t.Errorf("GetClient unknown err = %v, want fosite.ErrNotFound", err)
	}

	if err := s.SaveClient(ctx, &OAuthClient{ID: "bad", Kind: "other"}); err == nil {
		t.Error("SaveClient accepted an unknown kind")
	}
}

func TestClientAssertionJWT_NoOp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, _ := setupOAuth(t)
	if err := s.SetClientAssertionJWT(ctx, "jti", testNow); err != nil {
		t.Errorf("SetClientAssertionJWT: %v", err)
	}

	if err := s.ClientAssertionJWTValid(ctx, "jti"); err != nil {
		t.Errorf("ClientAssertionJWTValid: %v", err)
	}
}

func TestAuthorizeCode_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, u := setupOAuth(t)
	if err := s.CreateAuthorizeCodeSession(ctx, "code-sig", newRequest(testRequestID, u.ID)); err != nil {
		t.Fatalf("CreateAuthorizeCodeSession: %v", err)
	}

	got, err := s.GetAuthorizeCodeSession(ctx, "code-sig", &fosite.DefaultSession{})
	if err != nil {
		t.Fatalf("GetAuthorizeCodeSession: %v", err)
	}

	if got.GetID() != testRequestID {
		t.Errorf("id = %q", got.GetID())
	}

	if !got.GetRequestedAt().Equal(testNow) {
		t.Errorf("requested at = %v", got.GetRequestedAt())
	}

	gotClient, ok := got.GetClient().(*OAuthClient)
	if !ok || gotClient.GetID() != testClientID || gotClient.Name != "Test client" {
		t.Errorf("client not resolved from store: %+v", got.GetClient())
	}

	assertCodeSession(t, got.GetSession(), u.ID)

	if !got.GetGrantedScopes().ExactOne("trakt") || !got.GetRequestedScopes().ExactOne("trakt") {
		t.Errorf("scopes = %v / %v", got.GetRequestedScopes(), got.GetGrantedScopes())
	}

	if !got.GetGrantedAudience().ExactOne("https://mcp.example.com/mcp") ||
		!got.GetRequestedAudience().ExactOne("https://mcp.example.com/mcp") {
		t.Errorf("audience = %v / %v", got.GetRequestedAudience(), got.GetGrantedAudience())
	}

	assertNoSecretsPersisted(t, s, got)
}

// assertCodeSession checks the subject and authorize-code expiry restored from the store.
func assertCodeSession(t *testing.T, sess fosite.Session, wantSubject string) {
	t.Helper()

	if sess.GetSubject() != wantSubject {
		t.Errorf("subject = %q", sess.GetSubject())
	}

	if !sess.GetExpiresAt(fosite.AuthorizeCode).Equal(testNow.Add(10 * time.Minute)) {
		t.Errorf("code expiry = %v", sess.GetExpiresAt(fosite.AuthorizeCode))
	}
}

// assertNoSecretsPersisted checks that the stored authorize-code request keeps
// the fosite form keys, drops raw secrets, and that unknown codes are not found.
func assertNoSecretsPersisted(t *testing.T, s *Store, got fosite.Requester) {
	t.Helper()

	ctx := context.Background()

	form := got.GetRequestForm()
	if form.Get("redirect_uri") != testRedirectURI || form.Get("code_challenge") != "challenge" ||
		form.Get("code_challenge_method") != "S256" {
		t.Errorf("form lost fosite keys: %v", form)
	}

	if form.Has("code") || form.Has("code_verifier") {
		t.Errorf("form persisted secrets: %v", form)
	}

	if n := countRows(t, s, `SELECT count(*) FROM oauth_tokens WHERE CAST(data AS TEXT) LIKE '%must-not-persist%'`); n != 0 {
		t.Errorf("raw secret found in %d stored rows", n)
	}

	if _, err := s.GetAuthorizeCodeSession(ctx, "unknown", nil); !errors.Is(err, fosite.ErrNotFound) {
		t.Errorf("unknown code err = %v, want fosite.ErrNotFound", err)
	}
}

func TestAuthorizeCode_InvalidateThenGetReturnsErrInvalidatedAuthorizeCode(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, u := setupOAuth(t)
	if err := s.CreateAuthorizeCodeSession(ctx, "code-sig", newRequest(testRequestID, u.ID)); err != nil {
		t.Fatalf("CreateAuthorizeCodeSession: %v", err)
	}

	if err := s.InvalidateAuthorizeCodeSession(ctx, "code-sig"); err != nil {
		t.Fatalf("InvalidateAuthorizeCodeSession: %v", err)
	}

	got, err := s.GetAuthorizeCodeSession(ctx, "code-sig", nil)
	if !errors.Is(err, fosite.ErrInvalidatedAuthorizeCode) {
		t.Fatalf("err = %v, want ErrInvalidatedAuthorizeCode", err)
	}

	if got == nil || got.GetID() != testRequestID {
		t.Fatalf("request must come with ErrInvalidatedAuthorizeCode, got %v", got)
	}

	if err := s.InvalidateAuthorizeCodeSession(ctx, "unknown"); !errors.Is(err, fosite.ErrNotFound) {
		t.Errorf("invalidate unknown err = %v, want fosite.ErrNotFound", err)
	}
}

func TestAccessToken_CreateGetDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, u := setupOAuth(t)
	if err := s.CreateAccessTokenSession(ctx, "at-sig", newRequest(testRequestID, u.ID)); err != nil {
		t.Fatalf("CreateAccessTokenSession: %v", err)
	}
	// fosite's revocation handler passes a nil session.
	got, err := s.GetAccessTokenSession(ctx, "at-sig", nil)
	if err != nil {
		t.Fatalf("GetAccessTokenSession: %v", err)
	}

	if got.GetSession().GetSubject() != u.ID {
		t.Errorf("subject = %q", got.GetSession().GetSubject())
	}

	if n := countRows(t, s, `SELECT count(*) FROM oauth_tokens WHERE kind = 'access' AND expires_at = ?`,
		testNow.Add(time.Hour).Unix()); n != 1 {
		t.Errorf("access row with session expiry = %d, want 1", n)
	}

	if err := s.DeleteAccessTokenSession(ctx, "at-sig"); err != nil {
		t.Fatalf("DeleteAccessTokenSession: %v", err)
	}

	if _, err := s.GetAccessTokenSession(ctx, "at-sig", nil); !errors.Is(err, fosite.ErrNotFound) {
		t.Errorf("after delete err = %v, want fosite.ErrNotFound", err)
	}
}

func TestTokens_UserIDFromSubject(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, u := setupOAuth(t)

	req := newRequest(testRequestID, u.ID)
	if err := s.CreateAuthorizeCodeSession(ctx, "sig", req); err != nil {
		t.Fatal(err)
	}

	if err := s.CreatePKCERequestSession(ctx, "sig", req); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateAccessTokenSession(ctx, "at", req); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateRefreshTokenSession(ctx, "rt", "at", req); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM oauth_tokens WHERE user_id = ?`, u.ID); n != 4 {
		t.Errorf("rows bound to user = %d, want 4", n)
	}

	if err := s.CreateAccessTokenSession(ctx, "at-ghost", newRequest("req-2", "no-such-user")); err == nil {
		t.Error("token accepted for an unknown subject")
	}
}

func TestRefresh_RevokeFamily(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, u := setupOAuth(t)
	for _, id := range []string{testRequestID, "req-2"} {
		req := newRequest(id, u.ID)
		if err := s.CreateAccessTokenSession(ctx, "at-"+id, req); err != nil {
			t.Fatal(err)
		}

		if err := s.CreateRefreshTokenSession(ctx, "rt-"+id, "at-"+id, req); err != nil {
			t.Fatal(err)
		}
	}
	// A rotated family has several refresh rows under one request id.
	if err := s.CreateRefreshTokenSession(ctx, "rt-req-1-b", "at-req-1", newRequest(testRequestID, u.ID)); err != nil {
		t.Fatal(err)
	}

	if err := s.RevokeRefreshToken(ctx, testRequestID); err != nil {
		t.Fatalf("RevokeRefreshToken: %v", err)
	}

	for _, sig := range []string{"rt-req-1", "rt-req-1-b"} {
		got, err := s.GetRefreshTokenSession(ctx, sig, nil)
		if !errors.Is(err, fosite.ErrInactiveToken) {
			t.Errorf("%s err = %v, want ErrInactiveToken", sig, err)
		}

		if got == nil || got.GetID() != testRequestID {
			t.Errorf("%s: request must come with ErrInactiveToken, got %v", sig, got)
		}
	}

	if _, err := s.GetRefreshTokenSession(ctx, "rt-req-2", nil); err != nil {
		t.Errorf("other family refresh revoked: %v", err)
	}

	assertAccessRevokeAndDelete(t, s)
}

// assertAccessRevokeAndDelete checks that revoking one request id leaves other
// families usable, and that deleting a refresh session makes it unreadable.
func assertAccessRevokeAndDelete(t *testing.T, s *Store) {
	t.Helper()

	ctx := context.Background()

	if err := s.RevokeAccessToken(ctx, testRequestID); err != nil {
		t.Fatalf("RevokeAccessToken: %v", err)
	}

	if _, err := s.GetAccessTokenSession(ctx, "at-req-1", nil); !errors.Is(err, fosite.ErrInactiveToken) {
		t.Errorf("access err = %v, want ErrInactiveToken", err)
	}

	if _, err := s.GetAccessTokenSession(ctx, "at-req-2", nil); err != nil {
		t.Errorf("other family access revoked: %v", err)
	}

	if err := s.RevokeRefreshToken(ctx, "unknown"); err != nil {
		t.Errorf("revoke unknown request: %v", err)
	}

	if err := s.DeleteRefreshTokenSession(ctx, "rt-req-2"); err != nil {
		t.Fatalf("DeleteRefreshTokenSession: %v", err)
	}

	if _, err := s.GetRefreshTokenSession(ctx, "rt-req-2", nil); !errors.Is(err, fosite.ErrNotFound) {
		t.Errorf("after delete err = %v, want fosite.ErrNotFound", err)
	}
}

func TestRefresh_RotateRevokesFamilyImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, u := setupOAuth(t)

	req := newRequest(testRequestID, u.ID)
	if err := s.CreateAccessTokenSession(ctx, "at-1", req); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateRefreshTokenSession(ctx, "rt-1", "at-1", req); err != nil {
		t.Fatal(err)
	}

	if err := s.RotateRefreshToken(ctx, testRequestID, "rt-1"); err != nil {
		t.Fatalf("RotateRefreshToken: %v", err)
	}

	if _, err := s.GetRefreshTokenSession(ctx, "rt-1", nil); !errors.Is(err, fosite.ErrInactiveToken) {
		t.Errorf("old refresh err = %v, want ErrInactiveToken", err)
	}

	if _, err := s.GetAccessTokenSession(ctx, "at-1", nil); !errors.Is(err, fosite.ErrInactiveToken) {
		t.Errorf("old access err = %v, want ErrInactiveToken", err)
	}
	// fosite stores the new pair under the same request id right after rotating.
	if err := s.CreateRefreshTokenSession(ctx, "rt-2", "at-2", req); err != nil {
		t.Fatal(err)
	}

	if _, err := s.GetRefreshTokenSession(ctx, "rt-2", nil); err != nil {
		t.Errorf("new refresh: %v", err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM oauth_tokens WHERE kind = 'refresh' AND expires_at = ?`,
		testNow.Add(24*time.Hour).Unix()); n != 2 {
		t.Errorf("refresh rows with session expiry = %d, want 2", n)
	}
}

func TestRefresh_MarksClientUsed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, u := setupOAuth(t)
	if err := s.CreateRefreshTokenSession(ctx, "rt", "at", newRequest(testRequestID, u.ID)); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM oauth_clients WHERE id = ? AND last_used_at = ?`,
		testClientID, testNow.Unix()); n != 1 {
		t.Errorf("client last_used_at not set")
	}
}

func TestPKCE_CreateGetDelete(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, u := setupOAuth(t)
	req := newRequest(testRequestID, u.ID)
	// fosite keys the code and PKCE rows by the same signature.
	if err := s.CreateAuthorizeCodeSession(ctx, "sig", req); err != nil {
		t.Fatal(err)
	}

	if err := s.CreatePKCERequestSession(ctx, "sig", req); err != nil {
		t.Fatalf("CreatePKCERequestSession: %v", err)
	}

	got, err := s.GetPKCERequestSession(ctx, "sig", nil)
	if err != nil {
		t.Fatalf("GetPKCERequestSession: %v", err)
	}

	if got.GetRequestForm().Get("code_challenge") != "challenge" {
		t.Errorf("challenge = %q", got.GetRequestForm().Get("code_challenge"))
	}

	if err := s.DeletePKCERequestSession(ctx, "sig"); err != nil {
		t.Fatalf("DeletePKCERequestSession: %v", err)
	}

	if _, err := s.GetPKCERequestSession(ctx, "sig", nil); !errors.Is(err, fosite.ErrNotFound) {
		t.Errorf("after delete err = %v, want fosite.ErrNotFound", err)
	}

	if _, err := s.GetAuthorizeCodeSession(ctx, "sig", nil); err != nil {
		t.Errorf("code row lost with PKCE delete: %v", err)
	}
}

func TestPending_TakeOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, _ := setupOAuth(t)
	if err := s.SavePending(ctx, "p1", []byte("state"), 10*time.Minute); err != nil {
		t.Fatalf("SavePending: %v", err)
	}

	got, err := s.TakePending(ctx, "p1")
	if err != nil || string(got) != "state" {
		t.Fatalf("TakePending = %q, %v", got, err)
	}

	if _, err := s.TakePending(ctx, "p1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("second TakePending err = %v, want ErrNotFound", err)
	}

	if _, err := s.TakePending(ctx, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown TakePending err = %v, want ErrNotFound", err)
	}
}

func TestPending_Expired(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, _ := setupOAuth(t)
	if err := s.SavePending(ctx, "p1", []byte("state"), 10*time.Minute); err != nil {
		t.Fatalf("SavePending: %v", err)
	}

	s.now = func() time.Time { return testNow.Add(10*time.Minute + time.Second) }
	if _, err := s.TakePending(ctx, "p1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expired TakePending err = %v, want ErrNotFound", err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM pending_authorizations`); n != 0 {
		t.Errorf("expired pending row kept: %d", n)
	}
}

func TestDeleteExpired(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, u := setupOAuth(t)

	req := newRequest(testRequestID, u.ID)
	if err := s.CreateAuthorizeCodeSession(ctx, "code", req); err != nil { // expires +10 min
		t.Fatal(err)
	}

	if err := s.CreateAccessTokenSession(ctx, "at", req); err != nil { // expires +1 h
		t.Fatal(err)
	}

	if err := s.CreateRefreshTokenSession(ctx, "rt", "at", req); err != nil { // expires +24 h
		t.Fatal(err)
	}

	if err := s.SavePending(ctx, "p-old", []byte("x"), time.Minute); err != nil {
		t.Fatal(err)
	}

	if err := s.SavePending(ctx, "p-new", []byte("x"), 2*time.Hour); err != nil {
		t.Fatal(err)
	}

	n, err := s.DeleteExpired(ctx, testNow.Add(time.Hour+time.Second))
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}

	if n != 3 {
		t.Errorf("deleted = %d, want 3 (code, access, p-old)", n)
	}

	if _, err := s.GetRefreshTokenSession(ctx, "rt", nil); err != nil {
		t.Errorf("live refresh deleted: %v", err)
	}

	if _, err := s.TakePending(ctx, "p-new"); err != nil {
		t.Errorf("live pending deleted: %v", err)
	}
}

func TestPurgeUnusedClients(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, u := setupOAuth(t)
	for _, id := range []string{"stale", "with-token", "fresh"} {
		if err := s.SaveClient(ctx, &OAuthClient{ID: id, Kind: ClientKindDCR}); err != nil {
			t.Fatal(err)
		}
	}

	old := testNow.Add(-48 * time.Hour).Unix()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE oauth_clients SET created_at = ?, last_used_at = NULL WHERE id IN ('stale', 'with-token', ?)`,
		old, testClientID); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE oauth_clients SET last_used_at = ? WHERE id = 'fresh'`, testNow.Unix()); err != nil {
		t.Fatal(err)
	}

	req := newRequest(testRequestID, u.ID)

	req.Client = &OAuthClient{ID: "with-token"}
	if err := s.CreateAccessTokenSession(ctx, "at", req); err != nil {
		t.Fatal(err)
	}

	n, err := s.PurgeUnusedClients(ctx, testNow.Add(-24*time.Hour), testNow.Add(-72*time.Hour))
	if err != nil {
		t.Fatalf("PurgeUnusedClients: %v", err)
	}

	if n != 2 {
		t.Errorf("purged = %d, want 2 (stale, test client)", n)
	}

	for _, id := range []string{"with-token", "fresh"} {
		if _, err := s.ClientByID(ctx, id); err != nil {
			t.Errorf("client %s purged: %v", id, err)
		}
	}

	if _, err := s.ClientByID(ctx, "stale"); !errors.Is(err, ErrNotFound) {
		t.Errorf("stale client kept: %v", err)
	}
}

func TestPurgeUnusedClients_NeverUsed(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, _ := setupOAuth(t)
	// The fixture's own client has an unrelated created_at; drop it.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM oauth_clients`); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"never-used-old", "never-used-new", "used"} {
		if err := s.SaveClient(ctx, &OAuthClient{ID: id, Kind: ClientKindDCR}); err != nil {
			t.Fatal(err)
		}
	}
	// Created 25 h ago: past a 24 h never-used window, far inside a 180-day one.
	created := testNow.Add(-25 * time.Hour).Unix()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE oauth_clients SET created_at = ?, last_used_at = NULL WHERE id IN ('never-used-old', 'used')`,
		created); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE oauth_clients SET last_used_at = ? WHERE id = 'used'`, created+1); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE oauth_clients SET created_at = ?, last_used_at = NULL WHERE id = 'never-used-new'`,
		testNow.Add(-23*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}

	n, err := s.PurgeUnusedClients(ctx, testNow.Add(-180*24*time.Hour), testNow.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeUnusedClients: %v", err)
	}

	if n != 1 {
		t.Errorf("purged = %d, want 1 (never-used-old)", n)
	}

	if _, err := s.ClientByID(ctx, "never-used-old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("never-used client older than 24 h kept: %v", err)
	}

	for _, id := range []string{"never-used-new", "used"} {
		if _, err := s.ClientByID(ctx, id); err != nil {
			t.Errorf("client %s purged: %v", id, err)
		}
	}
}

func TestDeleteClient(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, _ := setupOAuth(t)
	if err := s.DeleteClient(ctx, testClientID); err != nil {
		t.Fatalf("DeleteClient: %v", err)
	}

	if _, err := s.ClientByID(ctx, testClientID); !errors.Is(err, ErrNotFound) {
		t.Errorf("client kept: %v", err)
	}

	if err := s.DeleteClient(ctx, "missing"); err != nil {
		t.Errorf("DeleteClient(missing) = %v, want nil", err)
	}
}

func TestDeleteUser_RemovesTokens(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, u := setupOAuth(t)

	keep, err := s.UpsertUser(ctx, "1002", "bob")
	if err != nil {
		t.Fatal(err)
	}

	for _, owner := range []User{u, keep} {
		req := newRequest("req-"+owner.ID, owner.ID)
		if err := s.CreateAuthorizeCodeSession(ctx, "code-"+owner.ID, req); err != nil {
			t.Fatal(err)
		}

		if err := s.CreatePKCERequestSession(ctx, "code-"+owner.ID, req); err != nil {
			t.Fatal(err)
		}

		if err := s.CreateAccessTokenSession(ctx, "at-"+owner.ID, req); err != nil {
			t.Fatal(err)
		}

		if err := s.CreateRefreshTokenSession(ctx, "rt-"+owner.ID, "at-"+owner.ID, req); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM oauth_tokens WHERE user_id = ?`, u.ID); n != 0 {
		t.Errorf("deleted user's rows left = %d", n)
	}

	if _, err := s.GetRefreshTokenSession(ctx, "rt-"+u.ID, nil); !errors.Is(err, fosite.ErrNotFound) {
		t.Errorf("deleted user's refresh err = %v, want fosite.ErrNotFound", err)
	}

	if n := countRows(t, s, `SELECT count(*) FROM oauth_tokens WHERE user_id = ?`, keep.ID); n != 4 {
		t.Errorf("other user's rows = %d, want 4", n)
	}
}

func TestOpen_SecureDeleteOn(t *testing.T) {
	t.Parallel()

	s := openTemp(t)
	if n := countRows(t, s, `PRAGMA secure_delete`); n != 1 {
		t.Errorf("secure_delete = %d, want 1", n)
	}
}

func TestOpen_SecondStoreCanWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	s1 := openAt(t, path, testKeyA)
	s2 := openAt(t, path, testKeyA)

	u, err := s1.UpsertUser(ctx, "1001", "alice")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := s1.SaveTraktToken(ctx, u.ID, TraktToken{RefreshToken: "r", AccessToken: "a"}); err != nil {
		t.Fatalf("SaveTraktToken: %v", err)
	}
	// Rekey and DeleteUser open write transactions from the second handle.
	if _, err := s2.Rekey(ctx); err != nil {
		t.Fatalf("Rekey on second store: %v", err)
	}

	if err := s2.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser on second store: %v", err)
	}

	if _, err := s1.UserBySlugOrTraktID(ctx, "1001"); !errors.Is(err, ErrNotFound) {
		t.Errorf("first store still sees user: %v", err)
	}
}

func TestInvalidateAuthorizeCode_Twice_SecondFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s, u := setupOAuth(t)
	if err := s.CreateAuthorizeCodeSession(ctx, "code-sig", newRequest(testRequestID, u.ID)); err != nil {
		t.Fatal(err)
	}

	if err := s.InvalidateAuthorizeCodeSession(ctx, "code-sig"); err != nil {
		t.Fatalf("first invalidate: %v", err)
	}

	if err := s.InvalidateAuthorizeCodeSession(ctx, "code-sig"); !errors.Is(err, fosite.ErrInvalidatedAuthorizeCode) {
		t.Errorf("second invalidate err = %v, want ErrInvalidatedAuthorizeCode", err)
	}
}

func TestRotateRefreshToken_Twice_SecondFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, u := setupOAuth(t)

	req := newRequest(testRequestID, u.ID)
	if err := s.CreateAccessTokenSession(ctx, "at-1", req); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateRefreshTokenSession(ctx, "rt-1", "at-1", req); err != nil {
		t.Fatal(err)
	}

	if err := s.RotateRefreshToken(ctx, testRequestID, "rt-1"); err != nil {
		t.Fatalf("first rotate: %v", err)
	}
	// A new pair exists, so only the signature guard can reject the replay.
	if err := s.CreateRefreshTokenSession(ctx, "rt-2", "at-2", req); err != nil {
		t.Fatal(err)
	}

	if err := s.RotateRefreshToken(ctx, testRequestID, "rt-1"); !errors.Is(err, fosite.ErrSerializationFailure) {
		t.Errorf("second rotate err = %v, want ErrSerializationFailure", err)
	}

	if _, err := s.GetRefreshTokenSession(ctx, "rt-2", nil); err != nil {
		t.Errorf("failed rotate touched the new refresh token: %v", err)
	}

	if err := s.RotateRefreshToken(ctx, testRequestID, "unknown"); !errors.Is(err, fosite.ErrSerializationFailure) {
		t.Errorf("unknown signature err = %v, want ErrSerializationFailure", err)
	}
}

func TestRotateRefreshToken_ConcurrentExactlyOneWins(t *testing.T) {
	t.Parallel()

	// concurrentRotations is enough goroutines to make an unguarded race likely.
	const concurrentRotations = 16

	ctx := context.Background()
	s, u := setupOAuth(t)

	req := newRequest(testRequestID, u.ID)
	if err := s.CreateRefreshTokenSession(ctx, "rt-1", "at-1", req); err != nil {
		t.Fatal(err)
	}

	errs := make([]error, concurrentRotations)

	var wg sync.WaitGroup
	for i := range errs {
		wg.Go(func() {
			errs[i] = s.RotateRefreshToken(ctx, testRequestID, "rt-1")
		})
	}

	wg.Wait()

	wins := 0

	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case !errors.Is(err, fosite.ErrSerializationFailure):
			t.Errorf("unexpected err: %v", err)
		}
	}

	if wins != 1 {
		t.Errorf("successful rotations = %d, want 1", wins)
	}
}
