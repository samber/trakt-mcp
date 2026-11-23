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

func TestNew_BurstIsSixthOfPerMinuteWithFloorOfOne(t *testing.T) {
	t.Parallel()

	l := New(120, 5)
	if got := l.readBurst; got != 20 {
		t.Errorf("readBurst = %d, want 20", got)
	}

	if got := l.writeBurst; got != 1 {
		t.Errorf("writeBurst = %d, want 1", got)
	}
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
	// 6 writes/min gives a write burst of 1; 60 reads/min gives a read burst of 10.
	l := newTestLimiter(60, 6, clock)

	if ok, _ := l.Allow("u", true); !ok {
		t.Fatal("first write rejected")
	}

	if ok, _ := l.Allow("u", true); ok {
		t.Fatal("second write allowed despite burst of 1")
	}

	for i := range 10 {
		if ok, _ := l.Allow("u", false); !ok {
			t.Fatalf("read %d rejected after write bucket was exhausted", i)
		}
	}

	if ok, _ := l.Allow("u", false); ok {
		t.Fatal("11th read allowed despite burst of 10")
	}
}

func TestAllow_PerUserIsolation(t *testing.T) {
	t.Parallel()

	clock := newFakeClock()
	l := newTestLimiter(6, 6, clock)

	if ok, _ := l.Allow("alice", true); !ok {
		t.Fatal("alice first write rejected")
	}

	if ok, _ := l.Allow("alice", true); ok {
		t.Fatal("alice second write allowed")
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
	// 6 writes/min: one token every 10 s, burst 1.
	l := newTestLimiter(60, 6, clock)

	ok, retry := l.Allow("u", true)
	if !ok || retry != 0 {
		t.Fatalf("first write: ok=%v retry=%v, want true 0", ok, retry)
	}

	ok, retry = l.Allow("u", true)
	if ok {
		t.Fatal("second write allowed")
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
	// 6 writes/min: one token every 10 s, burst 1.
	l := newTestLimiter(60, 6, clock)

	const (
		tokenInterval     = 10 * time.Second
		goroutines        = 64
		callsPerGoroutine = 50
	)

	if ok, _ := l.Allow("u", true); !ok {
		t.Fatal("first write rejected")
	}

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
