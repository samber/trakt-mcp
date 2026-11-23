// Package jobs runs the background cleanup of spec §10: hourly removal of
// expired OAuth rows and pending authorizations, and a daily purge of
// inactive users (Google grant revoked first) and unused clients. It also
// flushes the batched last_seen_at updates every minute.
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/samber/trakt-mcp/internal/store"
)

const (
	// cleanupInterval is the spec's hourly sweep of expired rows (§10).
	cleanupInterval = time.Hour
	// purgeInterval is the spec's daily purge of inactive users (§10).
	purgeInterval = 24 * time.Hour
	// touchFlushInterval is how often accumulated last_seen_at updates reach the
	// database: one transaction a minute whatever the traffic. The inactivity
	// purge counts in days, so the lag is harmless.
	touchFlushInterval = time.Minute
	// inactivityWindow is the 180-day inactivity limit of spec §10; it also
	// matches the refresh token lifetime, so a purged user holds no live grant.
	inactivityWindow = 180 * 24 * time.Hour
	// neverUsedClientWindow purges clients that never obtained a token after a
	// day: anyone can create them without authenticating (DCR, CIMD lookups),
	// and a real client finishes its first sign-in within minutes.
	neverUsedClientWindow = 24 * time.Hour
)

// Revoker revokes a Google token; *google.Upstream implements it.
type Revoker interface {
	Revoke(ctx context.Context, token string) error
}

// Run performs the cleanup and the purge once, then every job on its
// interval, until ctx is done. The first pass runs at start so frequent
// restarts never skip the daily purge. The last_seen_at flush has no first
// pass: nothing is pending at start. It shares this goroutine with the purge,
// so a flush and a purge never overlap. now is the clock; time.Now in
// production.
func Run(ctx context.Context, st *store.Store, rv Revoker, now func() time.Time) {
	cleanup(ctx, st, now)
	purge(ctx, st, rv, now)

	flush := time.NewTicker(touchFlushInterval)
	defer flush.Stop()

	hourly := time.NewTicker(cleanupInterval)
	defer hourly.Stop()

	daily := time.NewTicker(purgeInterval)
	defer daily.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-flush.C:
			flushTouches(ctx, st)
		case <-hourly.C:
			cleanup(ctx, st, now)
		case <-daily.C:
			purge(ctx, st, rv, now)
		}
	}
}

// flushTouches writes the pending last_seen_at updates. A failure keeps them
// pending for the next tick and for the flush in Store.Close.
func flushTouches(ctx context.Context, st *store.Store) {
	if err := st.FlushTouches(ctx); err != nil {
		logError(ctx, "jobs: flush last seen", err)
	}
}

// cleanup deletes expired fosite rows and pending authorizations.
func cleanup(ctx context.Context, st *store.Store, now func() time.Time) {
	n, err := st.DeleteExpired(ctx, now())
	if err != nil {
		logError(ctx, "jobs: delete expired rows", err)
		return
	}

	slog.DebugContext(ctx, "jobs: deleted expired rows", slog.Int("count", n))
}

// purge deletes users and clients unused for inactivityWindow.
func purge(ctx context.Context, st *store.Store, rv Revoker, now func() time.Time) {
	cutoff := now().Add(-inactivityWindow)

	users, err := st.InactiveUsers(ctx, cutoff)
	if err != nil {
		logError(ctx, "jobs: list inactive users", err)
	}

	for _, u := range users {
		if ctx.Err() != nil {
			return
		}

		purgeUser(ctx, st, rv, u.ID, cutoff)
	}

	n, err := st.PurgeUnusedClients(ctx, cutoff, now().Add(-neverUsedClientWindow))
	if err != nil {
		logError(ctx, "jobs: purge unused clients", err)
		return
	}

	if n > 0 {
		slog.InfoContext(ctx, "jobs: purged unused clients", slog.Int("count", n))
	}
}

// purgeUser deletes an inactive user, then revokes the Google grant read
// just before. The delete is guarded: a user who reconnected since
// InactiveUsers ran (newer last_seen_at, or a new refresh token) is kept, and
// their grant is not revoked. Revoking after the delete means the token
// revoked is exactly the one deleted. A token that cannot be read (sealed
// under a retired key) is not revoked and does not block the purge: the local
// data must go regardless. A failed revoke only warns, for the same reason;
// the user can still revoke from their Google account page.
func purgeUser(ctx context.Context, st *store.Store, rv Revoker, userID string, cutoff time.Time) {
	refresh := ""
	// expected is nullable: nil deletes without comparing the stored token.
	expected := &refresh

	tok, err := st.GoogleToken(ctx, userID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		slog.WarnContext(ctx, "jobs: read google token, deleting user without revoking",
			slog.String("user_id", userID), slog.Any("error", err))

		expected = nil
	default:
		refresh = tok.RefreshToken
	}

	deleted, err := st.DeleteUserIfInactive(ctx, userID, cutoff, expected)
	if err != nil {
		logError(ctx, "jobs: delete inactive user", err, slog.String("user_id", userID))
		return
	}

	if !deleted {
		slog.InfoContext(ctx, "jobs: user active again, not purged", slog.String("user_id", userID))
		return
	}

	slog.InfoContext(ctx, "user disconnected", slog.String("user_id", userID), slog.String("reason", "inactive"))

	if refresh == "" {
		return
	}

	if err := rv.Revoke(ctx, refresh); err != nil {
		slog.WarnContext(ctx, "jobs: google revoke failed after deleting the user",
			slog.String("user_id", userID), slog.Any("error", err))
	}
}

// logError logs a job failure, except the expected one when shutdown
// cancels a job mid-flight.
func logError(ctx context.Context, msg string, err error, attrs ...any) {
	if ctx.Err() != nil {
		return
	}

	slog.ErrorContext(ctx, msg, append(attrs, slog.Any("error", err))...)
}
