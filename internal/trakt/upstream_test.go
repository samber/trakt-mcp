package trakt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/trakt-mcp/internal/crypto"
	"github.com/samber/trakt-mcp/internal/store"
)

const (
	upClientID       = "up-client-id"
	upClientSecret   = "up-client-secret-value"
	upRedirect       = "https://mcp.example.com/oauth/trakt/callback"
	upUserAgent      = "trakt-mcp/9.9.9-test"
	upOldRefresh     = "refresh-old-secret"
	upOldAccess      = "access-old"
	upNewRefresh     = "refresh-new-secret"
	upNewAccess      = "access-new"
	upConsentAccess  = "access-consent"
	upConsentRefresh = "refresh-consent"
	// upExpiresIn mirrors Trakt's access token lifetime of one day.
	upExpiresIn = 86400
	// upSlowToken keeps the fake token endpoint busy long enough for every
	// concurrent caller to join the refresh.
	upSlowToken = 100 * time.Millisecond
	// upSecretBody is planted in fake error bodies; it must never reach an error.
	upSecretBody = "SECRET-BODY-MARKER"
)

// fakeTrakt serves the Trakt OAuth endpoints and /users/settings.
type fakeTrakt struct {
	srv *httptest.Server

	mu sync.Mutex
	// token answers /oauth/token; nil answers with a rotated pair.
	token    func(w http.ResponseWriter, body map[string]string)
	settings string
	bodies   map[string][]map[string]string
	headers  map[string]http.Header

	tokenCalls atomic.Int32
}

func newFakeTrakt(t *testing.T) *fakeTrakt {
	t.Helper()

	f := &fakeTrakt{bodies: map[string][]map[string]string{}, headers: map[string]http.Header{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeTrakt) serve(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read", http.StatusBadRequest)
		return
	}

	body := map[string]string{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, "json", http.StatusBadRequest)
			return
		}
	}

	f.mu.Lock()
	f.bodies[r.URL.Path] = append(f.bodies[r.URL.Path], body)
	f.headers[r.URL.Path] = r.Header.Clone()
	token, settings := f.token, f.settings
	f.mu.Unlock()

	switch r.URL.Path {
	case "/oauth/token":
		f.tokenCalls.Add(1)

		if token == nil {
			writeTokenPair(w, upNewAccess, upNewRefresh)
			return
		}

		token(w, body)
	case "/oauth/revoke":
		w.WriteHeader(http.StatusOK)
	case "/users/settings":
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, settings)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeTrakt) setToken(h func(w http.ResponseWriter, body map[string]string)) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.token = h
}

func (f *fakeTrakt) setSettings(body string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.settings = body
}

func (f *fakeTrakt) lastBody(path string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	b := f.bodies[path]
	if len(b) == 0 {
		return nil
	}

	return b[len(b)-1]
}

func (f *fakeTrakt) lastHeader(path string) http.Header {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.headers[path]
}

func writeTokenPair(w http.ResponseWriter, access, refresh string) {
	writeTokenPairAt(w, access, refresh, time.Now())
}

func writeTokenPairAt(w http.ResponseWriter, access, refresh string, created time.Time) {
	w.Header().Set("Content-Type", "application/json")

	resp := map[string]any{
		"access_token": access, "token_type": "bearer", "expires_in": upExpiresIn,
		"scope": "public", "created_at": created.Unix(),
	}
	if refresh != "" {
		resp["refresh_token"] = refresh
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func writeOAuthError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": code, "error_description": upSecretBody + " " + upOldRefresh,
	})
}

// faultStore wraps the real store and fails the next failSaves saves.
type faultStore struct {
	*store.Store

	failSaves atomic.Int32
	saves     atomic.Int32
}

func (s *faultStore) SaveTraktTokenIf(ctx context.Context, userID, expected string, t store.TraktToken) (bool, error) {
	s.saves.Add(1)

	if s.failSaves.Load() > 0 {
		s.failSaves.Add(-1)
		return false, errors.New("injected save failure")
	}

	return s.Store.SaveTraktTokenIf(ctx, userID, expected, t)
}

type upEnv struct {
	fake   *fakeTrakt
	st     *store.Store
	fault  *faultStore
	up     *Upstream
	userID string
	logs   *syncBuffer
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a logger.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

func newUpEnv(t *testing.T, expired bool) *upEnv {
	t.Helper()

	ctx := context.Background()
	f := newFakeTrakt(t)

	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	kr, err := crypto.ParseKeyring(key)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"), kr)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})

	u, err := st.UpsertUser(ctx, "1001", "alice")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	expiry := time.Now().Add(time.Hour)
	if expired {
		expiry = time.Now().Add(-time.Hour)
	}

	if err := st.SaveTraktToken(ctx, u.ID, store.TraktToken{
		RefreshToken: upOldRefresh, AccessToken: upOldAccess, Expiry: expiry,
	}); err != nil {
		t.Fatalf("SaveTraktToken: %v", err)
	}

	logs := &syncBuffer{}
	fault := &faultStore{Store: st}
	up := newUpstream(UpstreamConfig{
		ClientID:     upClientID,
		ClientSecret: upClientSecret,
		RedirectURL:  upRedirect,
		AuthURL:      f.srv.URL + "/oauth/authorize",
		TokenURL:     f.srv.URL + "/oauth/token",
		RevokeURL:    f.srv.URL + "/oauth/revoke",
		APIBaseURL:   f.srv.URL,
		UserAgent:    upUserAgent,
		HTTP:         f.srv.Client(),
		Logger:       slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}, fault)

	return &upEnv{fake: f, st: st, fault: fault, up: up, userID: u.ID, logs: logs}
}

func (e *upEnv) source(t *testing.T) TokenSource {
	t.Helper()

	return e.up.tokenSource(e.userID)
}

func (e *upEnv) stored(t *testing.T) store.TraktToken {
	t.Helper()

	tok, err := e.st.TraktToken(context.Background(), e.userID)
	if err != nil {
		t.Fatalf("TraktToken: %v", err)
	}

	return tok
}

func TestConcurrentExpiredCallsRefreshOnce(t *testing.T) {
	t.Parallel()

	const callers = 20

	env := newUpEnv(t, true)
	env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
		time.Sleep(upSlowToken)
		writeTokenPair(w, upNewAccess, upNewRefresh)
	})

	src := env.source(t)

	var (
		wg   sync.WaitGroup
		errs = make(chan error, callers)
		got  = make(chan string, callers)
	)

	for range callers {
		wg.Go(func() {
			tok, err := src.AccessToken(context.Background())
			if err != nil {
				errs <- err
				return
			}

			got <- tok
		})
	}

	wg.Wait()
	close(errs)
	close(got)

	for err := range errs {
		t.Errorf("AccessToken: %v", err)
	}

	for tok := range got {
		if tok != upNewAccess {
			t.Errorf("token = %q, want %q", tok, upNewAccess)
		}
	}

	if n := env.fake.tokenCalls.Load(); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}
}

func TestRefreshRotatesAndSavesBothTokens(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)

	before := time.Now()

	tok, err := env.source(t).AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if tok != upNewAccess {
		t.Errorf("token = %q", tok)
	}

	body := env.fake.lastBody("/oauth/token")
	want := map[string]string{
		"grant_type": "refresh_token", "refresh_token": upOldRefresh,
		"client_id": upClientID, "client_secret": upClientSecret, "redirect_uri": upRedirect,
	}

	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %q, want %q", k, body[k], v)
		}
	}

	if ct := env.fake.lastHeader("/oauth/token").Get("Content-Type"); ct != jsonContentType {
		t.Errorf("Content-Type = %q", ct)
	}

	stored := env.stored(t)
	if stored.RefreshToken != upNewRefresh || stored.AccessToken != upNewAccess {
		t.Errorf("stored = %+v", stored)
	}

	// created_at is now and expires_in one day; the store keeps whole seconds.
	lo := before.Add(upExpiresIn*time.Second - 2*time.Second)
	hi := time.Now().Add(upExpiresIn*time.Second + time.Second)

	if stored.Expiry.Before(lo) || stored.Expiry.After(hi) {
		t.Errorf("expiry = %v, want within [%v, %v]", stored.Expiry, lo, hi)
	}
}

func TestSaveFailureRetriedThenKeepsOldRow(t *testing.T) {
	t.Parallel()

	t.Run("two failures then success", func(t *testing.T) {
		t.Parallel()

		env := newUpEnv(t, true)
		env.fault.failSaves.Store(2)

		tok, err := env.source(t).AccessToken(context.Background())
		if err != nil {
			t.Fatalf("AccessToken: %v", err)
		}

		if tok != upNewAccess {
			t.Errorf("token = %q", tok)
		}

		if n := env.fault.saves.Load(); n != 3 {
			t.Errorf("save attempts = %d, want 3", n)
		}

		if got := env.stored(t).RefreshToken; got != upNewRefresh {
			t.Errorf("stored refresh = %q, want the rotated one", got)
		}
	})

	t.Run("always failing", testSaveAlwaysFailing)
}

func testSaveAlwaysFailing(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	// More failures than any number of attempts.
	env.fault.failSaves.Store(1 << 20)

	_, err := env.source(t).AccessToken(context.Background())
	if err == nil {
		t.Fatal("AccessToken: want error")
	}

	if errors.Is(err, ErrUpstreamAuth) {
		t.Errorf("err = %v, want a save error, not a revoked grant", err)
	}

	if n := env.fault.saves.Load(); n != maxSaveAttempts {
		t.Errorf("save attempts = %d, want %d", n, maxSaveAttempts)
	}

	if n := env.fake.tokenCalls.Load(); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}

	stored := env.stored(t)
	if stored.RefreshToken != upOldRefresh || stored.AccessToken != upOldAccess {
		t.Errorf("stored = %+v, want the old row untouched", stored)
	}

	logs := env.logs.String()
	if !strings.Contains(logs, `"level":"ERROR"`) || !strings.Contains(logs, env.userID) {
		t.Errorf("logs = %s, want an error line with the user id", logs)
	}

	assertNoSecrets(t, err.Error()+logs, upOldRefresh, upNewRefresh, upNewAccess, upClientSecret)
}

func assertNoSecrets(t *testing.T, text string, secrets ...string) {
	t.Helper()

	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%q leaked in %s", s, text)
		}
	}
}

func TestInvalidGrantDeletesUserOnlyIfRefreshUnchanged(t *testing.T) {
	t.Parallel()

	t.Run("unchanged refresh deletes the user", func(t *testing.T) {
		t.Parallel()

		env := newUpEnv(t, true)
		env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
			writeOAuthError(w, http.StatusUnauthorized, oauthErrInvalidGrant)
		})

		_, err := env.source(t).AccessToken(context.Background())
		if !errors.Is(err, ErrUpstreamAuth) {
			t.Fatalf("err = %v, want ErrUpstreamAuth", err)
		}

		if _, err := env.st.TraktToken(context.Background(), env.userID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("TraktToken err = %v, want ErrNotFound", err)
		}

		logs := env.logs.String()
		if !strings.Contains(logs, "user disconnected") || !strings.Contains(logs, reasonTraktRevoked) ||
			!strings.Contains(logs, env.userID) {
			t.Errorf("logs = %s", logs)
		}
	})

	t.Run("new consent meanwhile keeps the user", func(t *testing.T) {
		t.Parallel()

		env := newUpEnv(t, true)
		env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
			saveConsent(t, env)
			writeOAuthError(w, http.StatusBadRequest, oauthErrInvalidGrant)
		})

		tok, err := env.source(t).AccessToken(context.Background())
		if err != nil {
			t.Fatalf("AccessToken: %v", err)
		}

		if tok != upConsentAccess {
			t.Errorf("token = %q, want the new consent's", tok)
		}

		if got := env.stored(t).RefreshToken; got != upConsentRefresh {
			t.Errorf("stored refresh = %q", got)
		}

		if strings.Contains(env.logs.String(), "user disconnected") {
			t.Error("user reported disconnected")
		}
	})
}

// saveConsent stores a fresh grant, as a new consent finishing mid-refresh does.
func saveConsent(t *testing.T, env *upEnv) {
	t.Helper()

	if err := env.st.SaveTraktToken(context.Background(), env.userID, store.TraktToken{
		RefreshToken: upConsentRefresh, AccessToken: upConsentAccess, Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Errorf("SaveTraktToken: %v", err)
	}
}

func TestNewConsentDuringRefreshNotOverwritten(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
		saveConsent(t, env)
		writeTokenPair(w, upNewAccess, upNewRefresh)
	})

	tok, err := env.source(t).AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if tok != upConsentAccess {
		t.Errorf("token = %q, want the new consent's", tok)
	}

	stored := env.stored(t)
	if stored.RefreshToken != upConsentRefresh || stored.AccessToken != upConsentAccess {
		t.Errorf("stored = %+v, want the new consent", stored)
	}

	if n := env.fake.tokenCalls.Load(); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}
}

func TestNetworkErrorKeepsRefreshToken(t *testing.T) {
	t.Parallel()

	cases := map[string]func(w http.ResponseWriter, body map[string]string){
		"server error": func(w http.ResponseWriter, _ map[string]string) {
			http.Error(w, upSecretBody, http.StatusServiceUnavailable)
		},
		"connection dropped": func(w http.ResponseWriter, _ map[string]string) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("no hijacker")
				return
			}

			conn, _, err := hj.Hijack()
			if err != nil {
				t.Errorf("Hijack: %v", err)
				return
			}

			if err := conn.Close(); err != nil {
				t.Errorf("Close: %v", err)
			}
		},
		"malformed body": func(w http.ResponseWriter, _ map[string]string) {
			_, _ = io.WriteString(w, "{not json "+upSecretBody)
		},
	}

	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := newUpEnv(t, true)
			env.fake.setToken(h)

			_, err := env.source(t).AccessToken(context.Background())
			if err == nil {
				t.Fatal("want error")
			}

			if errors.Is(err, ErrUpstreamAuth) {
				t.Errorf("err = %v, want a transient error", err)
			}

			if strings.Contains(err.Error(), upSecretBody) {
				t.Errorf("err leaks body: %v", err)
			}

			stored := env.stored(t)
			if stored.RefreshToken != upOldRefresh {
				t.Errorf("stored refresh = %q, want the old one", stored.RefreshToken)
			}
		})
	}
}

func TestTokenErrorsNeverContainBody(t *testing.T) {
	t.Parallel()

	cases := map[string]func(w http.ResponseWriter, body map[string]string){
		"invalid_client": func(w http.ResponseWriter, _ map[string]string) {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client")
		},
		"invalid_request": func(w http.ResponseWriter, _ map[string]string) {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request")
		},
		"code is not a token": func(w http.ResponseWriter, _ map[string]string) {
			writeOAuthError(w, http.StatusBadRequest, upSecretBody+" "+upOldRefresh)
		},
		"plain 500": func(w http.ResponseWriter, _ map[string]string) {
			http.Error(w, upSecretBody+" "+upOldRefresh, http.StatusInternalServerError)
		},
		"200 without access token": func(w http.ResponseWriter, _ map[string]string) {
			_, _ = io.WriteString(w, `{"refresh_token":"`+upSecretBody+`"}`)
		},
	}

	secrets := []string{upSecretBody, upOldRefresh, upClientSecret, "code-secret", "verifier-secret"}

	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := newUpEnv(t, true)
			env.fake.setToken(h)

			_, refreshErr := env.source(t).AccessToken(context.Background())
			_, exchangeErr := env.up.Exchange(context.Background(), "code-secret", "verifier-secret")

			for _, err := range []error{refreshErr, exchangeErr} {
				if err == nil {
					t.Fatal("want error")
				}

				for _, s := range secrets {
					if strings.Contains(err.Error(), s) {
						t.Errorf("err %q contains %q", err, s)
					}
				}
			}

			if got := env.stored(t).RefreshToken; got != upOldRefresh {
				t.Errorf("stored refresh = %q, want the old one", got)
			}
		})
	}
}

func TestExchangeUsesPKCEVerifier(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)

	tok, err := env.up.Exchange(context.Background(), "code-1", "verifier-1")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if tok.AccessToken != upNewAccess || tok.RefreshToken != upNewRefresh || tok.Expiry.IsZero() {
		t.Errorf("token = %+v", tok)
	}

	body := env.fake.lastBody("/oauth/token")
	want := map[string]string{
		"grant_type": "authorization_code", "code": "code-1", "code_verifier": "verifier-1",
		"client_id": upClientID, "client_secret": upClientSecret, "redirect_uri": upRedirect,
	}

	for k, v := range want {
		if body[k] != v {
			t.Errorf("body[%s] = %q, want %q", k, body[k], v)
		}
	}
}

func TestRefreshWithoutNewRefreshTokenIsError(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
		writeTokenPair(w, upNewAccess, "")
	})

	if _, err := env.source(t).AccessToken(context.Background()); err == nil {
		t.Fatal("AccessToken: want error")
	}

	stored := env.stored(t)
	if stored.RefreshToken != upOldRefresh || stored.AccessToken != upOldAccess {
		t.Errorf("stored = %+v, want the old row untouched", stored)
	}

	// Exchange needs a refresh token as much as a refresh does.
	if _, err := env.up.Exchange(context.Background(), "code-1", "verifier-1"); err == nil {
		t.Error("Exchange: want error")
	}
}

func TestProfileReadsSettings(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)
	env.fake.setSettings(`{"user":{"username":"Alice","ids":{"slug":"alice","trakt":1001,"uuid":"u"},"vip":true},` +
		`"account":{"timezone":"Europe/Paris"},"browsing":{"welcome":{"exit_step":3}}}`)

	p, err := env.up.Profile(context.Background(), "access-profile")
	if err != nil {
		t.Fatalf("Profile: %v", err)
	}

	if p != (Profile{TraktID: "1001", Slug: "alice", VIP: true}) {
		t.Errorf("profile = %+v", p)
	}

	h := env.fake.lastHeader("/users/settings")
	if h.Get(headerAuth) != bearerPrefix+"access-profile" || h.Get(headerAPIKey) != upClientID ||
		h.Get(headerAPIVersion) != apiVersion {
		t.Errorf("headers = %v", h)
	}
}

func TestProfileMissingSlugIsError(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"missing slug": `{"user":{"ids":{"trakt":1001}}}`,
		"missing id":   `{"user":{"ids":{"slug":"alice"}}}`,
		"empty object": `{}`,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := newUpEnv(t, false)
			env.fake.setSettings(body)

			if _, err := env.up.Profile(context.Background(), "access-profile"); err == nil {
				t.Error("Profile: want error")
			}
		})
	}
}

func TestRejectedStaleTokenReturnsCurrentWithoutRefresh(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)

	tok, err := env.source(t).Rejected(context.Background(), "access-older-than-stored")
	if err != nil {
		t.Fatalf("Rejected: %v", err)
	}

	if tok != upOldAccess {
		t.Errorf("token = %q, want the stored one", tok)
	}

	if n := env.fake.tokenCalls.Load(); n != 0 {
		t.Errorf("token requests = %d, want 0", n)
	}
}

func TestRejectedCurrentTokenForcesRefresh(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)

	tok, err := env.source(t).Rejected(context.Background(), upOldAccess)
	if err != nil {
		t.Fatalf("Rejected: %v", err)
	}

	if tok != upNewAccess {
		t.Errorf("token = %q", tok)
	}

	if n := env.fake.tokenCalls.Load(); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}
}

func TestAuthCodeURLHasS256Challenge(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)
	verifier := "verifier-0123456789-0123456789-0123456789-abc"

	raw := env.up.AuthCodeURL("state-1", verifier)

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if u.Scheme+"://"+u.Host+u.Path != env.fake.srv.URL+"/oauth/authorize" {
		t.Errorf("url = %q", raw)
	}

	sum := sha256.Sum256([]byte(verifier))
	want := map[string]string{
		"response_type":         "code",
		"client_id":             upClientID,
		"redirect_uri":          upRedirect,
		"state":                 "state-1",
		"code_challenge_method": "S256",
		"code_challenge":        base64.RawURLEncoding.EncodeToString(sum[:]),
	}

	q := u.Query()
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}

	if q.Has("code_verifier") {
		t.Error("verifier sent in the URL")
	}
}

func TestClientWithoutGrantIsUpstreamAuth(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)

	if _, err := env.up.Client(context.Background(), "no-such-user"); !errors.Is(err, ErrUpstreamAuth) {
		t.Errorf("err = %v, want ErrUpstreamAuth", err)
	}

	if _, err := env.up.Client(context.Background(), env.userID); err != nil {
		t.Errorf("Client: %v", err)
	}
}

func TestRevokePostsJSON(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)

	if err := env.up.Revoke(context.Background(), "token-to-revoke"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	body := env.fake.lastBody("/oauth/revoke")
	if body["token"] != "token-to-revoke" || body["client_id"] != upClientID || body["client_secret"] != upClientSecret {
		t.Errorf("body = %v", body)
	}

	if ct := env.fake.lastHeader("/oauth/revoke").Get("Content-Type"); ct != jsonContentType {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestRefreshWithStaleCreatedAtDoesNotRefreshAgain(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
		// Two lifetimes ago: created_at + expires_in is already in the past.
		writeTokenPairAt(w, upNewAccess, upNewRefresh, time.Now().Add(-2*upExpiresIn*time.Second))
	})

	src := env.source(t)

	for range 2 {
		tok, err := src.AccessToken(context.Background())
		if err != nil {
			t.Fatalf("AccessToken: %v", err)
		}

		if tok != upNewAccess {
			t.Errorf("token = %q", tok)
		}
	}

	if n := env.fake.tokenCalls.Load(); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}
}

func TestUserAgentSentOnEveryCall(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	env.fake.setSettings(`{"user":{"ids":{"slug":"alice","trakt":1001}}}`)

	ctx := context.Background()

	if _, err := env.source(t).AccessToken(ctx); err != nil {
		t.Fatalf("AccessToken: %v", err)
	}

	if err := env.up.Revoke(ctx, "token-to-revoke"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, err := env.up.Profile(ctx, "access-profile"); err != nil {
		t.Fatalf("Profile: %v", err)
	}

	for _, path := range []string{"/oauth/token", "/oauth/revoke", "/users/settings"} {
		if ua := env.fake.lastHeader(path).Get(headerUserAgent); ua != upUserAgent {
			t.Errorf("%s User-Agent = %q, want %q", path, ua, upUserAgent)
		}
	}

	if ua := newUpstream(UpstreamConfig{}, env.fault).userAgent; ua != defaultUserAgent {
		t.Errorf("default User-Agent = %q, want %q", ua, defaultUserAgent)
	}
}

func TestProfileMalformedBodyNotInError(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)
	env.fake.setSettings(`{"user":{"ids":{"slug":"` + upSecretBody + `","trakt":"` + upSecretBody + `"}}}`)

	_, err := env.up.Profile(context.Background(), "access-profile")
	if err == nil {
		t.Fatal("Profile: want error")
	}

	assertNoSecrets(t, err.Error(), upSecretBody, "int64", "slug")

	if !strings.Contains(err.Error(), "malformed settings JSON") {
		t.Errorf("err = %v, want the static malformed-JSON error", err)
	}
}

func TestConfigPrintingRedactsSecret(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)
	cfg := UpstreamConfig{ClientID: upClientID, ClientSecret: upClientSecret, RedirectURL: upRedirect}

	var logs bytes.Buffer

	slog.New(slog.NewJSONHandler(&logs, nil)).Info("config", slog.Any("cfg", cfg))

	out := strings.Join([]string{
		fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg),
		fmt.Sprintf("%+v", &cfg), fmt.Sprintf("%#v", &cfg),
		fmt.Sprintf("%v", env.up), fmt.Sprintf("%+v", env.up), fmt.Sprintf("%#v", env.up),
		logs.String(),
	}, "\n")

	assertNoSecrets(t, out, upClientSecret)

	if !strings.Contains(out, upClientID) {
		t.Errorf("output lacks the client id: %s", out)
	}
}

func TestNewCodeVerifier(t *testing.T) {
	t.Parallel()

	a, err := NewCodeVerifier()
	if err != nil {
		t.Fatalf("NewCodeVerifier: %v", err)
	}

	b, err := NewCodeVerifier()
	if err != nil {
		t.Fatalf("NewCodeVerifier: %v", err)
	}

	// 32 random bytes encode to 43 base64url characters without padding.
	const wantLen = 43

	for _, v := range []string{a, b} {
		if len(v) != wantLen {
			t.Errorf("len(%q) = %d, want %d", v, len(v), wantLen)
		}

		if strings.Trim(v, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_") != "" {
			t.Errorf("%q has characters outside base64url", v)
		}
	}

	if a == b {
		t.Error("two verifiers are equal")
	}
}

func TestRejectedEmptyReturnsCurrentWithoutRefresh(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)

	if tok, err := env.source(t).Rejected(context.Background(), ""); err != nil || tok != upOldAccess ||
		env.fake.tokenCalls.Load() != 0 {
		t.Errorf("Rejected(\"\") = %q, %v, token calls %d; want the stored token, no refresh",
			tok, err, env.fake.tokenCalls.Load())
	}
}

func TestCanceledCallerDoesNotAbortSharedRefresh(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	entered := make(chan struct{})
	release := make(chan struct{})

	env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
		close(entered)
		<-release
		writeTokenPair(w, upNewAccess, upNewRefresh)
	})

	src := env.source(t)
	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)

	go func() {
		_, err := src.AccessToken(ctxA)
		errA <- err
	}()

	<-entered

	type result struct {
		tok string
		err error
	}

	resB := make(chan result, 1)

	go func() {
		tok, err := src.AccessToken(context.Background())
		resB <- result{tok, err}
	}()

	cancelA()

	if err := <-errA; !errors.Is(err, context.Canceled) {
		t.Errorf("caller A err = %v, want context.Canceled", err)
	}

	close(release)

	if b := <-resB; b.err != nil || b.tok != upNewAccess {
		t.Errorf("caller B = %q, %v; want %q", b.tok, b.err, upNewAccess)
	}

	if got := env.stored(t).RefreshToken; got != upNewRefresh {
		t.Errorf("stored refresh = %q, want the rotated one", got)
	}

	if n := env.fake.tokenCalls.Load(); n != 1 {
		t.Errorf("token requests = %d, want 1", n)
	}
}

// startAbandonedRefresh starts a refresh whose only caller gives up while the
// token endpoint is still answering, as a client timing out during shutdown
// would. It returns once the caller has returned; closing release lets the
// endpoint answer with a rotated pair.
func startAbandonedRefresh(t *testing.T, env *upEnv) (releaseFn func()) {
	t.Helper()

	entered := make(chan struct{})
	release := make(chan struct{})

	env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
		close(entered)
		<-release
		writeTokenPair(w, upNewAccess, upNewRefresh)
	})

	// Registered before the store-closing cleanup runs, so a failing test
	// never leaves the endpoint blocked.
	var once sync.Once

	t.Cleanup(func() { once.Do(func() { close(release) }) })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := env.source(t).AccessToken(ctx)
		done <- err
	}()

	<-entered
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller err = %v, want context.Canceled", err)
	}

	return func() { once.Do(func() { close(release) }) }
}

func TestWaitReturnsAfterDetachedRefreshSaved(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	release := startAbandonedRefresh(t, env)

	waited := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		waited <- env.up.Wait(ctx)
	}()

	select {
	case err := <-waited:
		t.Fatalf("Wait returned %v while a refresh was in flight", err)
	case <-time.After(50 * time.Millisecond):
	}

	release()

	if err := <-waited; err != nil {
		t.Fatalf("Wait: %v", err)
	}

	// Wait returning means the store may be closed now: the rotated pair must
	// already be there.
	if got := env.stored(t).RefreshToken; got != upNewRefresh {
		t.Errorf("stored refresh after Wait = %q, want the rotated one", got)
	}
}

func TestWaitHonorsContextExpiry(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	release := startAbandonedRefresh(t, env)

	defer func() {
		release()
		// Let the flight finish before the store closes in cleanup.
		if err := env.up.Wait(context.Background()); err != nil {
			t.Errorf("final Wait: %v", err)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if err := env.up.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait = %v, want context.DeadlineExceeded", err)
	}
}

func TestWaitWithoutFlightsReturnsAtOnce(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, false)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := env.up.Wait(ctx); err != nil {
		t.Errorf("Wait with no flight = %v, want nil", err)
	}
}

func TestInvalidGrantAfterLostSaveIsNotLoggedAsRevoked(t *testing.T) {
	t.Parallel()

	env := newUpEnv(t, true)
	// Every save of the rotated pair fails: the stored refresh token is now
	// spent at Trakt, which Trakt reports as invalid_grant next time.
	env.fault.failSaves.Store(maxSaveAttempts)

	if _, err := env.source(t).AccessToken(context.Background()); err == nil {
		t.Fatal("first AccessToken: want a save error")
	}

	env.fake.setToken(func(w http.ResponseWriter, _ map[string]string) {
		writeOAuthError(w, http.StatusBadRequest, oauthErrInvalidGrant)
	})

	if _, err := env.source(t).AccessToken(context.Background()); !errors.Is(err, ErrUpstreamAuth) {
		t.Fatalf("second AccessToken err = %v, want ErrUpstreamAuth", err)
	}

	logs := env.logs.String()
	if !strings.Contains(logs, `"reason":"`+reasonTokenLost+`"`) {
		t.Errorf("logs = %s, want reason %s", logs, reasonTokenLost)
	}

	if strings.Contains(logs, reasonTraktRevoked) {
		t.Errorf("logs = %s, want no %s after a lost save", logs, reasonTraktRevoked)
	}
}

// TestRefreshFailureKinds checks that a refresh Trakt could not answer maps
// to the Kinds spec §8.6 gives texts for, not to an opaque internal error.
func TestRefreshFailureKinds(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		token      func(w http.ResponseWriter, body map[string]string)
		kind       Kind
		wantStatus int
	}{
		"server error": {
			token: func(w http.ResponseWriter, _ map[string]string) {
				http.Error(w, upSecretBody, http.StatusBadGateway)
			},
			kind: KindServer, wantStatus: http.StatusBadGateway,
		},
		"connection dropped": {
			token: func(w http.ResponseWriter, _ map[string]string) {
				hj, ok := w.(http.Hijacker)
				if !ok {
					t.Error("no hijacker")
					return
				}

				conn, _, err := hj.Hijack()
				if err != nil {
					t.Errorf("Hijack: %v", err)
					return
				}

				_ = conn.Close()
			},
			kind: KindUnreachable,
		},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := newUpEnv(t, true)
			env.fake.setToken(c.token)

			_, err := env.source(t).AccessToken(context.Background())

			te, ok := errors.AsType[*Error](err)
			if !ok || te.Kind != c.kind || te.Status != c.wantStatus {
				t.Fatalf("err = %v (%+v), want Kind %s status %d", err, te, c.kind, c.wantStatus)
			}

			if strings.Contains(err.Error(), upSecretBody) {
				t.Errorf("err leaks body: %v", err)
			}
		})
	}
}
