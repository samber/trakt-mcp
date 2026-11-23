package oauth

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ory/fosite"
	"golang.org/x/time/rate"

	"github.com/samber/google-contacts-mcp/internal/crypto"
	"github.com/samber/google-contacts-mcp/internal/store"
)

const (
	// testRedirectClaude is a registered https redirect URI reused across redirect matching cases.
	testRedirectClaude = "https://claude.ai/cb"
	// testRedirectLoopback is a registered loopback redirect URI used to exercise port-agnostic matching.
	testRedirectLoopback = "http://127.0.0.1/callback"
	// testRedirectNative is a registered private-use scheme redirect URI (RFC 8252 section 7.1).
	testRedirectNative = "com.example.app:/callback"
)

// testKey is a fixed keyring entry: the store needs one to open.
const testKey = "A:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

func openStore(t *testing.T) *store.Store {
	t.Helper()

	kr, err := crypto.ParseKeyring(testKey)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"), kr)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return st
}

func TestGetClient_UnknownNonURLID(t *testing.T) {
	t.Parallel()

	r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))

	_, err := r.GetClient(context.Background(), "no-such-client")
	if !errors.Is(err, fosite.ErrNotFound) {
		t.Fatalf("err = %v, want fosite.ErrNotFound", err)
	}
}

func TestGetClient_DCRRow(t *testing.T) {
	t.Parallel()

	st := openStore(t)

	want := &store.OAuthClient{ID: "abc", Kind: store.ClientKindDCR, Name: "n", RedirectURIs: []string{"https://x.example.com/cb"}}
	if err := st.SaveClient(context.Background(), want); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	r := NewClientResolver(st, NewCIMDFetcher(CIMDOptions{}))

	c, err := r.GetClient(context.Background(), "abc")
	if err != nil {
		t.Fatalf("GetClient: %v", err)
	}

	if c.GetID() != "abc" || c.GetRedirectURIs()[0] != "https://x.example.com/cb" {
		t.Errorf("client = %+v", c)
	}
}

func TestGetClient_CIMDCacheRefreshAndStale(t *testing.T) {
	t.Parallel()

	var (
		hits atomic.Int32
		fail atomic.Bool
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)

		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Cache-Control", "max-age=60")
		writeJSON(t, w, docFor(r))
	}))
	defer srv.Close()

	now := fetchNow
	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})
	f.now = func() time.Time { return now }
	r := NewClientResolver(openStore(t), f)
	r.now = func() time.Time { return now }
	ctx := context.Background()
	id := srv.URL + testDocPath

	for i := range 2 {
		c, err := r.GetClient(ctx, id)
		if err != nil {
			t.Fatalf("GetClient #%d: %v", i, err)
		}

		if c.GetID() != id {
			t.Fatalf("id = %q", c.GetID())
		}
	}

	if got := hits.Load(); got != 1 {
		t.Fatalf("hits after cached read = %d, want 1", got)
	}

	now = now.Add(2 * time.Minute)

	if _, err := r.GetClient(ctx, id); err != nil {
		t.Fatalf("GetClient after expiry: %v", err)
	}

	if got := hits.Load(); got != 2 {
		t.Fatalf("hits after expiry = %d, want 2", got)
	}

	now = now.Add(2 * time.Minute)

	fail.Store(true)

	c, err := r.GetClient(ctx, id)
	if err != nil {
		t.Fatalf("stale row not served: %v", err)
	}

	if c.GetID() != id {
		t.Errorf("stale id = %q", c.GetID())
	}

	if got := hits.Load(); got != 3 {
		t.Errorf("hits after failed refetch = %d, want 3", got)
	}
}

func TestGetClient_CIMDFetchFailsWithoutCache(t *testing.T) {
	t.Parallel()

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	r := NewClientResolver(openStore(t), newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true}))
	if _, err := r.GetClient(context.Background(), srv.URL+testDocPath); err == nil {
		t.Fatal("unfetchable client resolved")
	}
}

func TestRedirectMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		registered []string
		got        string
		want       bool
	}{
		{"exact", []string{"https://claude.ai/api/mcp/auth_callback"}, "https://claude.ai/api/mcp/auth_callback", true},
		{"second of many", []string{"https://a.example.com/cb", "https://b.example.com/cb"}, "https://b.example.com/cb", true},
		{"path mismatch", []string{"https://claude.ai/api/mcp/auth_callback"}, "https://claude.ai/api/mcp/other", false},
		{"trailing slash", []string{testRedirectClaude}, "https://claude.ai/cb/", false},
		{"https port differs", []string{testRedirectClaude}, "https://claude.ai:8443/cb", false},
		{"host differs", []string{testRedirectClaude}, "https://evil.ai/cb", false},
		{"query differs", []string{"https://claude.ai/cb"}, "https://claude.ai/cb?x=1", false},
		{"loopback v4 any port", []string{testRedirectLoopback}, "http://127.0.0.1:53124/callback", true},
		{"loopback v4 port to port", []string{"http://127.0.0.1:1000/callback"}, "http://127.0.0.1:2000/callback", true},
		{"localhost any port", []string{"http://localhost:3000/callback"}, "http://localhost:61000/callback", true},
		{"loopback v6 any port", []string{"http://[::1]/callback"}, "http://[::1]:9999/callback", true},
		{"loopback path mismatch", []string{testRedirectLoopback}, "http://127.0.0.1:5000/other", false},
		{"loopback host differs", []string{testRedirectLoopback}, "http://localhost:5000/callback", false},
		{"loopback scheme differs", []string{"http://127.0.0.1/callback"}, "https://127.0.0.1:5000/callback", false},
		{"loopback query differs", []string{"http://127.0.0.1/callback"}, "http://127.0.0.1:5000/callback?a=b", false},
		{"loopback userinfo", []string{"http://127.0.0.1/callback"}, "http://u@127.0.0.1:5000/callback", false},
		{"non-loopback http port", []string{"http://example.com/cb"}, "http://example.com:81/cb", false},
		{"private-use exact", []string{testRedirectNative}, testRedirectNative, true},
		{"private-use path differs", []string{testRedirectNative}, "com.example.app:/other", false},
		{"private-use scheme differs", []string{testRedirectNative}, "com.example.other:/callback", false},
		{"private-use query differs", []string{testRedirectNative}, "com.example.app:/callback?a=b", false},
		{"empty registered", nil, "https://claude.ai/cb", false},
		{"unparsable", []string{"https://claude.ai/cb"}, "://bad", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := RedirectMatches(tt.registered, tt.got); got != tt.want {
				t.Errorf("RedirectMatches(%v, %q) = %v, want %v", tt.registered, tt.got, got, tt.want)
			}
		})
	}
}

func postRegister(t *testing.T, r *ClientResolver, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/oauth/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	r.RegisterHandler().ServeHTTP(rec, req)

	return rec
}

// registerTestResponse is the RFC 7591 registration response body.
type registerTestResponse struct {
	ClientID                string   `json:"client_id"`
	ClientIDIssuedAt        int64    `json:"client_id_issued_at"`
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
	GrantTypes              []string `json:"grant_types"`
	ResponseTypes           []string `json:"response_types"`
	ClientName              string   `json:"client_name"`
}

func TestRegister_OK(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	r := NewClientResolver(st, NewCIMDFetcher(CIMDOptions{}))
	r.now = func() time.Time { return fetchNow }

	rec := postRegister(t, r, `{
		"client_name": "Claude",
		"redirect_uris": ["https://claude.ai/api/mcp/auth_callback", "http://localhost:6274/cb"],
		"token_endpoint_auth_method": "none",
		"grant_types": ["authorization_code", "refresh_token"],
		"response_types": ["code"],
		"application_type": "web"
	}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}

	var resp registerTestResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	assertRegistrationResponse(t, &resp)
	assertRegistrationStored(t, st, r, resp.ClientID)
}

// assertRegistrationResponse checks the response fields of TestRegister_OK.
func assertRegistrationResponse(t *testing.T, resp *registerTestResponse) {
	t.Helper()

	if raw, err := hex.DecodeString(resp.ClientID); err != nil || len(raw) != clientIDBytes {
		t.Errorf("client_id = %q, want %d hex bytes", resp.ClientID, clientIDBytes)
	}

	if resp.ClientIDIssuedAt != fetchNow.Unix() || resp.TokenEndpointAuthMethod != authMethodNone || resp.ClientName != "Claude" {
		t.Errorf("resp = %+v", resp)
	}

	if len(resp.RedirectURIs) != 2 || len(resp.GrantTypes) != 2 || len(resp.ResponseTypes) != 1 {
		t.Errorf("resp lists = %+v", resp)
	}
}

// assertRegistrationStored checks that the registered client is persisted and
// resolvable by its id.
func assertRegistrationStored(t *testing.T, st *store.Store, r *ClientResolver, clientID string) {
	t.Helper()

	c, err := st.ClientByID(context.Background(), clientID)
	if err != nil {
		t.Fatalf("ClientByID: %v", err)
	}

	if c.Kind != store.ClientKindDCR || c.Name != "Claude" || len(c.RedirectURIs) != 2 {
		t.Errorf("stored = %+v", c)
	}

	if fc, err := r.GetClient(context.Background(), clientID); err != nil || fc.GetID() != clientID {
		t.Errorf("GetClient = %v, %v", fc, err)
	}
}

func TestRegister_MinimalDefaultsName(t *testing.T) {
	t.Parallel()

	r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))

	rec := postRegister(t, r, `{"redirect_uris": ["https://app.example.com/cb"]}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	if !bytes.Contains(rec.Body.Bytes(), []byte(`"client_name":"app.example.com"`)) {
		t.Errorf("body = %s", rec.Body)
	}
}

func TestRegister_RejectsConfidential(t *testing.T) {
	t.Parallel()

	r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))
	rec := postRegister(t, r, `{"redirect_uris": ["https://app.example.com/cb"], "token_endpoint_auth_method": "client_secret_basic"}`)
	assertRegisterError(t, rec, "invalid_client_metadata")
}

func TestRegister_Rejects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body, wantErr string
	}{
		{"no redirect uris", `{}`, errInvalidRedirectURI},
		{"empty redirect uris", `{"redirect_uris": []}`, errInvalidRedirectURI},
		{"relative redirect", `{"redirect_uris": ["/cb"]}`, errInvalidRedirectURI},
		{"http non-loopback", `{"redirect_uris": ["http://app.example.com/cb"]}`, errInvalidRedirectURI},
		{"custom scheme without a dot", `{"redirect_uris": ["myapp://cb"]}`, errInvalidRedirectURI},
		{"javascript scheme", `{"redirect_uris": ["javascript:alert(1)"]}`, errInvalidRedirectURI},
		{"data scheme", `{"redirect_uris": ["data:text/html,x"]}`, errInvalidRedirectURI},
		{"file scheme", `{"redirect_uris": ["file:///etc/passwd"]}`, errInvalidRedirectURI},
		{"private-use opaque", `{"redirect_uris": ["com.example.app:callback"]}`, errInvalidRedirectURI},
		{"private-use without path", `{"redirect_uris": ["com.example.app:"]}`, errInvalidRedirectURI},
		{"private-use userinfo", `{"redirect_uris": ["com.example.app://user@host/cb"]}`, errInvalidRedirectURI},
		{"private-use fragment", `{"redirect_uris": ["com.example.app:/cb#x"]}`, errInvalidRedirectURI},
		{"https userinfo", `{"redirect_uris": ["https://user@app.example.com/cb"]}`, errInvalidRedirectURI},
		{"fragment", `{"redirect_uris": ["https://app.example.com/cb#x"]}`, "invalid_redirect_uri"},
		{"bad application type", `{"redirect_uris": ["https://app.example.com/cb"], "application_type": "service"}`, errInvalidClientMetadata},
		{"bad grant type", `{"redirect_uris": ["https://app.example.com/cb"], "grant_types": ["client_credentials"]}`, errInvalidClientMetadata},
		{"bad response type", `{"redirect_uris": ["https://app.example.com/cb"], "response_types": ["token"]}`, errInvalidClientMetadata},
		{"malformed json", `{"redirect_uris": [`, "invalid_client_metadata"},
		{"too large", `{"client_name": "` + strings.Repeat("x", int(maxRegistrationBody)) + `"}`, "invalid_client_metadata"},
		{"too many redirect uris", `{"redirect_uris": [` + strings.Repeat(`"https://app.example.com/cb",`, maxRedirectURIs) +
			`"https://app.example.com/cb"]}`, "invalid_redirect_uri"},
		{
			"redirect uri too long", `{"redirect_uris": ["https://app.example.com/` + strings.Repeat("a", maxRedirectURIBytes) + `"]}`,
			"invalid_redirect_uri",
		},
		{"client name too long", `{"redirect_uris": ["https://app.example.com/cb"], "client_name": "` +
			strings.Repeat("é", maxClientNameRunes+1) + `"}`, "invalid_client_metadata"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))
			assertRegisterError(t, postRegister(t, r, tt.body), tt.wantErr)
		})
	}
}

func TestRegister_PrivateUseSchemeAllowed(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{testRedirectNative, "com.example.app:///callback", "com.example.app://callback", "io.github.user.app:/oauth2redirect?x=1"} {
		r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))

		rec := postRegister(t, r, `{"redirect_uris": ["`+uri+`"]}`)
		if rec.Code != http.StatusCreated {
			t.Errorf("%s: status = %d, body %s", uri, rec.Code, rec.Body)
		}
	}
}

func TestRegister_LoopbackHTTPAllowed(t *testing.T) {
	t.Parallel()

	for _, uri := range []string{"http://127.0.0.1:33418/cb", "http://localhost/cb", "http://[::1]:8080/cb"} {
		r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))
		if rec := postRegister(t, r, `{"redirect_uris": ["`+uri+`"]}`); rec.Code != http.StatusCreated {
			t.Errorf("%s: status = %d, body %s", uri, rec.Code, rec.Body)
		}
	}
}

func TestRegister_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))
	rec := httptest.NewRecorder()
	r.RegisterHandler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/oauth/register", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", rec.Code)
	}
}

func assertRegisterError(t *testing.T, rec *httptest.ResponseRecorder, wantErr string) {
	t.Helper()

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	var body struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.Body)
	}

	if body.Error != wantErr || body.Description == "" {
		t.Errorf("error = %+v, want %q", body, wantErr)
	}
}

func TestRegister_LimitsAtBoundary(t *testing.T) {
	t.Parallel()

	r := NewClientResolver(openStore(t), NewCIMDFetcher(CIMDOptions{}))

	uris := make([]string, maxRedirectURIs)
	for i := range uris {
		uris[i] = fmt.Sprintf("https://app.example.com/%s%d", strings.Repeat("a", maxRedirectURIBytes-40), i)
	}

	body, err := json.Marshal(map[string]any{
		"redirect_uris": uris,
		// Surrounding blanks are trimmed before counting.
		"client_name": "  " + strings.Repeat("é", maxClientNameRunes) + "  ",
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := postRegister(t, r, string(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	if !bytes.Contains(rec.Body.Bytes(), []byte(`"client_name":"`+strings.Repeat("é", maxClientNameRunes)+`"`)) {
		t.Errorf("client_name not trimmed: %s", rec.Body)
	}
}

func TestRegister_BudgetSpentReturns429(t *testing.T) {
	t.Parallel()

	st := openStore(t)
	r := NewClientResolver(st, NewCIMDFetcher(CIMDOptions{}))
	r.now = func() time.Time { return fetchNow }

	r.dcrLimit = newBucket(rate.Every(time.Hour), 1)
	if rec := postRegister(t, r, `{"redirect_uris": ["https://app.example.com/cb"]}`); rec.Code != http.StatusCreated {
		t.Fatalf("first status = %d, body %s", rec.Code, rec.Body)
	}

	rec := postRegister(t, r, `{"redirect_uris": ["https://app.example.com/cb"]}`)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429, body %s", rec.Code, rec.Body)
	}

	if ra, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || ra < 1 {
		t.Errorf("Retry-After = %q, want a positive number of seconds", rec.Header().Get("Retry-After"))
	}

	var body struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Error != errTemporarilyUnavailable {
		t.Errorf("body = %s (%v), want error %q", rec.Body, err, errTemporarilyUnavailable)
	}
	// An invalid request is refused before it spends budget.
	assertRegisterError(t, postRegister(t, r, `{}`), "invalid_redirect_uri")
}

// cimdServer serves a valid metadata document, or status when it is non-zero.
func cimdServer(t *testing.T) (*httptest.Server, *atomic.Int32, *atomic.Int64) {
	t.Helper()

	var (
		hits   atomic.Int32
		status atomic.Int64
	)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)

		if s := status.Load(); s != 0 {
			w.WriteHeader(int(s))
			return
		}

		w.Header().Set("Cache-Control", "max-age=60")
		writeJSON(t, w, docFor(r))
	}))
	t.Cleanup(srv.Close)

	return srv, &hits, &status
}

func TestGetClient_CIMDBudgetSpent(t *testing.T) {
	t.Parallel()

	srv, hits, _ := cimdServer(t)
	now := fetchNow
	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})
	f.now = func() time.Time { return now }
	r := NewClientResolver(openStore(t), f)
	r.now = func() time.Time { return now }
	r.cimdLimit = newBucket(rate.Every(time.Hour), 1)
	ctx := context.Background()
	id := srv.URL + testDocPath

	if _, err := r.GetClient(ctx, id); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	// A cold lookup of another client finds the budget spent and fetches nothing.
	if _, err := r.GetClient(ctx, srv.URL+"/other.json"); !errors.Is(err, ErrClientRateLimited) {
		t.Fatalf("cold lookup err = %v, want ErrClientRateLimited", err)
	}
	// An expired cached client is served stale instead of refetched.
	now = now.Add(2 * time.Minute)

	if c, err := r.GetClient(ctx, id); err != nil || c.GetID() != id {
		t.Fatalf("stale lookup = %v, %v, want the cached client", c, err)
	}

	if got := hits.Load(); got != 1 {
		t.Errorf("hits = %d, want 1: a spent budget must not fetch", got)
	}
}

func TestGetClient_CIMDDefinitiveFailureDeletesCache(t *testing.T) {
	t.Parallel()

	for _, code := range []int{http.StatusNotFound, http.StatusGone, http.StatusBadRequest} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			t.Parallel()

			srv, _, status := cimdServer(t)
			now := fetchNow
			f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})
			f.now = func() time.Time { return now }
			st := openStore(t)
			r := NewClientResolver(st, f)
			r.now = func() time.Time { return now }
			ctx := context.Background()

			id := srv.URL + testDocPath
			if _, err := r.GetClient(ctx, id); err != nil {
				t.Fatalf("first fetch: %v", err)
			}

			now = now.Add(2 * time.Minute)

			status.Store(int64(code))

			if _, err := r.GetClient(ctx, id); err == nil {
				t.Fatal("withdrawn client still served from cache")
			}

			if _, err := st.ClientByID(ctx, id); !errors.Is(err, store.ErrNotFound) {
				t.Errorf("cached row kept: %v", err)
			}
		})
	}
}

func TestGetClient_CIMDStalenessCapped(t *testing.T) {
	t.Parallel()

	srv, _, status := cimdServer(t)
	now := fetchNow
	f := newTestFetcher(t, srv, CIMDOptions{AllowPrivate: true})
	f.now = func() time.Time { return now }
	r := NewClientResolver(openStore(t), f)
	r.now = func() time.Time { return now }
	ctx := context.Background()

	id := srv.URL + testDocPath
	if _, err := r.GetClient(ctx, id); err != nil {
		t.Fatalf("first fetch: %v", err)
	}

	status.Store(http.StatusServiceUnavailable)

	expiry := fetchNow.Add(time.Minute)

	now = expiry.Add(maxStaleness - time.Second)

	if _, err := r.GetClient(ctx, id); err != nil {
		t.Fatalf("stale copy inside the cap not served: %v", err)
	}

	now = expiry.Add(maxStaleness + time.Second)

	if _, err := r.GetClient(ctx, id); err == nil {
		t.Fatal("stale copy served past the cap")
	}
}
