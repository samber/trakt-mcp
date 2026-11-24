package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/oauth2"
	"golang.org/x/time/rate"

	"github.com/samber/trakt-mcp/internal/config"
	"github.com/samber/trakt-mcp/internal/crypto"
	"github.com/samber/trakt-mcp/internal/obs"
	"github.com/samber/trakt-mcp/internal/store"
	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	testBaseURL       = "https://mcp.example.com"
	testResource      = testBaseURL + "/mcp"
	testClientID      = "client-1"
	testRedirect      = "https://claude.example.com/api/callback"
	testTraktClientID = "trakt-client-id"
	testTraktSecret   = "trakt-client-secret" //nolint:gosec // G101: a fake value for the fake Trakt
	testState         = "client-state-123"

	// testTraktAuthHost is the Trakt consent host the browser is sent to; the
	// tests never fetch it.
	testTraktAuthHost = "trakt.example.com"
	testTraktAuthPath = "/oauth/authorize"

	// The fake Trakt account and grant.
	fakeTraktCode    = "trakt-code"
	fakeTraktAccess  = "trakt-access"
	fakeTraktRefresh = "trakt-refresh"
	fakeTraktID      = 4242
	fakeTraktIDStr   = "4242"
	fakeTraktSlug    = "ada-lovelace"
	// fakeUpstreamBody is a marker the fake writes into failed responses; no
	// page may ever show it.
	fakeUpstreamBody = "upstream-body-marker-7f3a"
	// settingsIDTrakt is the numeric id key of /users/settings' user.ids.
	settingsIDTrakt = "trakt"
)

// fakeTrakt serves Trakt's token endpoint, revoke endpoint and /users/settings.
type fakeTrakt struct {
	t   *testing.T
	srv *httptest.Server

	mu sync.Mutex
	// tokenStatus and settingsStatus, when non-zero, make the endpoint fail
	// with that status and fakeUpstreamBody.
	tokenStatus    int
	settingsStatus int
	traktID        int64
	slug           string
	// verifiers are the PKCE verifiers received by the token endpoint.
	verifiers []string

	revokeHits atomic.Int32
}

func newFakeTrakt(t *testing.T) *fakeTrakt {
	t.Helper()

	f := &fakeTrakt{t: t, traktID: fakeTraktID, slug: fakeTraktSlug}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /oauth/token", f.token)
	mux.HandleFunc("GET /users/settings", f.settings)
	mux.HandleFunc("POST /oauth/revoke", func(http.ResponseWriter, *http.Request) { f.revokeHits.Add(1) })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeTrakt) set(fn func(f *fakeTrakt)) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fn(f)
}

func (f *fakeTrakt) receivedVerifiers() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.verifiers...)
}

func (f *fakeTrakt) token(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		f.t.Errorf("decode token body: %v", err)
	}

	f.mu.Lock()
	status := f.tokenStatus
	f.verifiers = append(f.verifiers, body["code_verifier"])
	f.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(fakeUpstreamBody))

		return
	}

	if body["grant_type"] != grantAuthorizationCode || body["code"] != fakeTraktCode || body["code_verifier"] == "" ||
		body["client_id"] != testTraktClientID || body["client_secret"] != testTraktSecret ||
		!strings.HasSuffix(body["redirect_uri"], pathCallback) {
		w.WriteHeader(http.StatusUnauthorized)
		writeJSON(f.t, w, map[string]string{"error": "invalid_grant"})

		return
	}

	writeJSON(f.t, w, map[string]any{
		"access_token":    fakeTraktAccess,
		grantRefreshToken: fakeTraktRefresh,
		"token_type":      "bearer",
		"expires_in":      int64((24 * time.Hour).Seconds()),
		"created_at":      time.Now().Unix(),
		"scope":           "public",
	})
}

func (f *fakeTrakt) settings(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	status, id, slug := f.settingsStatus, f.traktID, f.slug
	f.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(fakeUpstreamBody))

		return
	}

	if r.Header.Get("Authorization") != "Bearer "+fakeTraktAccess {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	writeJSON(f.t, w, map[string]any{
		"user": map[string]any{
			"username": slug,
			"vip":      false,
			"ids":      map[string]any{settingsIDTrakt: id, "slug": slug},
		},
	})
}

// env is a Server wired to a fake Trakt and a temp store with one DCR client.
type env struct {
	t     *testing.T
	srv   *Server
	mux   *http.ServeMux
	st    *store.Store
	kr    *crypto.Keyring
	trakt *fakeTrakt
	now   atomic.Pointer[time.Time]
	// reg holds the metrics of every server of this env.
	reg     *prometheus.Registry
	metrics *obs.Metrics
}

type envOption func(cfg *config.Config)

func newEnv(t *testing.T, opts ...envOption) *env {
	t.Helper()

	kr, err := crypto.ParseKeyring(testKey)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}

	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "db.sqlite"), kr)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	if err := st.SaveClient(context.Background(), &store.OAuthClient{
		ID: testClientID, Kind: store.ClientKindDCR, Name: "Claude",
		RedirectURIs: []string{testRedirect, "http://localhost:3000/callback"},
	}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	reg := prometheus.NewRegistry()
	e := &env{t: t, st: st, kr: kr, trakt: newFakeTrakt(t), reg: reg, metrics: obs.NewMetrics(reg)}
	e.srv, e.mux = e.newServer(testBaseURL, opts...)

	return e
}

func (e *env) newServer(base string, opts ...envOption) (*Server, *http.ServeMux) {
	e.t.Helper()

	baseURL, err := url.Parse(base)
	if err != nil {
		e.t.Fatalf("parse base: %v", err)
	}

	cfg := config.Config{
		BaseURL:       baseURL,
		TraktClientID: testTraktClientID,
		EnableDCR:     true,
	}
	for _, o := range opts {
		o(&cfg)
	}

	f := e.trakt
	up := trakt.NewUpstream(trakt.UpstreamConfig{
		ClientID:     testTraktClientID,
		ClientSecret: testTraktSecret,
		RedirectURL:  base + pathCallback,
		AuthURL:      "https://" + testTraktAuthHost + testTraktAuthPath,
		TokenURL:     f.srv.URL + "/oauth/token",
		RevokeURL:    f.srv.URL + "/oauth/revoke",
		APIBaseURL:   f.srv.URL,
		HTTP:         f.srv.Client(),
	}, e.st)

	srv, err := New(cfg, Deps{
		Store:    e.st,
		Clients:  NewClientResolver(e.st, NewCIMDFetcher(CIMDOptions{})),
		Upstream: up,
		Keyring:  e.kr,
		Metrics:  e.metrics,
		Now: func() time.Time {
			if p := e.now.Load(); p != nil {
				return *p
			}

			return time.Now()
		},
	})
	if err != nil {
		e.t.Fatalf("New: %v", err)
	}

	mux := http.NewServeMux()
	srv.Mount(mux)

	return srv, mux
}

func (e *env) advance(d time.Duration) {
	n := time.Now().Add(d)
	e.now.Store(&n)
}

func (e *env) do(req *http.Request, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	for _, c := range cookies {
		if c == nil {
			continue
		}

		req.AddCookie(c)
	}

	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)

	return rec
}

func (e *env) get(target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return e.do(httptest.NewRequestWithContext(e.t.Context(), http.MethodGet, target, nil), cookies...)
}

func (e *env) postForm(target string, form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(e.t.Context(), http.MethodPost, target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	return e.do(req, cookies...)
}

// pkce is a client-side PKCE pair.
type pkce struct{ verifier, challenge string }

func newPKCE() pkce {
	v := oauth2.GenerateVerifier()

	return pkce{verifier: v, challenge: s256(v)}
}

// s256 is the RFC 7636 S256 transform of a verifier.
func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func authorizeParams(p pkce) url.Values {
	return url.Values{
		"response_type":         {paramCode},
		paramClientID:           {testClientID},
		paramRedirectURI:        {testRedirect},
		"scope":                 {MCPScope},
		paramState:              {testState},
		"code_challenge":        {p.challenge},
		"code_challenge_method": {"S256"},
		"resource":              {testResource},
	}
}

var (
	pendingRe = regexp.MustCompile(`name="pending" value="([^"]+)"`)
	csrfRe    = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
)

// consentPage is what GET /oauth/authorize returned.
type consentPage struct {
	pending, csrf string
	cookie        *http.Cookie
	body          string
}

func (e *env) authorize(params url.Values) consentPage {
	e.t.Helper()

	rec := e.get("/oauth/authorize?" + params.Encode())
	if rec.Code != http.StatusOK {
		e.t.Fatalf("authorize status = %d, Location %q, body %s", rec.Code, rec.Header().Get("Location"), rec.Body)
	}

	body := rec.Body.String()

	pm, cm := pendingRe.FindStringSubmatch(body), csrfRe.FindStringSubmatch(body)
	if pm == nil || cm == nil {
		e.t.Fatalf("consent page lacks hidden fields: %s", body)
	}

	var cookie *http.Cookie

	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookieName {
			cookie = c
		}
	}

	if cookie == nil {
		e.t.Fatal("no CSRF cookie set")
	}

	return consentPage{pending: pm[1], csrf: cm[1], cookie: cookie, body: body}
}

// approveLocation posts the consent form and returns the Trakt consent URL
// the browser is sent to.
func (e *env) approveLocation(cp consentPage) *url.URL {
	e.t.Helper()

	rec := e.postForm("/oauth/consent", url.Values{fieldPending: {cp.pending}, fieldCSRF: {cp.csrf}, fieldAction: {actionApprove}}, cp.cookie)
	if rec.Code != http.StatusSeeOther {
		e.t.Fatalf("consent status = %d, body %s", rec.Code, rec.Body)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		e.t.Fatalf("parse Location: %v", err)
	}

	if loc.Host != testTraktAuthHost || loc.Path != testTraktAuthPath {
		e.t.Fatalf("consent redirect = %s", loc)
	}

	return loc
}

// approve posts the consent form and returns the state sent to Trakt.
func (e *env) approve(cp consentPage) string {
	e.t.Helper()
	return e.approveLocation(cp).Query().Get(paramState)
}

func (e *env) callback(state string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return e.get(pathCallback+"?"+url.Values{paramState: {state}, paramCode: {fakeTraktCode}}.Encode(), cookie)
}

// authCode runs the browser part of the flow and returns our authorization code.
func (e *env) authCode(p pkce) string {
	e.t.Helper()
	cp := e.authorize(authorizeParams(p))

	rec := e.callback(e.approve(cp), cp.cookie)
	if rec.Code != http.StatusSeeOther {
		e.t.Fatalf("callback status = %d, body %s", rec.Code, rec.Body)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		e.t.Fatalf("parse Location: %v", err)
	}

	q := loc.Query()
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != testRedirect {
		e.t.Fatalf("redirect = %s", loc)
	}

	if q.Get("iss") != testBaseURL || q.Get(paramState) != testState || q.Get(paramCode) == "" {
		e.t.Fatalf("redirect query = %v", q)
	}

	return q.Get(paramCode)
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
}

func (e *env) token(form url.Values) (int, tokenResponse) {
	e.t.Helper()

	rec := e.postForm("/oauth/token", form)
	if cc := rec.Header().Get("Cache-Control"); cc != cacheControlNoStore {
		e.t.Errorf("token Cache-Control = %q", cc)
	}

	var tr tokenResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &tr); err != nil {
		e.t.Fatalf("token body %q: %v", rec.Body, err)
	}

	return rec.Code, tr
}

func (e *env) exchange(p pkce) tokenResponse {
	e.t.Helper()

	code, tr := e.token(url.Values{
		paramGrantType: {grantAuthorizationCode}, paramCode: {e.authCode(p)}, "redirect_uri": {testRedirect},
		paramClientID: {testClientID}, "code_verifier": {p.verifier},
	})
	if code != http.StatusOK || tr.AccessToken == "" || tr.RefreshToken == "" {
		e.t.Fatalf("token exchange: status %d, %+v", code, tr)
	}

	return tr
}

func (e *env) refresh(refreshToken string) (int, tokenResponse) {
	e.t.Helper()
	return e.token(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, paramClientID: {testClientID}})
}

func (e *env) verify(srv *Server, token string) (*auth.TokenInfo, error) {
	return srv.Verifier()(context.Background(), token, httptest.NewRequestWithContext(e.t.Context(), http.MethodPost, "/mcp", nil))
}

// assertNoUser fails when the fake Trakt account was stored.
func (e *env) assertNoUser() {
	e.t.Helper()

	if _, err := e.st.UserBySlugOrTraktID(context.Background(), fakeTraktIDStr); !errors.Is(err, store.ErrNotFound) {
		e.t.Errorf("user stored: %v", err)
	}
}

// errorRedirect asserts rec redirects to the client with the given OAuth error.
func errorRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantErr string) {
	t.Helper()

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}

	if !strings.HasPrefix(loc.String(), testRedirect+"?") {
		t.Fatalf("redirect = %s", loc)
	}

	if q := loc.Query(); q.Get("error") != wantErr || q.Get("iss") != testBaseURL || q.Get(paramState) != testState {
		t.Fatalf("redirect query = %v, want error %s with iss and state", q, wantErr)
	}
}

// errorPage asserts rec is an HTML error page that does not redirect.
func errorPage(t *testing.T, rec *httptest.ResponseRecorder, status int, want ...string) string {
	t.Helper()

	if rec.Code != status {
		t.Fatalf("status = %d, want %d, body %s", rec.Code, status, rec.Body)
	}

	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("error page redirects to %s", loc)
	}

	body := rec.Body.String()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q:\n%s", w, body)
		}
	}

	return body
}

func TestMetadata_Fields(t *testing.T) {
	t.Parallel()

	for _, dcr := range []bool{true, false} {
		e := newEnv(t, func(c *config.Config) { c.EnableDCR = dcr })

		rec := e.get("/.well-known/oauth-authorization-server")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d", rec.Code)
		}

		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q", cc)
		}

		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}

		want := map[string]any{
			"issuer":                                         testBaseURL,
			"authorization_endpoint":                         testBaseURL + "/oauth/authorize",
			"token_endpoint":                                 testBaseURL + "/oauth/token",
			"revocation_endpoint":                            testBaseURL + "/oauth/revoke",
			"response_types_supported":                       []any{paramCode},
			"grant_types_supported":                          []any{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported":               []any{"S256"},
			"token_endpoint_auth_methods_supported":          []any{authMethodNone},
			"revocation_endpoint_auth_methods_supported":     []any{"none"},
			"client_id_metadata_document_supported":          true,
			"authorization_response_iss_parameter_supported": true,
			"scopes_supported":                               []any{MCPScope},
		}
		if dcr {
			want["registration_endpoint"] = testBaseURL + "/oauth/register"
		}

		if len(got) != len(want) {
			t.Errorf("dcr=%v: keys = %v, want %v", dcr, got, want)
		}

		for k, v := range want {
			gb, _ := json.Marshal(got[k])

			wb, _ := json.Marshal(v)
			if string(gb) != string(wb) {
				t.Errorf("dcr=%v: %s = %s, want %s", dcr, k, gb, wb)
			}
		}

		reg := e.postForm("/oauth/register", url.Values{})
		if !dcr && reg.Code != http.StatusNotFound {
			t.Errorf("register with DCR off: status %d, want 404", reg.Code)
		}
	}
}

func TestProtectedResourceMetadata(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	rec := e.get("/.well-known/oauth-protected-resource")

	var got struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}

	if got.Resource != testResource || len(got.AuthorizationServers) != 1 || got.AuthorizationServers[0] != testBaseURL ||
		len(got.ScopesSupported) != 1 || got.ScopesSupported[0] != MCPScope {
		t.Errorf("metadata = %+v", got)
	}

	if u := e.srv.ResourceMetadataURL(); u != testBaseURL+"/.well-known/oauth-protected-resource" {
		t.Errorf("ResourceMetadataURL = %q", u)
	}
}

func TestFlow_HappyPath(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	tr := e.exchange(newPKCE())
	if tr.TokenType != "bearer" && tr.TokenType != "Bearer" {
		t.Errorf("token_type = %q", tr.TokenType)
	}

	if tr.Scope != MCPScope {
		t.Errorf("scope = %q", tr.Scope)
	}

	ti, err := e.verify(e.srv, tr.AccessToken)
	if err != nil {
		t.Fatalf("Verifier: %v", err)
	}

	u, err := e.st.UserBySlugOrTraktID(context.Background(), fakeTraktIDStr)
	if err != nil {
		t.Fatalf("user not stored: %v", err)
	}

	assertTokenInfo(t, ti, u.ID)

	if _, err := e.verify(e.srv, "not-a-token"); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("garbage token err = %v, want ErrInvalidToken", err)
	}
}

func TestCallbackUpsertsUserBySlugAndTraktID(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.exchange(newPKCE())

	u, err := e.st.UserBySlugOrTraktID(context.Background(), fakeTraktIDStr)
	if err != nil {
		t.Fatalf("user by trakt id: %v", err)
	}

	if u.TraktID != fakeTraktIDStr || u.TraktSlug != fakeTraktSlug {
		t.Errorf("user = %+v", u)
	}

	bySlug, err := e.st.UserBySlugOrTraktID(context.Background(), fakeTraktSlug)
	if err != nil || bySlug.ID != u.ID {
		t.Fatalf("user by slug = %+v, %v", bySlug, err)
	}

	// A renamed account keeps our user id and gets its new slug.
	const renamed = "ada-byron"

	e.trakt.set(func(f *fakeTrakt) { f.slug = renamed })
	e.exchange(newPKCE())

	again, err := e.st.UserBySlugOrTraktID(context.Background(), fakeTraktIDStr)
	if err != nil || again.ID != u.ID || again.TraktSlug != renamed {
		t.Errorf("after rename: %+v, %v; want id %s slug %s", again, err, u.ID, renamed)
	}
}

func TestCallbackStoresTraktToken(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.exchange(newPKCE())

	u, err := e.st.UserBySlugOrTraktID(context.Background(), fakeTraktIDStr)
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	tok, err := e.st.TraktToken(context.Background(), u.ID)
	if err != nil {
		t.Fatalf("TraktToken: %v", err)
	}

	if tok.AccessToken != fakeTraktAccess || tok.RefreshToken != fakeTraktRefresh || tok.Expiry.IsZero() {
		t.Errorf("trakt token = %+v", tok)
	}
}

func TestConsentRedirectsToTraktWithS256(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cp := e.authorize(authorizeParams(newPKCE()))
	loc := e.approveLocation(cp)
	q := loc.Query()

	if q.Get("response_type") != paramCode || q.Get("client_id") != testTraktClientID ||
		q.Get("redirect_uri") != testBaseURL+"/oauth/trakt/callback" || q.Get(paramState) == "" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
		t.Fatalf("trakt consent URL = %s", loc)
	}

	// The state toward Trakt is a fresh pending id, never the consent page's.
	if q.Get(paramState) == cp.pending {
		t.Error("state toward Trakt reuses the consent pending id")
	}

	if rec := e.callback(q.Get(paramState), cp.cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, body %s", rec.Code, rec.Body)
	}

	// The verifier sent at exchange is the one behind the challenge.
	got := e.trakt.receivedVerifiers()
	if len(got) != 1 || s256(got[0]) != q.Get("code_challenge") {
		t.Errorf("verifiers = %v, want one matching challenge %s", got, q.Get("code_challenge"))
	}
}

func TestCallbackExchangeFailureShowsErrorPage(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusInternalServerError} {
		e := newEnv(t)
		e.trakt.set(func(f *fakeTrakt) { f.tokenStatus = status })
		cp := e.authorize(authorizeParams(newPKCE()))

		body := errorPage(t, e.callback(e.approve(cp), cp.cookie), http.StatusBadGateway, "Trakt sign-in failed")
		if strings.Contains(body, fakeUpstreamBody) {
			t.Errorf("status %d: page shows the upstream body: %s", status, body)
		}

		e.assertNoUser()
	}
}

func TestCallbackProfileFailureShowsErrorPage(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		e := newEnv(t)
		e.trakt.set(func(f *fakeTrakt) { f.settingsStatus = status })
		cp := e.authorize(authorizeParams(newPKCE()))

		body := errorPage(t, e.callback(e.approve(cp), cp.cookie), http.StatusBadGateway, "Trakt sign-in failed")
		if strings.Contains(body, fakeUpstreamBody) {
			t.Errorf("status %d: page shows the upstream body: %s", status, body)
		}

		e.assertNoUser()
	}

	// A profile without a slug is refused too.
	e := newEnv(t)
	e.trakt.set(func(f *fakeTrakt) { f.slug = "" })
	cp := e.authorize(authorizeParams(newPKCE()))
	errorPage(t, e.callback(e.approve(cp), cp.cookie), http.StatusBadGateway, "Trakt sign-in failed")
}

func TestCallback_AccessDeniedShowsRetryPage(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cp := e.authorize(authorizeParams(newPKCE()))
	state := e.approve(cp)
	// A usable code next to the error must not be exchanged: the error wins.
	rec := e.get(pathCallback+"?"+url.Values{paramState: {state}, "error": {"access_denied"}, paramCode: {fakeTraktCode}}.Encode(), cp.cookie)

	body := errorPage(t, rec, http.StatusForbidden, "Trakt access not granted")
	if !strings.Contains(body, `href="`+testBaseURL+`/oauth/authorize?`) {
		t.Errorf("no retry link: %s", body)
	}

	if strings.Contains(body, "code=") {
		t.Errorf("page carries an authorization code: %s", body)
	}

	e.assertNoUser()
}

func TestCallback_OtherUpstreamErrorShowsGenericPage(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cp := e.authorize(authorizeParams(newPKCE()))
	state := e.approve(cp)
	rec := e.get(pathCallback+"?"+url.Values{paramState: {state}, "error": {"server_error"}}.Encode(), cp.cookie)
	errorPage(t, rec, http.StatusBadGateway, "Trakt sign-in failed")
	e.assertNoUser()
}

func TestCallback_WrongState(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cp := e.authorize(authorizeParams(newPKCE()))
	e.approve(cp)

	for _, state := range []string{"", "forged-state", cp.pending} {
		rec := e.get(pathCallback+"?"+url.Values{paramState: {state}, paramCode: {fakeTraktCode}}.Encode(), cp.cookie)
		errorPage(t, rec, http.StatusBadRequest, "expired")
	}

	if got := e.trakt.receivedVerifiers(); len(got) != 0 {
		t.Errorf("code exchanged for an unvalidated state: %v", got)
	}

	e.assertNoUser()
}

func TestAuthorizeRejectsContactsScope(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	for _, scope := range []string{"contacts", "trakt contacts", "openid"} {
		params := authorizeParams(newPKCE())
		params.Set("scope", scope)
		errorRedirect(t, e.get("/oauth/authorize?"+params.Encode()), "invalid_scope")
	}
}

// assertTokenInfo checks the verified token info of the happy path: user id,
// roughly one hour of lifetime, and no user id on a bare context.
func assertTokenInfo(t *testing.T, ti *auth.TokenInfo, wantUID string) {
	t.Helper()

	if ti.UserID != wantUID || ti.Extra["uid"] != wantUID {
		t.Errorf("token info = %+v, want uid %s", ti, wantUID)
	}

	if ti.Expiration.Before(time.Now().Add(50*time.Minute)) || ti.Expiration.After(time.Now().Add(61*time.Minute)) {
		t.Errorf("expiration = %v, want about 1 h", ti.Expiration)
	}

	if _, ok := UserID(context.Background()); ok {
		t.Error("UserID without token info reported ok")
	}
}

func TestUserID_FromTokenInfo(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	tr := e.exchange(newPKCE())

	var (
		gotUID string
		gotOK  bool
	)

	h := auth.RequireBearerToken(e.srv.Verifier(), nil)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		gotUID, gotOK = UserID(r.Context())
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tr.AccessToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !gotOK || gotUID == "" {
		t.Errorf("status %d, uid %q, ok %v", rec.Code, gotUID, gotOK)
	}
}

func TestFlow_LoopbackLocalhostAnyPort(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	params := authorizeParams(newPKCE())
	params.Set("redirect_uri", "http://localhost:51234/callback")
	cp := e.authorize(params)

	rec := e.callback(e.approve(cp), cp.cookie)
	if loc := rec.Header().Get("Location"); !strings.HasPrefix(loc, "http://localhost:51234/callback?") {
		t.Errorf("redirect = %q, status %d", loc, rec.Code)
	}
}

func TestFlow_RefreshRotationAndReuse(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	tr1 := e.exchange(newPKCE())

	code, tr2 := e.refresh(tr1.RefreshToken)
	if code != http.StatusOK || tr2.RefreshToken == "" || tr2.RefreshToken == tr1.RefreshToken {
		t.Fatalf("first refresh: status %d, %+v", code, tr2)
	}

	if tr2.Scope != MCPScope {
		t.Errorf("refreshed scope = %q", tr2.Scope)
	}

	if _, err := e.verify(e.srv, tr2.AccessToken); err != nil {
		t.Fatalf("rotated access token rejected: %v", err)
	}

	if _, err := e.verify(e.srv, tr1.AccessToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Error("access token of the rotated refresh token still valid")
	}

	code, tr := e.refresh(tr1.RefreshToken)
	if code != http.StatusBadRequest || tr.Error != "invalid_grant" {
		t.Fatalf("reuse: status %d, %+v; want 400 invalid_grant", code, tr)
	}

	if code, tr := e.refresh(tr2.RefreshToken); code == http.StatusOK {
		t.Errorf("new refresh token survived reuse: %+v", tr)
	}

	if _, err := e.verify(e.srv, tr2.AccessToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Error("new access token survived reuse")
	}

	// One code exchange and one successful refresh; refused ones are not grants.
	assertOneGrantOfEachKind(t, e.reg)
}

func assertOneGrantOfEachKind(t *testing.T, reg *prometheus.Registry) {
	t.Helper()

	want := map[string]float64{grantAuthorizationCode: 1, grantRefreshToken: 1}
	if got := grantCounts(t, reg); !maps.Equal(got, want) {
		t.Errorf("grant metrics = %v, want %v", got, want)
	}
}

// grantsMetricSuffix ends the grant counter's name; the namespace prefix
// belongs to the obs package.
const grantsMetricSuffix = "_oauth_grants_total"

// grantCounts returns the grant counter's values by kind label.
func grantCounts(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}

	out := map[string]float64{}

	for _, mf := range mfs {
		if !strings.HasSuffix(mf.GetName(), grantsMetricSuffix) {
			continue
		}

		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "kind" {
					out[l.GetValue()] = m.GetCounter().GetValue()
				}
			}
		}
	}

	return out
}

func TestFlow_PKCERequired(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	params := authorizeParams(newPKCE())
	params.Del("code_challenge")
	params.Del("code_challenge_method")
	errorRedirect(t, e.get("/oauth/authorize?"+params.Encode()), "invalid_request")
}

func TestFlow_PlainPKCERejected(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	params := authorizeParams(newPKCE())
	params.Set("code_challenge", strings.Repeat("a", 43))
	params.Set("code_challenge_method", "plain")
	errorRedirect(t, e.get("/oauth/authorize?"+params.Encode()), "invalid_request")
}

func TestFlow_WrongPKCEVerifier(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	code, tr := e.token(url.Values{
		"grant_type": {"authorization_code"}, "code": {e.authCode(newPKCE())}, "redirect_uri": {testRedirect},
		"client_id": {testClientID}, "code_verifier": {newPKCE().verifier},
	})
	if code == http.StatusOK {
		t.Fatalf("wrong verifier accepted: %+v", tr)
	}
}

func TestFlow_WrongResource(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	for _, res := range [][]string{{"https://evil.example.com/mcp"}, {testBaseURL}, {testResource, "https://evil.example.com/mcp"}} {
		params := authorizeParams(newPKCE())
		params["resource"] = res
		errorRedirect(t, e.get("/oauth/authorize?"+params.Encode()), "invalid_target")
	}
}

func TestFlow_NoResourceGrantsAudience(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	p := newPKCE()
	params := authorizeParams(p)
	params.Del("resource")
	params.Del("scope")
	cp := e.authorize(params)
	rec := e.callback(e.approve(cp), cp.cookie)

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}

	status, tr := e.token(url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")}, "redirect_uri": {testRedirect},
		"client_id": {testClientID}, "code_verifier": {p.verifier},
	})
	if status != http.StatusOK {
		t.Fatalf("token: %d %+v", status, tr)
	}

	if _, err := e.verify(e.srv, tr.AccessToken); err != nil {
		t.Errorf("Verifier: %v", err)
	}
}

func TestFlow_UnsafeRedirectRejected(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	if err := e.st.SaveClient(context.Background(), &store.OAuthClient{
		ID: "bad-client", Kind: store.ClientKindDCR, Name: "Bad",
		RedirectURIs: []string{"javascript:alert(1)", "http://evil.example.com/cb", "data:text/html,x", "myapp://evil.example.com/cb"},
	}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	for _, ru := range []string{
		"javascript:alert(1)", "http://evil.example.com/cb", "https://unregistered.example.com/cb",
		"data:text/html,x", "myapp://evil.example.com/cb", "com.unregistered.app:/cb",
	} {
		params := authorizeParams(newPKCE())
		params.Set("client_id", "bad-client")
		params.Set("redirect_uri", ru)

		body := errorPage(t, e.get("/oauth/authorize?"+params.Encode()), http.StatusBadRequest)
		if strings.Contains(body, "javascript:") || strings.Contains(body, "evil.example.com") {
			t.Errorf("error page echoes the redirect URI: %s", body)
		}
	}
}

// TestFlow_PrivateUseSchemeRedirect runs the browser leg for a native client
// that registered a reverse-domain scheme: the consent page names the scheme
// and our authorization code lands on the registered private-use URI.
func TestFlow_PrivateUseSchemeRedirect(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	if err := e.st.SaveClient(context.Background(), &store.OAuthClient{
		ID: "native-client", Kind: store.ClientKindDCR, Name: "Native",
		RedirectURIs: []string{testRedirectNative},
	}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	params := authorizeParams(newPKCE())
	params.Set("client_id", "native-client")
	params.Set("redirect_uri", testRedirectNative)

	cp := e.authorize(params)
	if !strings.Contains(cp.body, "<strong>com.example.app</strong>") {
		t.Errorf("consent page does not name the scheme: %s", cp.body)
	}

	rec := e.callback(e.approve(cp), cp.cookie)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, body %s", rec.Code, rec.Body)
	}

	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}

	q := loc.Query()
	if loc.Scheme != "com.example.app" || loc.Path != "/callback" || q.Get(paramCode) == "" ||
		q.Get(paramState) != testState || q.Get("iss") != testBaseURL {
		t.Errorf("redirect = %s", loc)
	}
}

func TestFlow_FormPostRejected(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	params := authorizeParams(newPKCE())
	params.Set("response_mode", "form_post")
	errorPage(t, e.get("/oauth/authorize?"+params.Encode()), http.StatusBadRequest)
}

func TestFlow_UnknownClient(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	params := authorizeParams(newPKCE())
	params.Set("client_id", "nope")
	errorPage(t, e.get("/oauth/authorize?"+params.Encode()), http.StatusBadRequest)
}

func TestFlow_Deny(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cp := e.authorize(authorizeParams(newPKCE()))
	rec := e.postForm("/oauth/consent", url.Values{fieldPending: {cp.pending}, fieldCSRF: {cp.csrf}, fieldAction: {"deny"}}, cp.cookie)
	errorRedirect(t, rec, "access_denied")
}

func TestFlow_CSRFMismatch(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cp := e.authorize(authorizeParams(newPKCE()))
	errorPage(t, e.postForm("/oauth/consent", url.Values{"pending": {cp.pending}, "csrf": {"forged"}, "action": {"approve"}}, cp.cookie),
		http.StatusBadRequest)

	// Cookie missing: the form token alone is not enough.
	cp = e.authorize(authorizeParams(newPKCE()))
	errorPage(t, e.postForm("/oauth/consent", url.Values{"pending": {cp.pending}, "csrf": {cp.csrf}, "action": {"approve"}}),
		http.StatusBadRequest)

	// Callback from another browser (no cookie) is refused.
	cp = e.authorize(authorizeParams(newPKCE()))
	errorPage(t, e.callback(e.approve(cp), nil), http.StatusBadRequest)
}

func TestFlow_PendingExpired(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	cp := e.authorize(authorizeParams(newPKCE()))
	e.advance(11 * time.Minute)
	errorPage(t, e.postForm("/oauth/consent", url.Values{"pending": {cp.pending}, "csrf": {cp.csrf}, "action": {"approve"}}, cp.cookie),
		http.StatusBadRequest, "expired")

	errorPage(t, e.callback("unknown-state", cp.cookie), http.StatusBadRequest, "expired")

	// A callback arriving after the pending request's deadline is refused.
	e.now.Store(nil)
	cp = e.authorize(authorizeParams(newPKCE()))
	state := e.approve(cp)
	e.advance(11 * time.Minute)
	errorPage(t, e.callback(state, cp.cookie), http.StatusBadRequest, "expired")
	e.assertNoUser()

	// A callback replay finds nothing.
	e.now.Store(nil)
	cp = e.authorize(authorizeParams(newPKCE()))

	state = e.approve(cp)
	if rec := e.callback(state, cp.cookie); rec.Code != http.StatusSeeOther {
		t.Fatalf("first callback: %d", rec.Code)
	}

	errorPage(t, e.callback(state, cp.cookie), http.StatusBadRequest, "expired")
}

func TestConsentPage_TextAndEscaping(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	cp := e.authorize(authorizeParams(newPKCE()))
	if !strings.Contains(cp.body, "Claude wants to access your Trakt account. You will be sent back to <strong>claude.example.com</strong>.") {
		t.Errorf("consent text missing: %s", cp.body)
	}

	if err := e.st.SaveClient(context.Background(), &store.OAuthClient{
		ID: testClientID, Kind: store.ClientKindDCR, Name: "<script>x</script>",
		RedirectURIs: []string{testRedirect},
	}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	cp = e.authorize(authorizeParams(newPKCE()))
	if strings.Contains(cp.body, "<script>x") {
		t.Errorf("client name not escaped: %s", cp.body)
	}

	if !strings.Contains(cp.body, "&lt;script&gt;x&lt;/script&gt;") {
		t.Errorf("escaped client name missing: %s", cp.body)
	}

	if !strings.Contains(cp.body, "<strong>claude.example.com</strong>") {
		t.Errorf("redirect host not in <strong>: %s", cp.body)
	}

	if strings.Contains(cp.body, testRedirect) {
		t.Errorf("consent page renders the full redirect URI: %s", cp.body)
	}

	c := cp.cookie
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 600 {
		t.Errorf("cookie = %+v", c)
	}
}

func TestConsentCookie_SecureOnLocalhost(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	_, e.mux = e.newServer("http://localhost:8080")
	params := authorizeParams(newPKCE())
	params.Set("resource", "http://localhost:8080/mcp")

	cp := e.authorize(params)
	if !cp.cookie.Secure {
		t.Error("cookie not Secure on http://localhost base URL")
	}
}

func TestRevoke_KeepsTraktToken(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	tr := e.exchange(newPKCE())

	rec := e.postForm("/oauth/revoke", url.Values{"token": {tr.RefreshToken}, "client_id": {testClientID}})
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d, body %s", rec.Code, rec.Body)
	}

	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("revoke Cache-Control = %q", cc)
	}

	if code, _ := e.refresh(tr.RefreshToken); code == http.StatusOK {
		t.Error("revoked refresh token still works")
	}

	if _, err := e.verify(e.srv, tr.AccessToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Error("access token of a revoked grant still valid")
	}

	if n := e.trakt.revokeHits.Load(); n != 0 {
		t.Errorf("Trakt revoke hits = %d, want 0", n)
	}

	u, err := e.st.UserBySlugOrTraktID(context.Background(), fakeTraktIDStr)
	if err != nil {
		t.Fatalf("user: %v", err)
	}

	if _, err := e.st.TraktToken(context.Background(), u.ID); err != nil {
		t.Errorf("Trakt token gone after revoke: %v", err)
	}
}

func TestToken_RejectsNonForm(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/oauth/token", strings.NewReader(`{"grant_type":"refresh_token"}`))
	req.Header.Set("Content-Type", "application/json")

	rec := e.do(req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "invalid_request") {
		t.Errorf("status %d, body %s", rec.Code, rec.Body)
	}
}

func TestVerifier_WrongAudience(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	tr := e.exchange(newPKCE())

	other, _ := e.newServer("https://other.example.com")
	if _, err := e.verify(other, tr.AccessToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("token for another audience: err = %v, want ErrInvalidToken", err)
	}

	if _, err := e.verify(e.srv, tr.RefreshToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("refresh token as bearer: err = %v, want ErrInvalidToken", err)
	}
}

func TestVerifier_StoreFailureIsNotInvalidToken(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	tr := e.exchange(newPKCE())
	if err := e.st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err := e.verify(e.srv, tr.AccessToken)
	if err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("err = %v, want a non-invalid-token error", err)
	}

	if strings.Contains(err.Error(), tr.AccessToken) || strings.Contains(err.Error(), "sql") {
		t.Errorf("error leaks details to the client: %v", err)
	}
}

func TestVerifier_ExpiredAccessToken(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	// A negative lifespan issues access tokens that expired an hour ago.
	e.srv.fositeConfig.AccessTokenLifespan = -time.Hour

	tr := e.exchange(newPKCE())
	if _, err := e.verify(e.srv, tr.AccessToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("expired token: err = %v, want ErrInvalidToken", err)
	}
}

func TestAuthorize_PendingBudgetSpent(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.srv.pendingLimit = newBucket(rate.Every(time.Hour), 1)
	e.authorize(authorizeParams(newPKCE()))
	rec := e.get("/oauth/authorize?" + authorizeParams(newPKCE()).Encode())
	errorPage(t, rec, http.StatusTooManyRequests, "Too many sign-ins")

	if rec.Header().Get("Retry-After") == "" {
		t.Error("429 page has no Retry-After")
	}

	if len(rec.Result().Cookies()) != 0 {
		t.Error("refused authorize still set a cookie")
	}
}

func TestAuthorize_CIMDBudgetSpent(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	e.srv.clients.cimdLimit = newBucket(rate.Every(time.Hour), 0)
	params := authorizeParams(newPKCE())
	params.Set("client_id", "https://unknown-client.example.com/client.json")
	errorPage(t, e.get("/oauth/authorize?"+params.Encode()), http.StatusTooManyRequests, "Too many sign-ins")
}
