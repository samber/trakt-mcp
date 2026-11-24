package limits

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a manually advanced clock shared by the limiter and the test.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

func newTestLimiter(readsPerMin, writesPerMin int, clock *fakeClock) *Limiter {
	l := New(readsPerMin, writesPerMin)
	l.now = clock.Now

	return l
}

// spend takes n tokens from the user's bucket, failing if any is rejected.
func spend(t *testing.T, l *Limiter, userID string, write bool, n int) {
	t.Helper()

	for i := range n {
		if ok, _ := l.Allow(userID, write); !ok {
			t.Fatalf("%s call %d (write=%v) rejected within burst", userID, i, write)
		}
	}
}

func TestNew_BurstEqualsPerMinute(t *testing.T) {
	t.Parallel()

	l := New(120, 5)
	if got := l.readBurst; got != 120 {
		t.Errorf("readBurst = %d, want 120", got)
	}

	if got := l.writeBurst; got != 5 {
		t.Errorf("writeBurst = %d, want 5", got)
	}
}

// TestAllow_ThreePerMinuteMeansThreeNow guards against a burst below the
// per-minute count, which would turn "3 per minute" into "1 per 20 s".
func TestAllow_ThreePerMinuteMeansThreeNow(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newTestLimiter(3, 3, clock)

	for _, write := range []bool{false, true} {
		spend(t, l, "u", write, 3)

		ok, retry := l.Allow("u", write)
		if ok {
			t.Fatalf("4th call (write=%v) allowed at 3 per minute", write)
		}

		// 3 per minute refills one token every 20 s.
		if retry != 20*time.Second {
			t.Fatalf("retryAfter (write=%v) = %v, want 20s", write, retry)
		}
	}
}

func TestAllowGlobal_BurstEqualsPerMinute(t *testing.T) {
	t.Parallel()

	const globalPerMin = 30

	clock := newFakeClock()
	l := New(60, 6, WithGlobal(globalPerMin))
	l.now = clock.Now

	if got := l.globalBurst; got != globalPerMin {
		t.Fatalf("globalBurst = %d, want %d", got, globalPerMin)
	}

	for i := range globalPerMin {
		if ok, _ := l.AllowGlobal(); !ok {
			t.Fatalf("global call %d rejected within burst", i)
		}
	}

	ok, retry := l.AllowGlobal()
	if ok {
		t.Fatal("call past the per-minute burst allowed")
	}

	// 30 per minute refills one token every 2 s.
	if retry != 2*time.Second {
		t.Fatalf("retryAfter = %v, want 2s", retry)
	}

	clock.Advance(retry)

	if ok, _ := l.AllowGlobal(); !ok {
		t.Fatal("call rejected after waiting retryAfter")
	}
}

func TestAllowGlobal_UnconfiguredAlwaysAllows(t *testing.T) {
	t.Parallel()

	l := New(60, 6)
	for range 1000 {
		if ok, retry := l.AllowGlobal(); !ok || retry != 0 {
			t.Fatalf("unconfigured global bucket rejected: retry=%v", retry)
		}
	}
}

func TestWithGlobal_PanicsOnNonPositiveRate(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Error("New did not panic on a zero global rate")
		}
	}()

	New(60, 6, WithGlobal(0))
}

func TestNew_PanicsOnNonPositiveRate(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][2]int{"zero reads": {0, 1}, "negative writes": {1, -1}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("New did not panic")
				}
			}()

			New(args[0], args[1])
		})
	}
}

func TestAllow_SeparateBuckets(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	// 6 writes/min gives a write burst of 6; 60 reads/min gives a read burst of 60.
	l := newTestLimiter(60, 6, clock)

	spend(t, l, "u", true, 6)

	if ok, _ := l.Allow("u", true); ok {
		t.Fatal("7th write allowed despite burst of 6")
	}

	// Reads still have their whole burst after the write bucket is exhausted.
	spend(t, l, "u", false, 60)

	if ok, _ := l.Allow("u", false); ok {
		t.Fatal("61st read allowed despite burst of 60")
	}
}

func TestAllow_PerUserIsolation(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newTestLimiter(6, 6, clock)

	spend(t, l, "alice", true, 6)

	if ok, _ := l.Allow("alice", true); ok {
		t.Fatal("alice 7th write allowed")
	}

	if ok, _ := l.Allow("bob", true); !ok {
		t.Fatal("bob rejected because of alice's usage")
	}

	if ok, _ := l.Allow("bob", false); !ok {
		t.Fatal("bob read rejected")
	}
}

func TestAllow_RetryAfterPositive(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	// 6 writes/min: one token every 10 s, burst 6.
	l := newTestLimiter(60, 6, clock)

	ok, retry := l.Allow("u", true)
	if !ok || retry != 0 {
		t.Fatalf("first write: ok=%v retry=%v, want true 0", ok, retry)
	}

	spend(t, l, "u", true, 5)

	ok, retry = l.Allow("u", true)
	if ok {
		t.Fatal("7th write allowed")
	}

	if retry <= 0 || retry > 10*time.Second {
		t.Fatalf("retryAfter = %v, want in (0, 10s]", retry)
	}

	// Repeated rejections must not consume tokens, so retryAfter stays put.
	for range 5 {
		if ok, again := l.Allow("u", true); ok || again != retry {
			t.Fatalf("repeat rejection: ok=%v retry=%v, want false %v", ok, again, retry)
		}
	}

	clock.Advance(retry / 2)

	if ok, again := l.Allow("u", true); ok || again <= 0 || again >= retry {
		t.Fatalf("half-way: ok=%v retry=%v, want false with retry in (0, %v)", ok, again, retry)
	}

	clock.Advance(retry - retry/2)

	if ok, _ := l.Allow("u", true); !ok {
		t.Fatal("write rejected after waiting retryAfter")
	}
}

func TestAllow_ConcurrentRejectionsLeaveNoDebt(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	// 6 writes/min: one token every 10 s, burst 6.
	l := newTestLimiter(60, 6, clock)

	const (
		tokenInterval     = 10 * time.Second
		goroutines        = 64
		callsPerGoroutine = 50
	)

	spend(t, l, "u", true, 6)

	var (
		allowed atomic.Int32
		wg      sync.WaitGroup
	)
	for range goroutines {
		wg.Go(func() {
			for range callsPerGoroutine {
				if ok, _ := l.Allow("u", true); ok {
					allowed.Add(1)
				}
			}
		})
	}

	wg.Wait()

	if got := allowed.Load(); got != 0 {
		t.Fatalf("%d writes allowed on an exhausted bucket", got)
	}

	if ok, retry := l.Allow("u", true); ok || retry != tokenInterval {
		t.Fatalf("after concurrent rejections: ok=%v retry=%v, want false %v", ok, retry, tokenInterval)
	}
}

func TestWriteLock_Serializes(t *testing.T) {
	t.Parallel()

	l := New(60, 60)

	const (
		goroutines = 2
		iterations = 200
	)

	var (
		inside, maxInside atomic.Int32
		wg                sync.WaitGroup
	)
	for range goroutines {
		wg.Go(func() {
			for range iterations {
				unlock := l.WriteLock("u")
				n := inside.Add(1)

				for {
					m := maxInside.Load()
					if n <= m || maxInside.CompareAndSwap(m, n) {
						break
					}
				}

				time.Sleep(time.Microsecond)
				inside.Add(-1)
				unlock()
			}
		})
	}

	wg.Wait()

	if got := maxInside.Load(); got != 1 {
		t.Fatalf("max concurrent holders = %d, want 1", got)
	}
}

func TestWriteLock_PerUserAndIdempotentUnlock(t *testing.T) {
	t.Parallel()

	l := New(60, 60)
	unlockA := l.WriteLock("alice")

	done := make(chan struct{})

	go func() {
		unlockB := l.WriteLock("bob")
		unlockB()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bob blocked by alice's write lock")
	}

	unlockA()
	unlockA() // second call must be a no-op, not a double unlock panic.

	relock := make(chan struct{})

	go func() {
		l.WriteLock("alice")()
		close(relock)
	}()

	select {
	case <-relock:
	case <-time.After(5 * time.Second):
		t.Fatal("alice lock not released")
	}
}

func (l *Limiter) userCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return len(l.users)
}

func (l *Limiter) hasUser(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	_, ok := l.users[id]

	return ok
}

func TestEviction(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newTestLimiter(60, 6, clock)

	l.Allow("idle", true)
	l.Allow("active", false)
	unlockHeld := l.WriteLock("held")

	clock.Advance(idleTTL - time.Second)
	l.Allow("active", false)
	clock.Advance(2 * time.Second)

	l.evictIdle(clock.Now())

	if l.hasUser("idle") {
		t.Error("idle user not evicted")
	}

	if !l.hasUser("active") {
		t.Error("recently active user evicted")
	}

	if !l.hasUser("held") {
		t.Error("user with held write lock evicted")
	}

	// An evicted user starts with a fresh, full bucket.
	if ok, _ := l.Allow("idle", true); !ok {
		t.Error("evicted user rejected on return")
	}

	unlockHeld()
	clock.Advance(idleTTL + time.Second)
	l.evictIdle(clock.Now())

	if l.userCount() != 0 {
		t.Errorf("users after full idle period = %d, want 0", l.userCount())
	}
}

func TestEviction_WaiterBlocksEviction(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newTestLimiter(60, 6, clock)

	unlock := l.WriteLock("u")
	go func() {
		l.WriteLock("u")()
	}()

	deadline := time.Now().Add(5 * time.Second)
	for l.lockRefs("u") != 2 {
		if time.Now().After(deadline) {
			t.Fatal("waiter never registered")
		}

		time.Sleep(time.Millisecond)
	}

	// The entry has one holder and one waiter and has been idle past idleTTL.
	clock.Advance(idleTTL + time.Second)
	l.evictIdle(clock.Now())

	if !l.hasUser("u") {
		t.Fatal("user evicted while write lock held and waited on")
	}

	unlock()
}

func (l *Limiter) lockRefs(id string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	e, ok := l.users[id]
	if !ok {
		return -1
	}

	return e.lockRefs
}

func TestRun_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	l := New(60, 60)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		l.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
