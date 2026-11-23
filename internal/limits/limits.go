// Package limits enforces per-user request rates and serializes each user's
// write operations.
package limits

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// burstDivisor makes the burst one sixth of the per-minute rate, i.e. ten
	// seconds' worth of requests, so a client cannot spend a whole minute's
	// budget in one spike.
	burstDivisor = 6
	// minBurst keeps low rates usable: rate.Limiter rejects everything at burst 0.
	minBurst = 1
	// idleTTL is how long a user's buckets survive without activity; a returning
	// user after that gets fresh, full buckets, which is harmless at this scale.
	idleTTL = 10 * time.Minute
	// janitorInterval bounds how long an idle entry outlives idleTTL.
	janitorInterval = time.Minute
)

// Limiter holds a read bucket, a write bucket and a write lock per user.
// It is safe for concurrent use.
type Limiter struct {
	readLimit  rate.Limit
	writeLimit rate.Limit
	readBurst  int
	writeBurst int
	// now is replaceable so tests can drive the buckets and eviction without sleeping.
	now func() time.Time

	mu    sync.Mutex
	users map[string]*entry
}

type entry struct {
	// bucketMu makes each reserve-then-cancel pair atomic. rate.Limiter's
	// CancelAt only restores a token when no later reservation exists, so two
	// interleaved rejections would leave the bucket in debt.
	bucketMu sync.Mutex
	reads    *rate.Limiter
	writes   *rate.Limiter
	// writeMu serializes the user's writes so concurrent edits of the same
	// contact do not race on etags.
	writeMu sync.Mutex
	// lockRefs counts WriteLock holders plus waiters; guarded by Limiter.mu.
	// Eviction skips entries with lockRefs > 0 so a waiter and a later caller
	// never end up on two different mutexes.
	lockRefs int
	// lastSeen is guarded by Limiter.mu.
	lastSeen time.Time
}

// New builds a Limiter allowing readsPerMin reads and writesPerMin writes per
// user per minute. Both must be positive; config validates them, so a
// non-positive value is a programming error and panics.
func New(readsPerMin, writesPerMin int) *Limiter {
	if readsPerMin <= 0 || writesPerMin <= 0 {
		panic(fmt.Sprintf("limits: rates must be positive, got reads=%d writes=%d", readsPerMin, writesPerMin))
	}

	return &Limiter{
		readLimit:  perMinute(readsPerMin),
		writeLimit: perMinute(writesPerMin),
		readBurst:  burstFor(readsPerMin),
		writeBurst: burstFor(writesPerMin),
		now:        time.Now,
		users:      make(map[string]*entry),
	}
}

func perMinute(n int) rate.Limit {
	return rate.Limit(float64(n) / time.Minute.Seconds())
}

func burstFor(perMin int) int {
	return max(perMin/burstDivisor, minBurst)
}

// entryLocked returns the user's entry, creating it if needed, and marks it
// as seen. Callers must hold l.mu.
func (l *Limiter) entryLocked(userID string, now time.Time) *entry {
	e, ok := l.users[userID]
	if !ok {
		e = &entry{
			reads:  rate.NewLimiter(l.readLimit, l.readBurst),
			writes: rate.NewLimiter(l.writeLimit, l.writeBurst),
		}
		l.users[userID] = e
	}

	e.lastSeen = now

	return e
}

// Allow takes one token from the user's read or write bucket. When the bucket
// is empty it consumes nothing and returns the delay until the next token.
func (l *Limiter) Allow(userID string, write bool) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	e := l.entryLocked(userID, l.now())
	l.mu.Unlock()

	bucket := e.reads
	if write {
		bucket = e.writes
	}

	e.bucketMu.Lock()
	defer e.bucketMu.Unlock()
	// now is read inside the critical section so reservations stay in time order.
	now := l.now()
	// A reservation always succeeds for n=1 because burst >= 1; it reports the
	// wait, and cancelling it returns the token so rejections stay free.
	r := bucket.ReserveN(now, 1)

	delay := r.DelayFrom(now)
	if delay == 0 {
		return true, 0
	}

	r.CancelAt(now)

	return false, delay
}

// WriteLock blocks until the caller holds the user's write lock. The returned
// function releases it; calling it more than once is a no-op.
func (l *Limiter) WriteLock(userID string) (unlock func()) {
	l.mu.Lock()
	e := l.entryLocked(userID, l.now())
	e.lockRefs++
	l.mu.Unlock()

	e.writeMu.Lock()

	var once sync.Once

	return func() {
		once.Do(func() {
			e.writeMu.Unlock()
			l.mu.Lock()
			e.lockRefs--
			e.lastSeen = l.now()
			l.mu.Unlock()
		})
	}
}

// Run evicts idle users every janitorInterval until ctx is done.
func (l *Limiter) Run(ctx context.Context) {
	ticker := time.NewTicker(janitorInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.evictIdle(l.now())
		}
	}
}

// evictIdle drops users unseen for idleTTL whose write lock nobody holds or waits on.
func (l *Limiter) evictIdle(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for id, e := range l.users {
		if e.lockRefs == 0 && now.Sub(e.lastSeen) >= idleTTL {
			delete(l.users, id)
		}
	}
}
