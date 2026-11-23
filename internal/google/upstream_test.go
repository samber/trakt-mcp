package google

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/samber/google-contacts-mcp/internal/contacts"
	"github.com/samber/google-contacts-mcp/internal/store"
)

func TestAuthCodeURL(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	verifier := oauth2.GenerateVerifier()
	raw := env.up.AuthCodeURL("state-1", verifier)

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	sum := sha256.Sum256([]byte(verifier))
	want := map[string]string{
		"client_id":             "client-id",
		"redirect_uri":          "https://mcp.example.com/oauth/google/callback",
		"response_type":         "code",
		"scope":                 "openid email https://www.googleapis.com/auth/contacts",
		"state":                 "state-1",
		"access_type":           "offline",
		"prompt":                "consent",
		"code_challenge_method": "S256",
		"code_challenge":        base64.RawURLEncoding.EncodeToString(sum[:]),
	}

	q := u.Query()
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}

	if !strings.HasPrefix(raw, env.fake.srv.URL+"/auth?") {
		t.Errorf("url = %q", raw)
	}
}

func TestExchange_SendsVerifier(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.fake.newRefresh = "refresh-new"

	tok, err := env.up.Exchange(context.Background(), "code-1", "verifier-1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if tok.AccessToken != testAccessTokenNew || tok.RefreshToken != "refresh-new" {
		t.Errorf("token = %+v", tok)
	}

	_, body, _, _ := env.fake.last(routeToken)

	form, err := url.ParseQuery(string(body))
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}

	if form.Get("code") != "code-1" || form.Get("code_verifier") != "verifier-1" || form.Get("grant_type") != "authorization_code" {
		t.Errorf("token form = %v", form)
	}

	env.fake.tokenError = oauthErrInvalidGrant

	_, err = env.up.Exchange(context.Background(), "code-2", "verifier-2")
	if err == nil || strings.Contains(err.Error(), "secret detail") {
		t.Errorf("err = %v, want an error without the response body", err)
	}
}

func TestTokenSource_RefreshPersists(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.expireToken(t)

	ctx := context.Background()
	if _, err := env.people(t).Get(ctx, "people/c1"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if n := env.fake.tokenHits.Load(); n != 1 {
		t.Errorf("token hits = %d, want 1", n)
	}

	if _, _, auth, _ := env.fake.last(routeGet); auth != "Bearer access-new" {
		t.Errorf("Authorization = %q", auth)
	}

	_, body, _, _ := env.fake.last(routeToken)
	if form, _ := url.ParseQuery(string(body)); form.Get("refresh_token") != testRefreshToken || form.Get("grant_type") != "refresh_token" {
		t.Errorf("token form = %s", body)
	}

	got, err := env.st.GoogleToken(ctx, env.userID)
	if err != nil {
		t.Fatalf("GoogleToken: %v", err)
	}
	// Google omitted refresh_token: the stored one is kept.
	if got.AccessToken != testAccessTokenNew || got.RefreshToken != testRefreshToken || !got.Expiry.After(time.Now()) {
		t.Errorf("stored token = access %q refresh %q expiry %v", got.AccessToken, got.RefreshToken, got.Expiry)
	}

	// A rotated refresh token replaces the stored one.
	env.expireToken(t)

	env.fake.newRefresh = "refresh-2"
	if _, err := env.people(t).Get(ctx, "people/c1"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got, err := env.st.GoogleToken(ctx, env.userID); err != nil || got.RefreshToken != "refresh-2" {
		t.Errorf("stored refresh = %q, err %v; want refresh-2", got.RefreshToken, err)
	}
}

func TestTokenSource_ConcurrentRefreshOnce(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.expireToken(t)
	// The delay keeps the first refresh in flight while the others arrive.
	env.fake.tokenDelay = 100 * time.Millisecond

	const workers = 20

	var wg sync.WaitGroup

	errs := make(chan error, workers)
	for range workers {
		wg.Go(func() {
			p, err := env.up.People(context.Background(), env.userID)
			if err == nil {
				_, err = p.Get(context.Background(), "people/c1")
			}

			errs <- err
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Errorf("Get: %v", err)
		}
	}

	if n := env.fake.tokenHits.Load(); n != 1 {
		t.Errorf("token hits = %d, want 1", n)
	}
}

func TestTokenSource_InvalidGrantDeletesUser(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.expireToken(t)
	env.fake.tokenError = oauthErrInvalidGrant

	var logs bytes.Buffer

	env.up.log = slog.New(slog.NewJSONHandler(&logs, nil))
	ctx := context.Background()

	_, err := env.people(t).Get(ctx, "people/c1")
	if !errors.Is(err, contacts.ErrUpstreamAuth) {
		t.Fatalf("err = %v, want ErrUpstreamAuth", err)
	}

	var line struct {
		Level  string `json:"level"`
		Msg    string `json:"msg"`
		UserID string `json:"user_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(logs.Bytes(), &line); err != nil {
		t.Fatalf("log = %q: %v", logs.String(), err)
	}

	if line.Level != "INFO" || line.Msg != "user disconnected" || line.UserID != env.userID || line.Reason != reasonGoogleRevoked {
		t.Errorf("log = %s, want an info \"user disconnected\" line with user_id and reason %s", logs.String(), reasonGoogleRevoked)
	}

	if strings.Contains(logs.String(), "@") {
		t.Errorf("log names an e-mail: %s", logs.String())
	}

	if strings.Contains(err.Error(), "secret detail") {
		t.Errorf("error leaks the token response body: %v", err)
	}

	if n := env.fake.count(routeGet); n != 0 {
		t.Errorf("people calls = %d, want 0", n)
	}

	if _, err := env.st.UserBySubOrEmail(ctx, "sub-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UserBySubOrEmail err = %v, want ErrNotFound", err)
	}

	if _, err := env.up.People(ctx, env.userID); !errors.Is(err, contacts.ErrUpstreamAuth) {
		t.Errorf("People after delete err = %v, want ErrUpstreamAuth", err)
	}
}

func TestTokenSource_OtherRefreshErrorKeepsUser(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.expireToken(t)
	env.fake.tokenError = "server_error"
	ctx := context.Background()

	_, err := env.people(t).Get(ctx, "people/c1")
	if err == nil || errors.Is(err, contacts.ErrUpstreamAuth) {
		t.Fatalf("err = %v, want a plain error", err)
	}

	if strings.Contains(err.Error(), "secret detail") {
		t.Errorf("error leaks the token response body: %v", err)
	}

	if _, err := env.st.UserBySubOrEmail(ctx, "sub-1"); err != nil {
		t.Errorf("UserBySubOrEmail: %v, want the user kept", err)
	}
}

func TestRevoke(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)

	ctx := context.Background()
	if err := env.up.Revoke(ctx, testRefreshToken); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	env.fake.mu.Lock()
	forms := append([]string(nil), env.fake.revokedForm...)
	env.fake.mu.Unlock()

	if len(forms) != 1 {
		t.Fatalf("revoke calls = %d, want 1", len(forms))
	}

	if form, _ := url.ParseQuery(forms[0]); form.Get("token") != testRefreshToken {
		t.Errorf("revoke form = %q", forms[0])
	}

	env.fake.revokeCode = 400

	err := env.up.Revoke(ctx, testRefreshToken)
	if err == nil || strings.Contains(err.Error(), testRefreshToken) {
		t.Errorf("err = %v, want an error without the token", err)
	}
}

// reconsent stores a fresh grant, as the OAuth callback does when the user
// connects again while an old refresh is in flight.
func (e *testEnv) reconsent(t *testing.T) {
	t.Helper()

	if err := e.st.SaveGoogleToken(context.Background(), e.userID, store.GoogleToken{
		RefreshToken: "refresh-reconsent", AccessToken: "access-reconsent", Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Errorf("SaveGoogleToken: %v", err)
	}
}

func TestTokenSource_RefreshDoesNotOverwriteNewerGrant(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.expireToken(t)
	env.fake.onToken = func() { env.reconsent(t) }

	ctx := context.Background()
	if _, err := env.people(t).Get(ctx, "people/c1"); err != nil {
		t.Fatalf("Get: %v", err)
	}

	got, err := env.st.GoogleToken(ctx, env.userID)
	if err != nil {
		t.Fatalf("GoogleToken: %v", err)
	}

	if got.RefreshToken != "refresh-reconsent" || got.AccessToken != "access-reconsent" {
		t.Errorf("stored = refresh %q access %q; want the re-consent grant kept", got.RefreshToken, got.AccessToken)
	}

	if _, _, auth, _ := env.fake.last(routeGet); auth != "Bearer access-reconsent" {
		t.Errorf("Authorization = %q, want the newer grant's token", auth)
	}
}

func TestTokenSource_InvalidGrantKeepsUserWhoReconsented(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.expireToken(t)
	env.fake.tokenError = oauthErrInvalidGrant
	env.fake.onToken = func() { env.reconsent(t) }

	ctx := context.Background()
	if _, err := env.people(t).Get(ctx, "people/c1"); err != nil {
		t.Fatalf("Get: %v, want the newer grant used", err)
	}

	if _, err := env.st.UserBySubOrEmail(ctx, "sub-1"); err != nil {
		t.Fatalf("user deleted despite re-consent: %v", err)
	}

	if _, _, auth, _ := env.fake.last(routeGet); auth != "Bearer access-reconsent" {
		t.Errorf("Authorization = %q, want the newer grant's token", auth)
	}
}

// unauthorized makes the fake answer 401 to the first n requests on rt.
func (e *testEnv) unauthorized(rt string, n int32) {
	var served atomic.Int32

	e.fake.override = func(w http.ResponseWriter, _ *http.Request, got string) bool {
		if got != rt || served.Add(1) > n {
			return false
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		e.fake.write(w, `{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`)

		return true
	}
}

func TestPeople401_RefreshInvalidGrantDeletesUser(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	// The stored access token is still valid: only Google knows it was revoked.
	env.unauthorized(routeGet, 1)
	env.fake.tokenError = oauthErrInvalidGrant
	ctx := context.Background()

	_, err := env.people(t).Get(ctx, "people/c1")
	if !errors.Is(err, contacts.ErrUpstreamAuth) {
		t.Fatalf("err = %v, want ErrUpstreamAuth", err)
	}

	if n := env.fake.tokenHits.Load(); n != 1 {
		t.Errorf("token hits = %d, want 1", n)
	}

	if n := env.fake.count(routeGet); n != 1 {
		t.Errorf("people calls = %d, want 1", n)
	}

	if _, err := env.st.UserBySubOrEmail(ctx, "sub-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("UserBySubOrEmail err = %v, want ErrNotFound", err)
	}
}

func TestPeople401_RefreshOKRetriesOnce(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.unauthorized(routeGet, 1)

	ctx := context.Background()

	got, err := env.people(t).Get(ctx, "people/c1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.ResourceName != "people/c1" {
		t.Errorf("contact = %+v", got)
	}

	if n := env.fake.tokenHits.Load(); n != 1 {
		t.Errorf("token hits = %d, want 1", n)
	}

	if n := env.fake.count(routeGet); n != 2 {
		t.Errorf("people calls = %d, want 2", n)
	}

	if _, _, auth, _ := env.fake.last(routeGet); auth != "Bearer access-new" {
		t.Errorf("retry Authorization = %q, want the refreshed token", auth)
	}

	if tok, err := env.st.GoogleToken(ctx, env.userID); err != nil || tok.AccessToken != testAccessTokenNew {
		t.Errorf("stored access = %q, err %v; want access-new", tok.AccessToken, err)
	}
}

func TestPeople401_Twice_NoDelete(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.unauthorized(routeGet, 2)

	ctx := context.Background()

	_, err := env.people(t).Get(ctx, "people/c1")
	if !errors.Is(err, contacts.ErrUpstreamAuth) {
		t.Fatalf("err = %v, want ErrUpstreamAuth", err)
	}

	if n := env.fake.tokenHits.Load(); n != 1 {
		t.Errorf("token hits = %d, want 1", n)
	}

	if n := env.fake.count(routeGet); n != 2 {
		t.Errorf("people calls = %d, want 2", n)
	}

	if _, err := env.st.UserBySubOrEmail(ctx, "sub-1"); err != nil {
		t.Errorf("UserBySubOrEmail: %v, want the user kept", err)
	}
}

func TestPeople401_WriteRetriedOnce(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.unauthorized(routeCreate, 1)

	ctx := context.Background()

	got, err := env.people(t).Create(ctx, contacts.Contact{GivenName: "Ada"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got.ResourceName != "people/new" {
		t.Errorf("contact = %+v", got)
	}

	if n := env.fake.count(routeCreate); n != 2 {
		t.Errorf("create calls = %d, want 2", n)
	}

	if n := env.fake.tokenHits.Load(); n != 1 {
		t.Errorf("token hits = %d, want 1", n)
	}
}

func TestPeople401_OtherRefreshErrorKeepsUser(t *testing.T) {
	t.Parallel()

	env := newTestEnv(t)
	env.unauthorized(routeGet, 1)
	env.fake.tokenError = "server_error"
	ctx := context.Background()

	_, err := env.people(t).Get(ctx, "people/c1")
	if err == nil || errors.Is(err, contacts.ErrUpstreamAuth) {
		t.Fatalf("err = %v, want a plain refresh error", err)
	}

	if strings.Contains(err.Error(), "secret detail") {
		t.Errorf("error leaks the token response body: %v", err)
	}

	if n := env.fake.count(routeGet); n != 1 {
		t.Errorf("people calls = %d, want 1", n)
	}

	if _, err := env.st.UserBySubOrEmail(ctx, "sub-1"); err != nil {
		t.Errorf("UserBySubOrEmail: %v, want the user kept", err)
	}
}
