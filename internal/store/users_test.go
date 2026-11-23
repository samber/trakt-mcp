package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/samber/google-contacts-mcp/internal/crypto"
)

// Fixed test keys: 32 bytes each, base64-encoded.
const (
	testKeyA = "A:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	testKeyB = "B:AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE="
)

func mustKeyring(t *testing.T, s string) *crypto.Keyring {
	t.Helper()

	kr, err := crypto.ParseKeyring(s)
	if err != nil {
		t.Fatalf("ParseKeyring: %v", err)
	}

	return kr
}

func openAt(t *testing.T, path, keys string) *Store {
	t.Helper()

	s, err := Open(context.Background(), path, mustKeyring(t, keys))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	return s
}

func openTemp(t *testing.T) *Store {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "nested", "dir", "db.sqlite"), testKeyA)
}

func TestOpen_MigratesTwiceIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sub", "db.sqlite")
	kr := mustKeyring(t, testKeyA)

	s1, err := Open(ctx, path, kr)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	if _, err := s1.UpsertUser(ctx, "sub-1", "a@example.com"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2 := openAt(t, path, testKeyA)

	var n int
	if err := s2.db.QueryRowContext(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil {
		t.Fatalf("count migrations: %v", err)
	}

	if n != 1 {
		t.Errorf("schema_migrations rows = %d, want 1", n)
	}

	if _, err := s2.UserBySubOrEmail(ctx, "sub-1"); err != nil {
		t.Errorf("user lost after reopen: %v", err)
	}

	var fk int
	if err := s2.db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("pragma foreign_keys: %v", err)
	}

	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}

	var mode string
	if err := s2.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	// WAL needs shared memory, which network filesystems do not provide.
	if mode != "delete" {
		t.Errorf("journal_mode = %q, want delete", mode)
	}
}

func TestOpen_RejectsQueryInPath(t *testing.T) {
	t.Parallel()

	_, err := Open(context.Background(), filepath.Join(t.TempDir(), "db?x=1"), mustKeyring(t, testKeyA))
	if err == nil {
		t.Fatal("Open accepted a path with '?'")
	}
}

func TestUpsertUser_SameSubUpdatesEmail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)

	u1, err := s.UpsertUser(ctx, "sub-1", "Old@Example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if len(u1.ID) != 2*userIDBytes {
		t.Errorf("ID = %q, want %d hex chars", u1.ID, 2*userIDBytes)
	}

	if u1.Email != "old@example.com" {
		t.Errorf("Email = %q, want lowercased", u1.Email)
	}

	u2, err := s.UpsertUser(ctx, "sub-1", "new@example.com")
	if err != nil {
		t.Fatalf("UpsertUser again: %v", err)
	}

	if u2.ID != u1.ID {
		t.Errorf("ID changed: %q -> %q", u1.ID, u2.ID)
	}

	if u2.Email != "new@example.com" {
		t.Errorf("Email = %q, want new@example.com", u2.Email)
	}

	if !u2.CreatedAt.Equal(u1.CreatedAt) {
		t.Errorf("CreatedAt changed: %v -> %v", u1.CreatedAt, u2.CreatedAt)
	}

	other, err := s.UpsertUser(ctx, "sub-2", "x@example.com")
	if err != nil {
		t.Fatalf("UpsertUser other: %v", err)
	}

	if other.ID == u1.ID {
		t.Error("two subs share one ID")
	}
}

func TestUpsertUser_RejectsEmptySub(t *testing.T) {
	t.Parallel()

	if _, err := openTemp(t).UpsertUser(context.Background(), "", "a@example.com"); err == nil {
		t.Fatal("UpsertUser accepted an empty sub")
	}
}

func TestUserBySubOrEmail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)

	u, err := s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	for _, q := range []string{"sub-1", "a@example.com", "A@Example.COM", " a@example.com "} {
		got, err := s.UserBySubOrEmail(ctx, q)
		if err != nil {
			t.Errorf("UserBySubOrEmail(%q): %v", q, err)
			continue
		}

		if got.ID != u.ID {
			t.Errorf("UserBySubOrEmail(%q).ID = %q, want %q", q, got.ID, u.ID)
		}
	}

	for _, q := range []string{"nobody@example.com", "SUB-1", ""} {
		if _, err := s.UserBySubOrEmail(ctx, q); !errors.Is(err, ErrNotFound) {
			t.Errorf("UserBySubOrEmail(%q) err = %v, want ErrNotFound", q, err)
		}
	}
}

func TestUserBySubOrEmail_SubWinsOverEmail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)
	// A sub that looks like another user's e-mail must still resolve by sub.
	bySub, err := s.UpsertUser(ctx, "b@example.com", "x@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if _, err := s.UpsertUser(ctx, "sub-2", "b@example.com"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	got, err := s.UserBySubOrEmail(ctx, "b@example.com")
	if err != nil {
		t.Fatalf("UserBySubOrEmail: %v", err)
	}

	if got.ID != bySub.ID {
		t.Errorf("got user %q, want the sub match %q", got.ID, bySub.ID)
	}
}

func TestUserBySubOrEmail_AmbiguousEmail(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	s := openTemp(t)
	for _, sub := range []string{"sub-1", "sub-2"} {
		if _, err := s.UpsertUser(ctx, sub, "shared@example.com"); err != nil {
			t.Fatalf("UpsertUser: %v", err)
		}
	}

	if _, err := s.UserBySubOrEmail(ctx, "shared@example.com"); !errors.Is(err, ErrAmbiguous) {
		t.Errorf("err = %v, want ErrAmbiguous", err)
	}
}

func TestGoogleToken_RoundTripAndCiphertextOnDisk(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)

	u, err := s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if _, err := s.GoogleToken(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GoogleToken before save err = %v, want ErrNotFound", err)
	}

	want := GoogleToken{
		RefreshToken: "1//refresh-plaintext-secret",
		AccessToken:  "ya29.access-plaintext-secret",
		Expiry:       time.Unix(1_900_000_000, 0),
	}
	if err := s.SaveGoogleToken(ctx, u.ID, want); err != nil {
		t.Fatalf("SaveGoogleToken: %v", err)
	}

	got, err := s.GoogleToken(ctx, u.ID)
	if err != nil {
		t.Fatalf("GoogleToken: %v", err)
	}

	if got.RefreshToken != want.RefreshToken || got.AccessToken != want.AccessToken || !got.Expiry.Equal(want.Expiry) {
		t.Errorf("GoogleToken = %+v, want %+v", got, want)
	}

	var (
		refreshCT, accessCT []byte
		keyID               string
	)

	err = s.db.QueryRowContext(ctx,
		`SELECT refresh_ct, access_ct, key_id FROM google_tokens WHERE user_id = ?`, u.ID).
		Scan(&refreshCT, &accessCT, &keyID)
	if err != nil {
		t.Fatalf("raw select: %v", err)
	}

	if bytes.Contains(refreshCT, []byte(want.RefreshToken)) {
		t.Error("refresh_ct contains the plaintext refresh token")
	}

	if bytes.Contains(accessCT, []byte(want.AccessToken)) {
		t.Error("access_ct contains the plaintext access token")
	}

	if keyID != "A" {
		t.Errorf("key_id = %q, want A", keyID)
	}

	assertCiphertextBoundToUser(t, s, u, want, refreshCT, accessCT)
}

// assertCiphertextBoundToUser checks that ciphertext copied to another user
// fails to decrypt and that saving again replaces the row.
func assertCiphertextBoundToUser(t *testing.T, s *Store, u User, want GoogleToken, refreshCT, accessCT []byte) {
	t.Helper()

	ctx := context.Background()

	// Ciphertext is bound to its user: moving it to another user must fail to decrypt.
	other, err := s.UpsertUser(ctx, "sub-2", "b@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO google_tokens (user_id, refresh_ct, access_ct, expiry, key_id) VALUES (?, ?, ?, 0, 'A')`,
		other.ID, refreshCT, accessCT); err != nil {
		t.Fatalf("copy row: %v", err)
	}

	if _, err := s.GoogleToken(ctx, other.ID); err == nil {
		t.Error("GoogleToken decrypted a ciphertext copied from another user")
	}

	// Saving again replaces the row.
	want.AccessToken = "ya29.second"
	if err := s.SaveGoogleToken(ctx, u.ID, want); err != nil {
		t.Fatalf("SaveGoogleToken again: %v", err)
	}

	if got, err := s.GoogleToken(ctx, u.ID); err != nil || got.AccessToken != "ya29.second" {
		t.Errorf("after resave: %+v, %v", got, err)
	}
}

func TestSaveGoogleToken_UnknownUser(t *testing.T) {
	t.Parallel()

	err := openTemp(t).SaveGoogleToken(context.Background(), "no-such-user", GoogleToken{RefreshToken: "r"})
	if err == nil {
		t.Fatal("SaveGoogleToken accepted an unknown user")
	}
}

func TestDeleteUser_Cascades(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)

	u, err := s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	keep, err := s.UpsertUser(ctx, "sub-2", "b@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	for _, id := range []string{u.ID, keep.ID} {
		if err := s.SaveGoogleToken(ctx, id, GoogleToken{RefreshToken: "r", AccessToken: "a"}); err != nil {
			t.Fatalf("SaveGoogleToken: %v", err)
		}

		if _, err := s.db.ExecContext(ctx,
			`INSERT INTO oauth_tokens (signature, kind, request_id, user_id, client_id, data, expires_at, active)
			 VALUES (?, 'access', 'req', ?, 'client', x'00', 0, 1)`, "sig-"+id, id); err != nil {
			t.Fatalf("insert oauth_tokens: %v", err)
		}
	}

	if err := s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	count := func(query string, args ...any) int {
		t.Helper()

		var n int
		if err := s.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}

		return n
	}
	if n := count(`SELECT count(*) FROM google_tokens WHERE user_id = ?`, u.ID); n != 0 {
		t.Errorf("google_tokens rows left = %d", n)
	}

	if n := count(`SELECT count(*) FROM oauth_tokens WHERE user_id = ?`, u.ID); n != 0 {
		t.Errorf("oauth_tokens rows left = %d", n)
	}

	if n := count(`SELECT count(*) FROM oauth_tokens WHERE user_id = ?`, keep.ID); n != 1 {
		t.Errorf("other user's oauth_tokens rows = %d, want 1", n)
	}

	if _, err := s.UserBySubOrEmail(ctx, "sub-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("user still found: %v", err)
	}

	if err := s.DeleteUser(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteUser err = %v, want ErrNotFound", err)
	}
}

func TestRekey(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")
	want := GoogleToken{RefreshToken: "refresh", AccessToken: "access", Expiry: time.Unix(1_900_000_000, 0)}

	sA, err := Open(ctx, path, mustKeyring(t, testKeyA))
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}

	u, err := sA.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := sA.SaveGoogleToken(ctx, u.ID, want); err != nil {
		t.Fatalf("SaveGoogleToken: %v", err)
	}

	if err := sA.Close(); err != nil {
		t.Fatalf("Close A: %v", err)
	}

	sBA, err := Open(ctx, path, mustKeyring(t, testKeyB+","+testKeyA))
	if err != nil {
		t.Fatalf("Open B,A: %v", err)
	}

	n, err := sBA.Rekey(ctx)
	if err != nil {
		t.Fatalf("Rekey: %v", err)
	}

	if n != 1 {
		t.Errorf("Rekey = %d, want 1", n)
	}

	if n, err := sBA.Rekey(ctx); err != nil || n != 0 {
		t.Errorf("second Rekey = %d, %v; want 0, nil", n, err)
	}

	if err := sBA.Close(); err != nil {
		t.Fatalf("Close B,A: %v", err)
	}

	assertRekeyedUnderB(t, path, u.ID, want)
}

// assertRekeyedUnderB reopens the database with key B only and checks the
// token for userID decrypts to want under key id B.
func assertRekeyedUnderB(t *testing.T, path, userID string, want GoogleToken) {
	t.Helper()

	ctx := context.Background()

	sB := openAt(t, path, testKeyB)

	got, err := sB.GoogleToken(ctx, userID)
	if err != nil {
		t.Fatalf("GoogleToken under B only: %v", err)
	}

	if got != want {
		t.Errorf("GoogleToken = %+v, want %+v", got, want)
	}

	var keyID string
	if err := sB.db.QueryRowContext(ctx, `SELECT key_id FROM google_tokens`).Scan(&keyID); err != nil {
		t.Fatalf("select key_id: %v", err)
	}

	if keyID != "B" {
		t.Errorf("key_id = %q, want B", keyID)
	}
}

func TestRekey_MissingOldKeyFails(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")

	sA, err := Open(ctx, path, mustKeyring(t, testKeyA))
	if err != nil {
		t.Fatalf("Open A: %v", err)
	}

	u, err := sA.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := sA.SaveGoogleToken(ctx, u.ID, GoogleToken{RefreshToken: "r"}); err != nil {
		t.Fatalf("SaveGoogleToken: %v", err)
	}

	if err := sA.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := openAt(t, path, testKeyB).Rekey(ctx); err == nil {
		t.Fatal("Rekey succeeded without the key that sealed the row")
	}
}

func TestInactiveUsers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)
	base := time.Unix(1_800_000_000, 0)

	s.now = func() time.Time { return base }

	old, err := s.UpsertUser(ctx, "old", "old@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	touched, err := s.UpsertUser(ctx, "touched", "touched@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	s.now = func() time.Time { return base.Add(2 * time.Hour) }
	if _, err := s.UpsertUser(ctx, "fresh", "fresh@example.com"); err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := s.TouchUser(ctx, touched.ID); err != nil {
		t.Fatalf("TouchUser: %v", err)
	}

	if err := s.FlushTouches(ctx); err != nil {
		t.Fatalf("FlushTouches: %v", err)
	}

	got, err := s.InactiveUsers(ctx, base.Add(time.Hour))
	if err != nil {
		t.Fatalf("InactiveUsers: %v", err)
	}

	if len(got) != 1 || got[0].ID != old.ID {
		t.Fatalf("InactiveUsers = %+v, want only %q", got, old.ID)
	}

	if !got[0].LastSeenAt.Equal(base) || got[0].Email != "old@example.com" {
		t.Errorf("InactiveUsers[0] = %+v", got[0])
	}
	// The cutoff is exclusive: a user seen exactly at `before` is still active.
	if got, err := s.InactiveUsers(ctx, base); err != nil || len(got) != 0 {
		t.Errorf("InactiveUsers(base) = %+v, %v; want none", got, err)
	}
}

// touchFixture opens a store whose clock the test moves with at.
type touchFixture struct {
	s    *Store
	base time.Time
	now  time.Time
}

func newTouchFixture(t *testing.T) *touchFixture {
	t.Helper()
	f := &touchFixture{s: openTemp(t), base: time.Unix(1_800_000_000, 0)}
	f.at(0)

	return f
}

// at moves the store clock to base+d.
func (f *touchFixture) at(d time.Duration) {
	f.now = f.base.Add(d)
	f.s.now = func() time.Time { return f.now }
}

func (f *touchFixture) lastSeen(t *testing.T, sub string) time.Time {
	t.Helper()

	got, err := f.s.UserBySubOrEmail(context.Background(), sub)
	if err != nil {
		t.Fatalf("UserBySubOrEmail: %v", err)
	}

	return got.LastSeenAt
}

func TestTouchUser_WritesOnlyOnFlush(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newTouchFixture(t)

	u, err := f.s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	// Touches arrive out of order: the latest time wins, none reaches the database.
	for _, d := range []time.Duration{10 * time.Second, 70 * time.Second, 30 * time.Second} {
		f.at(d)

		if err := f.s.TouchUser(ctx, u.ID); err != nil {
			t.Fatalf("TouchUser: %v", err)
		}
	}

	if got := f.lastSeen(t, "sub-1"); !got.Equal(f.base) {
		t.Errorf("before flush: last_seen_at = %v, want unchanged %v", got, f.base)
	}

	if err := f.s.FlushTouches(ctx); err != nil {
		t.Fatalf("FlushTouches: %v", err)
	}

	if got := f.lastSeen(t, "sub-1"); !got.Equal(f.base.Add(70 * time.Second)) {
		t.Errorf("after flush: last_seen_at = %v, want base+70s", got)
	}

	// A flushed time is not written again: overwrite the row, flush with
	// nothing pending, and the overwrite must survive.
	if _, err := f.s.db.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE id = ?`,
		f.base.Add(5*time.Second).Unix(), u.ID); err != nil {
		t.Fatalf("overwrite last_seen_at: %v", err)
	}

	if err := f.s.FlushTouches(ctx); err != nil {
		t.Fatalf("second FlushTouches: %v", err)
	}

	if got := f.lastSeen(t, "sub-1"); !got.Equal(f.base.Add(5 * time.Second)) {
		t.Errorf("empty flush wrote: last_seen_at = %v, want base+5s", got)
	}
}

func TestFlushTouches_NeverMovesLastSeenBackwards(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newTouchFixture(t)
	f.at(10 * time.Second)

	u, err := f.s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	// The touch is older than the row: a reconnect rewrote last_seen_at after it.
	f.at(5 * time.Second)

	if err := f.s.TouchUser(ctx, u.ID); err != nil {
		t.Fatalf("TouchUser: %v", err)
	}

	if err := f.s.FlushTouches(ctx); err != nil {
		t.Fatalf("FlushTouches: %v", err)
	}

	if got := f.lastSeen(t, "sub-1"); !got.Equal(f.base.Add(10 * time.Second)) {
		t.Errorf("last_seen_at = %v, want base+10s", got)
	}
}

func TestFlushTouches_FailureKeepsTouchesPending(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newTouchFixture(t)

	u, err := f.s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	f.at(10 * time.Second)

	if err := f.s.TouchUser(ctx, u.ID); err != nil {
		t.Fatalf("TouchUser: %v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	if err := f.s.FlushTouches(cancelled); err == nil {
		t.Fatal("FlushTouches with a cancelled context succeeded")
	}

	if got := f.lastSeen(t, "sub-1"); !got.Equal(f.base) {
		t.Fatalf("failed flush wrote: last_seen_at = %v", got)
	}

	// No new touch since: the retry alone must write the time the failed flush took.
	if err := f.s.FlushTouches(ctx); err != nil {
		t.Fatalf("retry FlushTouches: %v", err)
	}

	if got := f.lastSeen(t, "sub-1"); !got.Equal(f.base.Add(10 * time.Second)) {
		t.Errorf("after retry: last_seen_at = %v, want base+10s", got)
	}
}

func TestFlushTouches_RestoreKeepsNewerTouch(t *testing.T) {
	t.Parallel()

	f := newTouchFixture(t)
	id := "user-1"
	f.s.pendingTouches[id] = f.base.Add(20 * time.Second)

	f.s.restoreTouches(map[string]time.Time{id: f.base.Add(10 * time.Second)})

	if got := f.s.pendingTouches[id]; !got.Equal(f.base.Add(20 * time.Second)) {
		t.Errorf("older restored touch replaced a newer one: %v", got)
	}

	f.s.restoreTouches(map[string]time.Time{id: f.base.Add(30 * time.Second)})

	if got := f.s.pendingTouches[id]; !got.Equal(f.base.Add(30 * time.Second)) {
		t.Errorf("newer restored touch was dropped: %v", got)
	}
}

func TestDeleteUser_DropsPendingTouch(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newTouchFixture(t)

	u, err := f.s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := f.s.TouchUser(ctx, u.ID); err != nil {
		t.Fatalf("TouchUser: %v", err)
	}

	if err := f.s.DeleteUser(ctx, u.ID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}

	if n := len(f.s.pendingTouches); n != 0 {
		t.Errorf("%d pending touches after DeleteUser, want 0", n)
	}

	if err := f.s.FlushTouches(ctx); err != nil {
		t.Errorf("FlushTouches: %v", err)
	}
}

func TestDeleteUserIfInactive_PendingTouchKeepsUser(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	f := newTouchFixture(t)

	idle, err := f.s.UpsertUser(ctx, "idle", "idle@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	active, err := f.s.UpsertUser(ctx, "active", "active@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}
	// Both rows were last seen at base; only "active" called a tool since, and
	// that touch has not been flushed yet.
	cutoff := f.base.Add(time.Hour)
	f.at(2 * time.Hour)

	if err := f.s.TouchUser(ctx, active.ID); err != nil {
		t.Fatalf("TouchUser: %v", err)
	}

	if deleted, err := f.s.DeleteUserIfInactive(ctx, active.ID, cutoff, nil); err != nil || deleted {
		t.Errorf("user with a pending touch: deleted %v, err %v; want false, nil", deleted, err)
	}

	if deleted, err := f.s.DeleteUserIfInactive(ctx, idle.ID, cutoff, nil); err != nil || !deleted {
		t.Errorf("idle user: deleted %v, err %v; want true, nil", deleted, err)
	}

	// Once flushed, the database row alone keeps the user.
	if err := f.s.FlushTouches(ctx); err != nil {
		t.Fatalf("FlushTouches: %v", err)
	}

	if deleted, err := f.s.DeleteUserIfInactive(ctx, active.ID, cutoff, nil); err != nil || deleted {
		t.Errorf("flushed user: deleted %v, err %v; want false, nil", deleted, err)
	}
}

func TestClose_FlushesPendingTouches(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "db.sqlite")

	s, err := Open(ctx, path, mustKeyring(t, testKeyA))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	base := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return base }

	u, err := s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	s.now = func() time.Time { return base.Add(10 * time.Second) }
	if err := s.TouchUser(ctx, u.ID); err != nil {
		t.Fatalf("TouchUser: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := openAt(t, path, testKeyA).UserBySubOrEmail(ctx, "sub-1")
	if err != nil {
		t.Fatalf("UserBySubOrEmail: %v", err)
	}

	if !got.LastSeenAt.Equal(base.Add(10 * time.Second)) {
		t.Errorf("last_seen_at after Close = %v, want base+10s", got.LastSeenAt)
	}
}

// TestTouchUser_ConcurrentWithFlush is meant for -race: touches race a flusher
// and every touched user must end up written.
func TestTouchUser_ConcurrentWithFlush(t *testing.T) {
	t.Parallel()

	const (
		touchers      = 8
		touchesEach   = 200
		flushes       = 20
		distinctUsers = 4
	)

	ctx := context.Background()
	s := openTemp(t)
	start := time.Now()

	ids := make([]string, distinctUsers)
	for i := range ids {
		u, err := s.UpsertUser(ctx, fmt.Sprintf("sub-%d", i), fmt.Sprintf("u%d@example.com", i))
		if err != nil {
			t.Fatalf("UpsertUser: %v", err)
		}

		ids[i] = u.ID
		// Reset to the past so only a flushed touch can move the row forward.
		if _, err := s.db.ExecContext(ctx, `UPDATE users SET last_seen_at = 0 WHERE id = ?`, u.ID); err != nil {
			t.Fatalf("reset last_seen_at: %v", err)
		}
	}

	var wg sync.WaitGroup
	for g := range touchers {
		wg.Go(func() {
			for i := range touchesEach {
				if err := s.TouchUser(ctx, ids[(g+i)%distinctUsers]); err != nil {
					t.Errorf("TouchUser: %v", err)
					return
				}
			}
		})
	}

	wg.Go(func() {
		for range flushes {
			if err := s.FlushTouches(ctx); err != nil {
				t.Errorf("FlushTouches: %v", err)
				return
			}
		}
	})
	wg.Wait()

	if err := s.FlushTouches(ctx); err != nil {
		t.Fatalf("final FlushTouches: %v", err)
	}

	for i := range ids {
		got, err := s.UserBySubOrEmail(ctx, fmt.Sprintf("sub-%d", i))
		if err != nil {
			t.Fatalf("UserBySubOrEmail: %v", err)
		}

		if got.LastSeenAt.Unix() < start.Unix() {
			t.Errorf("user %d: last_seen_at = %v, want at or after %v", i, got.LastSeenAt, start)
		}
	}
}

func TestSaveGoogleTokenIf(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)

	u, err := s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if ok, err := s.SaveGoogleTokenIf(ctx, u.ID, "", GoogleToken{RefreshToken: "x"}); err != nil || ok {
		t.Errorf("no row: saved %v, err %v; want false, nil", ok, err)
	}

	if err := s.SaveGoogleToken(ctx, u.ID, GoogleToken{RefreshToken: "r1", AccessToken: "a1"}); err != nil {
		t.Fatalf("SaveGoogleToken: %v", err)
	}

	// A new consent replaced r1 with r2: the stale refresh must not win.
	if err := s.SaveGoogleToken(ctx, u.ID, GoogleToken{RefreshToken: "r2", AccessToken: "a2"}); err != nil {
		t.Fatalf("SaveGoogleToken: %v", err)
	}

	ok, err := s.SaveGoogleTokenIf(ctx, u.ID, "r1", GoogleToken{RefreshToken: "r1", AccessToken: "stale"})
	if err != nil || ok {
		t.Fatalf("mismatch: saved %v, err %v; want false, nil", ok, err)
	}

	if got, err := s.GoogleToken(ctx, u.ID); err != nil || got.RefreshToken != "r2" || got.AccessToken != "a2" {
		t.Errorf("after mismatch: %+v, %v; want r2/a2 kept", got, err)
	}

	assertSaveIfMatches(t, s, u)
}

// assertSaveIfMatches checks that SaveGoogleTokenIf writes when the stored
// refresh token still equals the expected one.
func assertSaveIfMatches(t *testing.T, s *Store, u User) {
	t.Helper()

	ctx := context.Background()

	ok, err := s.SaveGoogleTokenIf(ctx, u.ID, "r2", GoogleToken{RefreshToken: "r2", AccessToken: "a3", Expiry: time.Unix(1_900_000_000, 0)})
	if err != nil || !ok {
		t.Fatalf("match: saved %v, err %v; want true, nil", ok, err)
	}

	if got, err := s.GoogleToken(ctx, u.ID); err != nil || got.AccessToken != "a3" || got.Expiry.Unix() != 1_900_000_000 {
		t.Errorf("after match: %+v, %v", got, err)
	}
}

func TestDeleteUserIfRefresh(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)

	u, err := s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := s.SaveGoogleToken(ctx, u.ID, GoogleToken{RefreshToken: "r2", AccessToken: "a2"}); err != nil {
		t.Fatalf("SaveGoogleToken: %v", err)
	}

	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO oauth_tokens (signature, kind, request_id, user_id, client_id, data, expires_at, active)
		 VALUES ('sig', 'access', 'req', ?, 'client', x'00', 0, 1)`, u.ID); err != nil {
		t.Fatalf("insert oauth_tokens: %v", err)
	}

	if ok, err := s.DeleteUserIfRefresh(ctx, u.ID, "r1"); err != nil || ok {
		t.Fatalf("mismatch: deleted %v, err %v; want false, nil", ok, err)
	}

	if _, err := s.UserBySubOrEmail(ctx, "sub-1"); err != nil {
		t.Fatalf("user gone after mismatch: %v", err)
	}

	assertDeleteIfRefreshMatches(t, s, u)
}

// assertDeleteIfRefreshMatches checks that DeleteUserIfRefresh deletes the user
// and their tokens on a matching refresh token, and refuses an unknown user.
func assertDeleteIfRefreshMatches(t *testing.T, s *Store, u User) {
	t.Helper()

	ctx := context.Background()

	if ok, err := s.DeleteUserIfRefresh(ctx, u.ID, "r2"); err != nil || !ok {
		t.Fatalf("match: deleted %v, err %v; want true, nil", ok, err)
	}

	if _, err := s.UserBySubOrEmail(ctx, "sub-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("user still found: %v", err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM oauth_tokens WHERE user_id = ?`, u.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("oauth_tokens left = %d, err %v", n, err)
	}

	if ok, err := s.DeleteUserIfRefresh(ctx, u.ID, "r2"); err != nil || ok {
		t.Errorf("unknown user: deleted %v, err %v; want false, nil", ok, err)
	}
}

func TestDeleteUserIfInactive(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s := openTemp(t)
	base := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return base }

	u, err := s.UpsertUser(ctx, "sub-1", "a@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if err := s.SaveGoogleToken(ctx, u.ID, GoogleToken{RefreshToken: "r1", AccessToken: "a1"}); err != nil {
		t.Fatalf("SaveGoogleToken: %v", err)
	}

	ptr := func(v string) *string { return &v }
	cutoff := base.Add(time.Hour)

	// Reconnected: last_seen_at moved to the cutoff or past it.
	if ok, err := s.DeleteUserIfInactive(ctx, u.ID, base, ptr("r1")); err != nil || ok {
		t.Fatalf("last seen at cutoff: deleted %v, err %v; want false, nil", ok, err)
	}
	// Reconnected: a new consent replaced the refresh token.
	if ok, err := s.DeleteUserIfInactive(ctx, u.ID, cutoff, ptr("r0")); err != nil || ok {
		t.Fatalf("token changed: deleted %v, err %v; want false, nil", ok, err)
	}
	// A token appeared where none was read.
	if ok, err := s.DeleteUserIfInactive(ctx, u.ID, cutoff, ptr("")); err != nil || ok {
		t.Fatalf("token appeared: deleted %v, err %v; want false, nil", ok, err)
	}

	if _, err := s.UserBySubOrEmail(ctx, "sub-1"); err != nil {
		t.Fatalf("user gone after refused deletes: %v", err)
	}

	assertDeleteIfInactiveDeletes(t, s, u, cutoff)
}

// assertDeleteIfInactiveDeletes checks the deleting paths of DeleteUserIfInactive:
// a still-inactive user with a matching token, a missing user, and a nil guard.
func assertDeleteIfInactiveDeletes(t *testing.T, s *Store, u User, cutoff time.Time) {
	t.Helper()

	ctx := context.Background()
	ptr := func(v string) *string { return &v }

	if ok, err := s.DeleteUserIfInactive(ctx, u.ID, cutoff, ptr("r1")); err != nil || !ok {
		t.Fatalf("still inactive: deleted %v, err %v; want true, nil", ok, err)
	}

	if _, err := s.GoogleToken(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("google token kept: %v", err)
	}

	if ok, err := s.DeleteUserIfInactive(ctx, u.ID, cutoff, nil); err != nil || ok {
		t.Errorf("missing user: deleted %v, err %v; want false, nil", ok, err)
	}

	// No token, nil guard: last_seen_at alone decides.
	v, err := s.UpsertUser(ctx, "sub-2", "b@example.com")
	if err != nil {
		t.Fatalf("UpsertUser: %v", err)
	}

	if ok, err := s.DeleteUserIfInactive(ctx, v.ID, cutoff, nil); err != nil || !ok {
		t.Errorf("nil guard: deleted %v, err %v; want true, nil", ok, err)
	}
}
