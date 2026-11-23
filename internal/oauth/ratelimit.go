package oauth

import (
	"errors"
	"math"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// clientCreateRate and clientCreateBurst bound how fast unauthenticated
	// callers can make this server write client rows (DCR registrations, cold
	// CIMD fetches): a real MCP client registers once per install, so 5 per
	// second with a burst of 20 never slows real use but caps a flood at
	// about 430 000 rows a day, which the never-used purge then removes.
	clientCreateRate  rate.Limit = 5
	clientCreateBurst int        = 20
	// pendingRate and pendingBurst bound GET /oauth/authorize, which writes a
	// pending authorization row without authentication. Each real sign-in
	// needs one, so 20 per second with a burst of 100 is far above real load.
	pendingRate  rate.Limit = 20
	pendingBurst int        = 100
	// minRetryAfter is the smallest Retry-After sent: the header counts whole
	// seconds and 0 would invite an immediate retry.
	minRetryAfter = time.Second
	// headerRetryAfter tells a refused caller when to try again.
	headerRetryAfter = "Retry-After"
)

// ErrClientRateLimited reports that a client lookup needed a metadata fetch
// while the global fetch budget was spent.
var ErrClientRateLimited = errors.New("oauth: too many client registrations, retry later")

// bucket is a global token bucket shared by every caller.
type bucket struct {
	// mu makes each reserve-then-cancel pair atomic: rate.Limiter's CancelAt
	// only restores a token when no later reservation exists, so two
	// interleaved refusals would leave the bucket in debt.
	mu  sync.Mutex
	lim *rate.Limiter
}

func newBucket(r rate.Limit, burst int) *bucket {
	return &bucket{lim: rate.NewLimiter(r, burst)}
}

// take spends one token at now. When none is available it spends nothing and
// returns how long until one is.
func (b *bucket) take(now time.Time) (bool, time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	res := b.lim.ReserveN(now, 1)
	if !res.OK() {
		return false, minRetryAfter
	}

	if d := res.DelayFrom(now); d > 0 {
		res.CancelAt(now)
		return false, max(d, minRetryAfter)
	}

	return true, 0
}

// retryAfterValue formats d as whole seconds, rounded up, for Retry-After.
func retryAfterValue(d time.Duration) string {
	return strconv.FormatInt(int64(math.Ceil(max(d, minRetryAfter).Seconds())), 10)
}
