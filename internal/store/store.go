// Package store persists users, encrypted Google tokens and OAuth state in a
// single SQLite file.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	// Registers the pure-Go "sqlite" driver, so builds stay CGO_ENABLED=0.
	_ "modernc.org/sqlite"

	"github.com/samber/google-contacts-mcp/internal/crypto"
)

const (
	// driverName is the database/sql name registered by modernc.org/sqlite.
	driverName = "sqlite"
	// dsnPragmas apply to every pooled connection: the rollback journal
	// (DELETE) instead of WAL, because the database file may sit on a network
	// volume and WAL needs a shared-memory file that network filesystems do
	// not support; enforced foreign keys so no child row outlives its user; a
	// 5 s wait on a locked file; secure_delete so freed pages holding
	// ciphertext are zeroed; and _txlock=immediate so a write transaction
	// takes the write lock at BEGIN, waiting on busy_timeout instead of
	// failing with SQLITE_BUSY when another process (rekey beside serve)
	// committed after its snapshot.
	dsnPragmas = "?_pragma=foreign_keys(1)&_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)" +
		"&_pragma=secure_delete(1)&_txlock=immediate"
	// dsnQuerySep starts the DSN query string, so it cannot appear in the path.
	dsnQuerySep = "?"
	// dirMode keeps the data directory private to the service user.
	dirMode = 0o700
	// fileMode keeps the database private; SQLite gives the -journal file the same mode.
	fileMode = 0o600
	// maxOpenConns is 1 because SQLite allows one writer: a single connection
	// serializes writes in Go instead of failing with SQLITE_BUSY.
	maxOpenConns = 1
	// closeFlushTimeout bounds the last_seen_at flush in Close; it matches the
	// busy_timeout, the longest a write waits for another writer.
	closeFlushTimeout = 5 * time.Second
	// migrationsDir is the embedded directory holding NNN_name.sql files.
	migrationsDir = "migrations"
	// migrationExt filters embedded migration files.
	migrationExt = ".sql"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

var (
	// ErrNotFound reports a missing user or token.
	ErrNotFound = errors.New("store: not found")
	// ErrAmbiguous reports an e-mail shared by several users; look them up by sub.
	ErrAmbiguous = errors.New("store: e-mail matches several users")
)

// Store is the SQLite-backed persistence layer.
type Store struct {
	db *sql.DB
	kr *crypto.Keyring
	// now is replaced in tests.
	now func() time.Time

	touchMu sync.Mutex
	// pendingTouches holds, per user id, the latest activity time not yet
	// written to users.last_seen_at. FlushTouches drains it.
	pendingTouches map[string]time.Time
}

// Open opens (creating if needed) the database at path, applies pending
// migrations and returns a Store encrypting with kr.
func Open(ctx context.Context, path string, kr *crypto.Keyring) (*Store, error) {
	if kr == nil {
		return nil, errors.New("store: nil keyring")
	}

	if strings.Contains(path, dsnQuerySep) {
		return nil, fmt.Errorf("store: db path %q must not contain %q", path, dsnQuerySep)
	}

	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("store: create db dir: %w", err)
	}

	db, err := sql.Open(driverName, path+dsnPragmas)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConns)

	s := &Store{db: db, kr: kr, now: time.Now, pendingTouches: make(map[string]time.Time)}
	if err := s.init(ctx, path); err != nil {
		return nil, errors.Join(err, db.Close())
	}

	return s, nil
}

func (s *Store) init(ctx context.Context, path string) error {
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("store: open %s: %w", path, err)
	}

	if err := os.Chmod(path, fileMode); err != nil {
		return fmt.Errorf("store: chmod db: %w", err)
	}

	return s.migrate(ctx)
}

// Close writes the pending last_seen_at updates, then closes the database.
// A failed flush does not prevent the close; both errors are returned.
func (s *Store) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeFlushTimeout)
	defer cancel()

	return errors.Join(s.FlushTouches(ctx), s.db.Close())
}

// migrate applies every embedded migration not yet listed in
// schema_migrations, in filename order, each in its own transaction.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT    PRIMARY KEY,
		applied_at INTEGER NOT NULL
	) STRICT`); err != nil {
		return fmt.Errorf("store: create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationsFS, migrationsDir)
	if err != nil {
		return fmt.Errorf("store: list migrations: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), migrationExt) {
			names = append(names, e.Name())
		}
	}

	sort.Strings(names)

	for _, name := range names {
		if err := s.applyMigration(ctx, name); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) applyMigration(ctx context.Context, name string) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: migration %s: begin: %w", name, err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM schema_migrations WHERE version = ?`, name).Scan(&n); err != nil {
		return fmt.Errorf("store: migration %s: check: %w", name, err)
	}

	if n > 0 {
		return tx.Rollback()
	}

	body, err := migrationsFS.ReadFile(migrationsDir + "/" + name)
	if err != nil {
		return fmt.Errorf("store: migration %s: read: %w", name, err)
	}

	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("store: migration %s: %w", name, err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, name, s.now().Unix()); err != nil {
		return fmt.Errorf("store: migration %s: record: %w", name, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %s: commit: %w", name, err)
	}

	return nil
}
