package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/samber/trakt-mcp/internal/crypto"
	"github.com/samber/trakt-mcp/internal/store"
	"github.com/samber/trakt-mcp/internal/trakt"
)

const (
	testKey = "A:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	// waitTimeout bounds every poll and shutdown wait in these tests.
	waitTimeout = 5 * time.Second
	pollEvery   = 10 * time.Millisecond
)

// fakeRevoke is Trakt's revoke endpoint; it records every revoked token.
type fakeRevoke struct {
	mu     sync.Mutex
	tokens []string
	status int
	srv    *httptest.Server
}

func newFakeRevoke(t *testing.T, status int) *fakeRevoke {
	t.Helper()

	f := &fakeRevoke{status: status}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}

		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		f.mu.Lock()
		f.tokens = append(f.tokens, body.Token)
		f.mu.Unlock()
		w.WriteHeader(f.status)
	}))
	t.Cleanup(f.srv.Close)

	return f
}

func (f *fakeRevoke) revoked() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.tokens)
}

type fixture struct {
	st *store.Store
	up *trakt.Upstream
	// withToken has a stored Trakt grant, withoutToken has none.
	withToken, withoutToken store.User
}

func newFixture(t *testing.T, rv *fakeRevoke) *fixture {
	t.Helper()

	ctx := context.Background()

	kr, err := crypto.ParseKeyring(testKey)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}

	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "db.sqlite"), kr)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	a, err := st.UpsertUser(ctx, "1001", "user-a")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := st.SaveTraktToken(ctx, a.ID, store.TraktToken{RefreshToken: "refresh-a", AccessToken: "access-a"}); err != nil {
		t.Fatalf("SaveTraktToken: %v", err)
	}

	b, err := st.UpsertUser(ctx, "1002", "user-b")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := st.SaveClient(ctx, &store.OAuthClient{
		ID: "client-1", Kind: store.ClientKindDCR, Name: "Old", RedirectURIs: []string{"https://app.example.com/cb"},
	}); err != nil {
		t.Fatalf("SaveClient: %v", err)
	}

	up := trakt.NewUpstream(trakt.UpstreamConfig{RevokeURL: rv.srv.URL, HTTP: rv.srv.Client()}, st)

	return &fixture{st: st, up: up, withToken: a, withoutToken: b}
}

// inactiveClock is past the purge window for users and clients created now.
func inactiveClock() func() time.Time {
	at := time.Now().Add(inactivityWindow + 24*time.Hour)
	return func() time.Time { return at }
}

// startRun runs Run in the background and returns a stop function that
// cancels it and fails the test if Run does not return.
func startRun(t *testing.T, f *fixture, now func() time.Time) (stop func()) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)

		Run(ctx, f.st, f.up, now)
	}()

	return func() {
		cancel()

		select {
		case <-done:
		case <-time.After(waitTimeout):
			t.Fatal("Run did not stop after cancel")
		}
	}
}

// waitDeleted polls until every user is gone.
func waitDeleted(t *testing.T, st *store.Store, users ...store.User) {
	t.Helper()

	deadline := time.Now().Add(waitTimeout)

	for {
		remaining := 0

		for _, u := range users {
			_, err := st.UserBySlugOrTraktID(context.Background(), u.TraktID)
			switch {
			case errors.Is(err, store.ErrNotFound):
			case err != nil:
				t.Fatalf("UserBySlugOrTraktID: %v", err)
			default:
				remaining++
			}
		}

		if remaining == 0 {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("%d users still present", remaining)
		}

		time.Sleep(pollEvery)
	}
}

// waitFor polls cond until it holds, failing the test after waitTimeout.
// Run purges users first, then revokes their grants, then purges clients:
// stopping the run loop before the last step of interest lets its cancelled
// context abort that step.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(waitTimeout)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(pollEvery)
	}
}

// waitClientGone polls until the client no longer exists.
func waitClientGone(t *testing.T, st *store.Store, clientID string) {
	t.Helper()

	waitFor(t, "client "+clientID+" to be purged", func() bool {
		_, err := st.ClientByID(context.Background(), clientID)

		switch {
		case errors.Is(err, store.ErrNotFound):
			return true
		case err != nil:
			t.Fatalf("ClientByID: %v", err)
		}

		return false
	})
}

func TestJobs_PurgesInactiveAndRevokes(t *testing.T) {
	t.Parallel()

	rv := newFakeRevoke(t, http.StatusOK)
	f := newFixture(t, rv)
	stop := startRun(t, f, inactiveClock())
	waitDeleted(t, f.st, f.withToken, f.withoutToken)
	waitClientGone(t, f.st, "client-1")
	stop()

	if got := rv.revoked(); !slices.Equal(got, []string{"refresh-a"}) {
		t.Errorf("revoked = %q, want exactly [refresh-a]", got)
	}

	if _, err := f.st.TraktToken(context.Background(), f.withToken.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("TraktToken after purge: err = %v, want ErrNotFound", err)
	}

	if _, err := f.st.ClientByID(context.Background(), "client-1"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("ClientByID after purge: err = %v, want ErrNotFound", err)
	}
}

func TestJobs_RevokeFailureStillDeletes(t *testing.T) {
	t.Parallel()

	rv := newFakeRevoke(t, http.StatusInternalServerError)
	f := newFixture(t, rv)
	stop := startRun(t, f, inactiveClock())
	waitDeleted(t, f.st, f.withToken, f.withoutToken)
	waitFor(t, "the revoke call", func() bool { return len(rv.revoked()) >= 1 })
	stop()

	if got := rv.revoked(); len(got) != 1 {
		t.Errorf("revoke calls = %d, want 1", len(got))
	}
}

func TestJobs_ActiveUsersKept(t *testing.T) {
	t.Parallel()

	rv := newFakeRevoke(t, http.StatusOK)
	f := newFixture(t, rv)
	stop := startRun(t, f, time.Now)
	// Run's first pass is synchronous before its loop; give it time, then
	// stop and check nothing was touched.
	time.Sleep(100 * time.Millisecond)
	stop()

	for _, u := range []store.User{f.withToken, f.withoutToken} {
		if _, err := f.st.UserBySlugOrTraktID(context.Background(), u.TraktID); err != nil {
			t.Errorf("active user %s: %v", u.ID, err)
		}
	}

	if got := rv.revoked(); len(got) != 0 {
		t.Errorf("revoked = %q, want none", got)
	}

	if _, err := f.st.ClientByID(context.Background(), "client-1"); err != nil {
		t.Errorf("recent client purged: %v", err)
	}
}

func TestJobs_StopsOnCancel(t *testing.T) {
	t.Parallel()

	rv := newFakeRevoke(t, http.StatusOK)
	f := newFixture(t, rv)
	stop := startRun(t, f, time.Now)
	stop()
}

// TestPurgeUser_ReconnectedMeanwhileKept covers a user listed as inactive who
// reconnects before purgeUser runs: their last_seen_at is now past the cutoff.
func TestPurgeUser_ReconnectedMeanwhileKept(t *testing.T) {
	t.Parallel()

	rv := newFakeRevoke(t, http.StatusOK)
	f := newFixture(t, rv)

	cutoff := time.Now().Add(-time.Hour)
	for _, u := range []store.User{f.withToken, f.withoutToken} {
		purgeUser(context.Background(), f.st, f.up, u.ID, cutoff)

		if _, err := f.st.UserBySlugOrTraktID(context.Background(), u.TraktID); err != nil {
			t.Errorf("reconnected user %s deleted: %v", u.ID, err)
		}
	}

	if got := rv.revoked(); len(got) != 0 {
		t.Errorf("revoked = %q, want none for a reconnected user", got)
	}

	tok, err := f.st.TraktToken(context.Background(), f.withToken.ID)
	if err != nil || tok.RefreshToken != "refresh-a" {
		t.Errorf("TraktToken = %+v, %v; want the grant kept", tok, err)
	}
}
