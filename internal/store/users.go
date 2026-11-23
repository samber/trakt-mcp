package store

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// userIDBytes random bytes give a 32-hex-char user id, unguessable and stable across slug changes.
	userIDBytes = 16
	// maxSlugMatches is how many rows UserBySlugOrTraktID fetches by slug: two
	// are enough to tell a unique match from an ambiguous one.
	maxSlugMatches = 2
)

// userColumns is the column list scanned by scanUser, in order.
const userColumns = `id, trakt_id, trakt_slug, created_at, last_seen_at`

// User is a person who connected their Trakt account. ID is our own id; it
// keys every per-user row and never changes, while TraktSlug follows renames.
type User struct {
	ID, TraktID, TraktSlug string
	CreatedAt, LastSeenAt  time.Time
}

// TraktToken holds a user's Trakt OAuth tokens in clear, in memory only.
type TraktToken struct {
	RefreshToken, AccessToken string
	// Expiry is the access token expiry; zero when unknown.
	Expiry time.Time
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (User, error) {
	var (
		u                 User
		created, lastSeen int64
	)
	if err := row.Scan(&u.ID, &u.TraktID, &u.TraktSlug, &created, &lastSeen); err != nil {
		return User{}, err
	}

	u.CreatedAt = time.Unix(created, 0)
	u.LastSeenAt = time.Unix(lastSeen, 0)

	return u, nil
}

// normalizeSlug trims and lowercases a slug: Trakt slugs are lowercase, so an
// operator typing "Alice" still finds "alice".
func normalizeSlug(slug string) string {
	return strings.ToLower(strings.TrimSpace(slug))
}

func newUserID() (string, error) {
	b := make([]byte, userIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("store: read user id: %w", err)
	}

	return hex.EncodeToString(b), nil
}

// UpsertUser creates the user for a Trakt user id, or updates its slug if it
// exists, keeping our user id. Both cases mark the user as seen now.
func (s *Store) UpsertUser(ctx context.Context, traktID, traktSlug string) (User, error) {
	traktID = strings.TrimSpace(traktID)
	if traktID == "" {
		return User{}, errors.New("store: empty trakt id")
	}

	traktSlug = normalizeSlug(traktSlug)
	if traktSlug == "" {
		return User{}, errors.New("store: empty trakt slug")
	}

	id, err := newUserID()
	if err != nil {
		return User{}, err
	}

	now := s.now().Unix()

	u, err := scanUser(s.db.QueryRowContext(ctx, `
		INSERT INTO users (id, trakt_id, trakt_slug, created_at, last_seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (trakt_id) DO UPDATE SET trakt_slug = excluded.trakt_slug, last_seen_at = excluded.last_seen_at
		RETURNING `+userColumns,
		id, traktID, traktSlug, now, now))
	if err != nil {
		return User{}, fmt.Errorf("store: upsert user: %w", err)
	}

	return u, nil
}

// UserBySlugOrTraktID finds a user by exact Trakt user id, else by
// case-insensitive Trakt slug. An id match wins; a slug held by several users
// is ErrAmbiguous.
func (s *Store) UserBySlugOrTraktID(ctx context.Context, q string) (User, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return User{}, ErrNotFound
	}

	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE trakt_id = ?`, q))
	if err == nil {
		return u, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, fmt.Errorf("store: user by trakt id: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE trakt_slug = ? LIMIT ?`, normalizeSlug(q), maxSlugMatches)
	if err != nil {
		return User{}, fmt.Errorf("store: user by trakt slug: %w", err)
	}

	users, err := collectUsers(rows)
	if err != nil {
		return User{}, fmt.Errorf("store: user by trakt slug: %w", err)
	}

	switch len(users) {
	case 0:
		return User{}, ErrNotFound
	case 1:
		return users[0], nil
	default:
		return User{}, ErrAmbiguous
	}
}

func collectUsers(rows *sql.Rows) (users []User, err error) {
	defer func() { err = errors.Join(err, rows.Close()) }()

	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}

		users = append(users, u)
	}

	return users, rows.Err()
}

// TouchUser records that the user was active now. It only updates memory:
// FlushTouches writes the accumulated times to the database, so a tool call
// costs no database write. It never fails; the error result keeps the
// signature the tools package depends on.
func (s *Store) TouchUser(_ context.Context, id string) error {
	now := s.now()
	s.touchMu.Lock()
	defer s.touchMu.Unlock()

	if last, ok := s.pendingTouches[id]; !ok || now.After(last) {
		s.pendingTouches[id] = now
	}

	return nil
}

// FlushTouches writes the pending activity times to users.last_seen_at in one
// transaction, so any number of users costs one commit. It does nothing, and
// touches no database file, when nothing is pending. A user's last_seen_at
// never moves backwards. On failure the times stay pending for the next call.
// A crash loses at most the activity since the last successful flush, which
// the day-scale inactivity purge cannot notice.
func (s *Store) FlushTouches(ctx context.Context) (err error) {
	batch := s.takePendingTouches()
	if len(batch) == 0 {
		return nil
	}
	defer func() {
		if err != nil {
			s.restoreTouches(batch)
		}
	}()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: flush touches: begin: %w", err)
	}

	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	for id, seen := range batch {
		// max() keeps a newer value written meanwhile (UpsertUser on a reconnect).
		if _, err := tx.ExecContext(ctx,
			`UPDATE users SET last_seen_at = max(last_seen_at, ?) WHERE id = ?`, seen.Unix(), id); err != nil {
			return fmt.Errorf("store: flush touches: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: flush touches: commit: %w", err)
	}

	return nil
}

// takePendingTouches hands the pending times to the caller and starts a new
// empty set. It returns nil when nothing is pending.
func (s *Store) takePendingTouches() map[string]time.Time {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()

	if len(s.pendingTouches) == 0 {
		return nil
	}

	batch := s.pendingTouches
	s.pendingTouches = make(map[string]time.Time)

	return batch
}

// restoreTouches puts back the times of a failed flush, keeping any newer one
// recorded since.
func (s *Store) restoreTouches(batch map[string]time.Time) {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()

	for id, seen := range batch {
		if cur, ok := s.pendingTouches[id]; !ok || seen.After(cur) {
			s.pendingTouches[id] = seen
		}
	}
}

// touchedSince reports whether the user has activity, not yet flushed, at or
// after cutoff.
func (s *Store) touchedSince(id string, cutoff time.Time) bool {
	s.touchMu.Lock()
	defer s.touchMu.Unlock()

	seen, ok := s.pendingTouches[id]

	return ok && !seen.Before(cutoff)
}

// forgetTouch drops the pending activity of a deleted user.
func (s *Store) forgetTouch(id string) {
	s.touchMu.Lock()
	delete(s.pendingTouches, id)
	s.touchMu.Unlock()
}

// SaveTraktToken stores both tokens encrypted under the active key, bound to
// userID through the AEAD additional data, replacing any previous row.
func (s *Store) SaveTraktToken(ctx context.Context, userID string, t TraktToken) error {
	aad := []byte(userID)

	refreshCT, err := s.kr.Encrypt([]byte(t.RefreshToken), aad)
	if err != nil {
		return fmt.Errorf("store: encrypt refresh token: %w", err)
	}

	accessCT, err := s.kr.Encrypt([]byte(t.AccessToken), aad)
	if err != nil {
		return fmt.Errorf("store: encrypt access token: %w", err)
	}

	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO trakt_tokens (user_id, refresh_ct, access_ct, expiry, key_id)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET
			refresh_ct = excluded.refresh_ct, access_ct = excluded.access_ct,
			expiry = excluded.expiry, key_id = excluded.key_id`,
		userID, refreshCT, accessCT, unixOrZero(t.Expiry), s.kr.ActiveID()); err != nil {
		return fmt.Errorf("store: save trakt token: %w", err)
	}

	return nil
}

// SaveTraktTokenIf stores t only if the user's stored refresh token still
// equals expectedRefresh, and reports whether it did. A refresh that started
// before a new consent must not overwrite the newer grant. The read, compare
// and write run in one immediate transaction. A user with no stored token
// reports false.
func (s *Store) SaveTraktTokenIf(ctx context.Context, userID, expectedRefresh string, t TraktToken) (saved bool, err error) {
	aad := []byte(userID)

	refreshCT, err := s.kr.Encrypt([]byte(t.RefreshToken), aad)
	if err != nil {
		return false, fmt.Errorf("store: encrypt refresh token: %w", err)
	}

	accessCT, err := s.kr.Encrypt([]byte(t.AccessToken), aad)
	if err != nil {
		return false, fmt.Errorf("store: encrypt access token: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: save trakt token if: begin: %w", err)
	}
	defer func() {
		if err != nil || !saved {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	match, err := s.refreshMatches(ctx, tx, userID, expectedRefresh)
	if err != nil || !match {
		return false, err
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE trakt_tokens SET refresh_ct = ?, access_ct = ?, expiry = ?, key_id = ?
		WHERE user_id = ?`,
		refreshCT, accessCT, unixOrZero(t.Expiry), s.kr.ActiveID(), userID); err != nil {
		return false, fmt.Errorf("store: save trakt token if: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: save trakt token if: commit: %w", err)
	}

	return true, nil
}

// DeleteUserIfRefresh deletes the user like DeleteUser, but only if their
// stored refresh token still equals expectedRefresh, and reports whether it
// did. An invalid_grant on an old refresh token must not delete a user who
// re-consented in the meantime. A user with no stored token reports false.
func (s *Store) DeleteUserIfRefresh(ctx context.Context, userID, expectedRefresh string) (deleted bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: delete user if: begin: %w", err)
	}
	defer func() {
		if err != nil || !deleted {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	match, err := s.refreshMatches(ctx, tx, userID, expectedRefresh)
	if err != nil || !match {
		return false, err
	}

	for _, query := range deleteUserChildren {
		if _, err := tx.ExecContext(ctx, query, userID); err != nil {
			return false, fmt.Errorf("store: delete user rows: %w", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return false, fmt.Errorf("store: delete user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: delete user if: commit: %w", err)
	}

	s.forgetTouch(userID)

	return true, nil
}

// refreshMatches reports whether userID's stored refresh token equals
// expected, comparing the decrypted values in constant time.
func (s *Store) refreshMatches(ctx context.Context, tx *sql.Tx, userID, expected string) (bool, error) {
	var refreshCT []byte

	err := tx.QueryRowContext(ctx,
		`SELECT refresh_ct FROM trakt_tokens WHERE user_id = ?`, userID).Scan(&refreshCT)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("store: load trakt token: %w", err)
	}

	current, err := s.kr.Decrypt(refreshCT, []byte(userID))
	if err != nil {
		return false, fmt.Errorf("store: decrypt refresh token: %w", err)
	}

	return subtle.ConstantTimeCompare(current, []byte(expected)) == 1, nil
}

// TraktToken returns the decrypted tokens of a user, or ErrNotFound.
func (s *Store) TraktToken(ctx context.Context, userID string) (TraktToken, error) {
	var (
		refreshCT, accessCT []byte
		expiry              int64
	)

	err := s.db.QueryRowContext(ctx,
		`SELECT refresh_ct, access_ct, expiry FROM trakt_tokens WHERE user_id = ?`, userID).
		Scan(&refreshCT, &accessCT, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return TraktToken{}, ErrNotFound
	}

	if err != nil {
		return TraktToken{}, fmt.Errorf("store: load trakt token: %w", err)
	}

	aad := []byte(userID)

	refresh, err := s.kr.Decrypt(refreshCT, aad)
	if err != nil {
		return TraktToken{}, fmt.Errorf("store: decrypt refresh token: %w", err)
	}

	access, err := s.kr.Decrypt(accessCT, aad)
	if err != nil {
		return TraktToken{}, fmt.Errorf("store: decrypt access token: %w", err)
	}

	t := TraktToken{RefreshToken: string(refresh), AccessToken: string(access)}
	if expiry != 0 {
		t.Expiry = time.Unix(expiry, 0)
	}

	return t, nil
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.Unix()
}

// deleteUserChildren removes every row owned by a user, run before the user
// row itself so the foreign keys never see an orphan.
var deleteUserChildren = []string{
	`DELETE FROM trakt_tokens WHERE user_id = ?`,
	`DELETE FROM oauth_tokens WHERE user_id = ?`,
}

// DeleteUser removes a user with their Trakt tokens and every fosite row
// bound to them, in one transaction. It returns ErrNotFound for an unknown id.
func (s *Store) DeleteUser(ctx context.Context, userID string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: delete user: begin: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	for _, query := range deleteUserChildren {
		if _, err := tx.ExecContext(ctx, query, userID); err != nil {
			return fmt.Errorf("store: delete user rows: %w", err)
		}
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID)
	if err != nil {
		return fmt.Errorf("store: delete user: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete user: %w", err)
	}

	if n == 0 {
		return ErrNotFound
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: delete user: commit: %w", err)
	}

	s.forgetTouch(userID)

	return nil
}

// DeleteUserIfInactive deletes the user like DeleteUser, but only while they
// are still last seen strictly before cutoff and their stored refresh token
// still equals *expectedRefresh ("" meaning no stored token), and reports
// whether it did. expectedRefresh is nullable: nil skips the token check, for
// a token that cannot be read (sealed under a retired key). The inactivity purge reads the user's token, then deletes:
// a user who reconnected in between (new last_seen_at, new grant) must keep
// their account. The checks and the delete run in one immediate transaction.
func (s *Store) DeleteUserIfInactive(ctx context.Context, userID string, cutoff time.Time, expectedRefresh *string) (deleted bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: delete inactive user: begin: %w", err)
	}
	defer func() {
		if err != nil || !deleted {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	var lastSeen int64

	err = tx.QueryRowContext(ctx, `SELECT last_seen_at FROM users WHERE id = ?`, userID).Scan(&lastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("store: delete inactive user: %w", err)
	}
	// Activity still waiting in memory for FlushTouches counts as seen: the
	// purge must not delete a user who called a tool in the last minute.
	if lastSeen >= cutoff.Unix() || s.touchedSince(userID, cutoff) {
		return false, nil
	}

	match, err := s.tokenGuardMatches(ctx, tx, userID, expectedRefresh)
	if err != nil || !match {
		return false, err
	}

	if err := deleteChildRows(ctx, tx, userID); err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, userID); err != nil {
		return false, fmt.Errorf("store: delete inactive user: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: delete inactive user: commit: %w", err)
	}

	s.forgetTouch(userID)

	return true, nil
}

// tokenGuardMatches reports whether the stored Trakt token still matches
// expectedRefresh: nil skips the check, "" requires no stored token, any other
// value must equal the stored refresh token.
func (s *Store) tokenGuardMatches(ctx context.Context, tx *sql.Tx, userID string, expectedRefresh *string) (bool, error) {
	switch {
	case expectedRefresh == nil:
		return true, nil
	case *expectedRefresh == "":
		return s.hasNoTraktToken(ctx, tx, userID)
	default:
		return s.refreshMatches(ctx, tx, userID, *expectedRefresh)
	}
}

// deleteChildRows runs every deleteUserChildren statement for userID inside tx.
func deleteChildRows(ctx context.Context, tx *sql.Tx, userID string) error {
	for _, query := range deleteUserChildren {
		if _, err := tx.ExecContext(ctx, query, userID); err != nil {
			return fmt.Errorf("store: delete user rows: %w", err)
		}
	}

	return nil
}

// hasNoTraktToken reports whether userID has no stored Trakt token row.
func (s *Store) hasNoTraktToken(ctx context.Context, tx *sql.Tx, userID string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM trakt_tokens WHERE user_id = ?`, userID).Scan(&n); err != nil {
		return false, fmt.Errorf("store: count trakt tokens: %w", err)
	}

	return n == 0, nil
}

// InactiveUsers lists users last seen strictly before the cutoff.
func (s *Store) InactiveUsers(ctx context.Context, before time.Time) ([]User, error) {
	rows, err := s.db.QueryContext(ctx,
		// Literal column list (same order as userColumns): no string concatenation in SQL.
		`SELECT id, trakt_id, trakt_slug, created_at, last_seen_at FROM users WHERE last_seen_at < ? ORDER BY last_seen_at`, before.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: inactive users: %w", err)
	}

	users, err := collectUsers(rows)
	if err != nil {
		return nil, fmt.Errorf("store: inactive users: %w", err)
	}

	return users, nil
}

type sealedToken struct {
	userID              string
	refreshCT, accessCT []byte
}

// Rekey re-encrypts under the active key every Trakt token row holding a
// ciphertext sealed by another key, and returns how many rows it rewrote.
// It runs in one transaction: a row sealed by a key missing from the keyring
// aborts the whole run.
func (s *Store) Rekey(ctx context.Context) (n int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("store: rekey: begin: %w", err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	rows, err := tx.QueryContext(ctx, `SELECT user_id, refresh_ct, access_ct FROM trakt_tokens`)
	if err != nil {
		return 0, fmt.Errorf("store: rekey: list: %w", err)
	}
	// Rows are read fully before writing: the transaction holds the only connection.
	stale, err := s.collectStale(rows)
	if err != nil {
		return 0, err
	}

	active := s.kr.ActiveID()

	for _, row := range stale {
		aad := []byte(row.userID)

		refreshCT, err := s.reseal(row.refreshCT, aad)
		if err != nil {
			return 0, fmt.Errorf("store: rekey user %s: refresh token: %w", row.userID, err)
		}

		accessCT, err := s.reseal(row.accessCT, aad)
		if err != nil {
			return 0, fmt.Errorf("store: rekey user %s: access token: %w", row.userID, err)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE trakt_tokens SET refresh_ct = ?, access_ct = ?, key_id = ? WHERE user_id = ?`,
			refreshCT, accessCT, active, row.userID); err != nil {
			return 0, fmt.Errorf("store: rekey user %s: %w", row.userID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("store: rekey: commit: %w", err)
	}

	return len(stale), nil
}

func (s *Store) collectStale(rows *sql.Rows) (stale []sealedToken, err error) {
	defer func() { err = errors.Join(err, rows.Close()) }()

	active := s.kr.ActiveID()

	for rows.Next() {
		var t sealedToken
		if err := rows.Scan(&t.userID, &t.refreshCT, &t.accessCT); err != nil {
			return nil, fmt.Errorf("store: rekey: scan: %w", err)
		}

		for _, ct := range [][]byte{t.refreshCT, t.accessCT} {
			id, err := s.kr.KeyID(ct)
			if err != nil {
				return nil, fmt.Errorf("store: rekey user %s: %w", t.userID, err)
			}

			if id != active {
				stale = append(stale, t)
				break
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: rekey: list: %w", err)
	}

	return stale, nil
}

// reseal decrypts ct with whichever key sealed it and encrypts it again under the active key.
func (s *Store) reseal(ct, aad []byte) ([]byte, error) {
	plaintext, err := s.kr.Decrypt(ct, aad)
	if err != nil {
		return nil, err
	}

	return s.kr.Encrypt(plaintext, aad)
}
