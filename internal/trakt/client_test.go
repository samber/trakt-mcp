package trakt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/samber/trakt-mcp/internal/trakt/api"
)

const (
	testAPIKey    = "test-client-id"
	testUserAgent = "trakt-mcp/test"
	firstToken    = "token-1"
	secondToken   = "token-2"
	// testPlaybackID is an arbitrary playback item id for the DELETE endpoint.
	testPlaybackID = 42
)

// fakeTokens is a TokenSource whose Rejected call hands out secondToken.
type fakeTokens struct {
	mu       sync.Mutex
	current  string
	rejected []string
	err      error
}

func newFakeTokens() *fakeTokens {
	return &fakeTokens{current: firstToken}
}

func (f *fakeTokens) AccessToken(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.current, nil
}

func (f *fakeTokens) Rejected(_ context.Context, rejected string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.rejected = append(f.rejected, rejected)
	if f.err != nil {
		return "", f.err
	}

	f.current = secondToken

	return f.current, nil
}

func (f *fakeTokens) rejectedTokens() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]string(nil), f.rejected...)
}

// newTestAPI starts handler on an httptest server and returns a generated
// client bound to it with a fresh fakeTokens.
func newTestAPI(t *testing.T, handler http.HandlerFunc) (*api.ClientWithResponses, *fakeTokens, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	ts := newFakeTokens()
	c := New(Config{BaseURL: srv.URL, APIKey: testAPIKey, UserAgent: testUserAgent})

	return c.For(ts).API(), ts, srv
}

func getTrending(ctx context.Context, c *api.ClientWithResponses) error {
	rsp, err := c.GetMoviesTrendingWithResponse(ctx, nil)
	if err != nil {
		return err
	}

	if rsp.StatusCode() != http.StatusOK {
		return fmt.Errorf("unexpected status %d", rsp.StatusCode())
	}

	return nil
}

// onlyReader hides bytes.Reader-like types so http.NewRequest cannot set
// GetBody, forcing the client to buffer the body itself.
type onlyReader struct{ io.Reader }

func postHistory(ctx context.Context, c *api.ClientWithResponses, body string) error {
	_, err := c.PostSyncHistoryAddWithBodyWithResponse(ctx, "application/json", onlyReader{strings.NewReader(body)})

	return err
}

func asError(t *testing.T, err error) *Error {
	t.Helper()

	var te *Error
	if !errors.As(err, &te) {
		t.Fatalf("error %v (%T) is not a *trakt.Error", err, err)
	}

	return te
}

func TestHeadersSent(t *testing.T) {
	t.Parallel()

	var got http.Header

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[]")
	})

	if err := getTrending(t.Context(), c); err != nil {
		t.Fatalf("call failed: %v", err)
	}

	want := map[string]string{
		"trakt-api-key":     testAPIKey,
		"trakt-api-version": "2",
		"Content-Type":      "application/json",
		"Authorization":     "Bearer " + firstToken,
		"User-Agent":        testUserAgent,
	}
	for k, v := range want {
		if g := got.Get(k); g != v {
			t.Errorf("header %s = %q, want %q", k, g, v)
		}
	}
}

func TestStatusMapping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status     int
		retryAfter string
		kind       Kind
		wantRetry  time.Duration
	}{
		{status: http.StatusUnauthorized, kind: KindUnauthorized},
		{status: http.StatusNotFound, kind: KindNotFound},
		{status: http.StatusConflict, kind: KindConflict},
		{status: statusAccountLimit, kind: KindAccountLimit},
		{status: http.StatusLocked, kind: KindLocked},
		{status: http.StatusUpgradeRequired, kind: KindVIPRequired},
		{status: http.StatusTooManyRequests, retryAfter: "7", kind: KindRateLimited, wantRetry: 7 * time.Second},
		{status: http.StatusInternalServerError, kind: KindServer},
		{status: http.StatusServiceUnavailable, kind: KindServer},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			t.Parallel()

			c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}

				w.WriteHeader(tt.status)
			})

			te := asError(t, getTrending(t.Context(), c))
			if te.Status != tt.status || te.Kind != tt.kind || te.RetryAfter != tt.wantRetry {
				t.Fatalf("got status=%d kind=%v retry=%v, want %d %v %v",
					te.Status, te.Kind, te.RetryAfter, tt.status, tt.kind, tt.wantRetry)
			}

			if te.Message == "" {
				t.Error("empty message")
			}
		})
	}
}

func TestRetryAfterParsed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	tests := map[string]struct {
		header string
		want   time.Duration
	}{
		"seconds":       {"30", 30 * time.Second},
		"http date":     {now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second},
		"past date":     {now.Add(-time.Minute).Format(http.TimeFormat), 0},
		"negative":      {"-5", 0},
		"huge negative": {"-9223372037", 0},
		"missing":       {"", 0},
		"garbage":       {"soon", 0},
		"zero":          {"0", 0},
		"fractional ok": {"1.5", 0},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := parseRetryAfter(tt.header, now); got != tt.want {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}

	// End to end through a 429 with an HTTP-date header.
	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	})

	te := asError(t, getTrending(t.Context(), c))
	if te.RetryAfter < 58*time.Minute || te.RetryAfter > time.Hour {
		t.Fatalf("RetryAfter = %v, want about 1h", te.RetryAfter)
	}
}

func TestRetryAfterAbsurdValueClamped(t *testing.T) {
	t.Parallel()

	now := time.Now()
	for _, h := range []string{"999999999", "99999999999999999999", now.AddDate(10, 0, 0).UTC().Format(http.TimeFormat)} {
		if got := parseRetryAfter(h, now); got != maxRetryAfter {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", h, got, maxRetryAfter)
		}
	}
}

func TestRetryOnceOn401WithRefresh(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	c, ts, _ := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if r.Header.Get("Authorization") != "Bearer "+secondToken {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[]")
	})

	if err := getTrending(t.Context(), c); err != nil {
		t.Fatalf("call failed: %v", err)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("server calls = %d, want 2", got)
	}

	if got := ts.rejectedTokens(); len(got) != 1 || got[0] != firstToken {
		t.Errorf("rejected tokens = %v, want [%s]", got, firstToken)
	}
}

func TestSecond401IsUpstreamAuth(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	c, ts, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})

	err := getTrending(t.Context(), c)
	if !errors.Is(err, ErrUpstreamAuth) {
		t.Fatalf("err = %v, want ErrUpstreamAuth", err)
	}

	if te := asError(t, err); te.Kind != KindUnauthorized {
		t.Errorf("kind = %v, want KindUnauthorized", te.Kind)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("server calls = %d, want 2", got)
	}

	if got := len(ts.rejectedTokens()); got != 1 {
		t.Errorf("refreshes = %d, want 1", got)
	}
}

func TestRefreshFailureIsReturned(t *testing.T) {
	t.Parallel()

	refreshErr := errors.New("refresh refused")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)

	ts := newFakeTokens()
	ts.err = refreshErr
	c := New(Config{BaseURL: srv.URL, APIKey: testAPIKey}).For(ts).API()

	if err := getTrending(t.Context(), c); !errors.Is(err, refreshErr) {
		t.Fatalf("err = %v, want the refresh error", err)
	}
}

func TestIdempotentGetRetriedOnceOn5xx(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)

			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "[]")
	})

	if err := getTrending(t.Context(), c); err != nil {
		t.Fatalf("call failed: %v", err)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("server calls = %d, want 2", got)
	}

	// A GET that keeps failing is retried only once.
	calls.Store(0)

	c2, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	if te := asError(t, getTrending(t.Context(), c2)); te.Kind != KindServer {
		t.Fatalf("kind = %v, want KindServer", te.Kind)
	}

	if got := calls.Load(); got != 2 {
		t.Errorf("server calls = %d, want 2", got)
	}
}

func TestWriteNotRetriedOn5xx(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	te := asError(t, postHistory(t.Context(), c, `{"movies":[]}`))
	if te.Kind != KindServer || te.Status != http.StatusInternalServerError {
		t.Fatalf("got kind=%v status=%d, want KindServer 500", te.Kind, te.Status)
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("POST server calls = %d, want 1", got)
	}

	calls.Store(0)

	if _, err := c.DeleteSyncPlaybackItemWithResponse(t.Context(), testPlaybackID); err == nil {
		t.Fatal("DELETE on 500 returned no error")
	}

	if got := calls.Load(); got != 1 {
		t.Errorf("DELETE server calls = %d, want 1", got)
	}
}

func TestWriteBodyReplayedOn401Retry(t *testing.T) {
	t.Parallel()

	const body = `{"movies":[{"ids":{"trakt":1}}]}`

	var (
		mu     sync.Mutex
		bodies []string
	)

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}

		mu.Lock()

		bodies = append(bodies, string(b))
		mu.Unlock()

		if r.Header.Get("Authorization") != "Bearer "+secondToken {
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "{}")
	})

	if err := postHistory(t.Context(), c, body); err != nil {
		t.Fatalf("call failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(bodies) != 2 || bodies[0] != body || bodies[1] != body {
		t.Fatalf("bodies = %q, want the same body twice", bodies)
	}
}

func TestErrorMessageNeverContainsBody(t *testing.T) {
	t.Parallel()

	const secret = "eyJhbGciOiJIUzI1NiJ9.secret-token-value" //nolint:gosec // G101: a fake token planted to prove it never leaks

	c, _, srv := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid","token":"`+secret+`"}`)
	})

	err := getTrending(t.Context(), c)
	te := asError(t, err)

	for _, s := range []string{err.Error(), te.Message} {
		if strings.Contains(s, secret) || strings.Contains(s, srv.URL) || strings.Contains(s, "/movies/trending") {
			t.Fatalf("error text leaks body or URL: %q", s)
		}
	}
}

func TestConflictBodyKeptForConflictOnly(t *testing.T) {
	t.Parallel()

	const body = `{"expires_at":"2026-11-24T13:00:00.000Z"}`

	tests := []struct {
		status int
		keep   bool
	}{
		{status: http.StatusConflict, keep: true},
		{status: http.StatusNotFound},
		{status: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.status), func(t *testing.T) {
			t.Parallel()

			c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, body)
			})

			te := asError(t, getTrending(t.Context(), c))

			got := te.ConflictBody()
			if tt.keep && string(got) != body {
				t.Fatalf("ConflictBody = %q, want %q", got, body)
			}

			if !tt.keep && got != nil {
				t.Fatalf("ConflictBody = %q for status %d, want nil", got, tt.status)
			}
		})
	}
}

func TestConflictBodyBoundedAndNeverInMessage(t *testing.T) {
	t.Parallel()

	const secret = "planted-secret-in-conflict-body"

	oversize := `{"secret":"` + secret + `","pad":"` + strings.Repeat("x", maxErrorBodyBytes) + `"}`

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, oversize)
	})

	err := getTrending(t.Context(), c)
	te := asError(t, err)

	if got := len(te.ConflictBody()); got != maxErrorBodyBytes {
		t.Fatalf("ConflictBody length = %d, want %d", got, maxErrorBodyBytes)
	}

	for _, s := range []string{err.Error(), te.Message} {
		if strings.Contains(s, secret) {
			t.Fatalf("error text leaks the conflict body: %q", s)
		}
	}

	// The accessor returns a copy: changing it does not change the error.
	te.ConflictBody()[0] = 'X'
	if te.ConflictBody()[0] != '{' {
		t.Fatal("ConflictBody exposes the internal slice")
	}
}

func TestErrorDescriptionTruncated(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", maxMessageRunes+50)

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(statusAccountLimit)
		_, _ = io.WriteString(w, `{"error":"limit","error_description":"`+long+`"}`)
	})

	te := asError(t, getTrending(t.Context(), c))
	if n := len([]rune(te.Message)); n != maxMessageRunes {
		t.Fatalf("message length = %d runes, want %d", n, maxMessageRunes)
	}

	if !strings.HasPrefix(long, te.Message) {
		t.Fatal("message is not a prefix of error_description")
	}
}

func TestUnreachableDoesNotLeakURL(t *testing.T) {
	t.Parallel()

	// A closed server gives "connection refused" on a known port.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	c := New(Config{BaseURL: url, APIKey: testAPIKey}).For(newFakeTokens()).API()

	err := getTrending(t.Context(), c)
	te := asError(t, err)

	if te.Kind != KindUnreachable {
		t.Fatalf("kind = %v, want KindUnreachable", te.Kind)
	}

	host := strings.TrimPrefix(url, "http://")
	for _, s := range []string{err.Error(), te.Message} {
		if strings.Contains(s, host) || strings.Contains(s, "/movies/trending") {
			t.Fatalf("error text leaks URL: %q", s)
		}
	}
}

func TestCanceledIsKindCanceled(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(50*time.Millisecond, cancel)

	err := getTrending(ctx, c)
	if te := asError(t, err); te.Kind != KindCanceled {
		t.Fatalf("kind = %v, want KindCanceled", te.Kind)
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is context.Canceled", err)
	}
}

func TestSlowResponseTimesOut(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	c := New(Config{
		BaseURL: srv.URL,
		APIKey:  testAPIKey,
		HTTP:    &http.Client{Timeout: 50 * time.Millisecond},
	}).For(newFakeTokens()).API()

	start := time.Now()

	te := asError(t, getTrending(t.Context(), c))
	if te.Kind != KindUnreachable {
		t.Fatalf("kind = %v, want KindUnreachable", te.Kind)
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("call took %v, timeout not applied", elapsed)
	}
}

func TestSecond401KeepsFixedMessage(t *testing.T) {
	t.Parallel()

	c, _, _ := newTestAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":"invalid_token","error_description":"upstream wording"}`)
	})

	err := getTrending(t.Context(), c)
	if !errors.Is(err, ErrUpstreamAuth) {
		t.Fatalf("err = %v, want ErrUpstreamAuth", err)
	}

	if te := asError(t, err); te.Message != revokedMessage {
		t.Fatalf("Message = %q, want %q", te.Message, revokedMessage)
	}
}

func TestRedirectIsErrorAndNotFollowed(t *testing.T) {
	t.Parallel()

	var targetCalls atomic.Int32

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)

	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			c, _, _ := newTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/elsewhere", status)
			})

			te := asError(t, postHistory(t.Context(), c, `{"movies":[]}`))
			if te.Status != status {
				t.Fatalf("status = %d, want %d", te.Status, status)
			}

			te = asError(t, getTrending(t.Context(), c))
			if te.Status != status {
				t.Fatalf("GET status = %d, want %d", te.Status, status)
			}
		})
	}

	// A caller-supplied client gets the same no-follow policy.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	custom := &http.Client{Timeout: time.Second}

	c := New(Config{BaseURL: srv.URL, APIKey: testAPIKey, HTTP: custom}).For(newFakeTokens()).API()
	if te := asError(t, getTrending(t.Context(), c)); te.Status != http.StatusFound {
		t.Fatalf("custom client status = %d, want %d", te.Status, http.StatusFound)
	}

	if custom.CheckRedirect != nil {
		t.Error("New mutated the caller's http.Client")
	}

	if got := targetCalls.Load(); got != 0 {
		t.Fatalf("redirect target called %d times, want 0", got)
	}
}

func TestDefaultHTTPClientHasTimeout(t *testing.T) {
	t.Parallel()

	c := New(Config{APIKey: testAPIKey})
	if c.http.Timeout != defaultHTTPTimeout {
		t.Fatalf("default timeout = %v, want %v", c.http.Timeout, defaultHTTPTimeout)
	}

	if c.baseURL != defaultBaseURL+"/" {
		t.Fatalf("default base URL = %q", c.baseURL)
	}
}

// TestCallerClientWithoutTimeoutGetsDefault pins that a supplied client with
// no Timeout is bounded too, without changing the caller's own client.
func TestCallerClientWithoutTimeoutGetsDefault(t *testing.T) {
	t.Parallel()

	own := &http.Client{}

	if got := New(Config{APIKey: testAPIKey, HTTP: own}).http.Timeout; got != defaultHTTPTimeout {
		t.Errorf("client timeout = %v, want %v", got, defaultHTTPTimeout)
	}

	if got := newUpstream(UpstreamConfig{HTTP: own}, nil).http.Timeout; got != defaultHTTPTimeout {
		t.Errorf("upstream timeout = %v, want %v", got, defaultHTTPTimeout)
	}

	if own.Timeout != 0 {
		t.Errorf("caller's client timeout = %v, want 0 (not mutated)", own.Timeout)
	}

	const custom = 5 * time.Second

	if got := New(Config{APIKey: testAPIKey, HTTP: &http.Client{Timeout: custom}}).http.Timeout; got != custom {
		t.Errorf("custom client timeout = %v, want %v", got, custom)
	}
}
