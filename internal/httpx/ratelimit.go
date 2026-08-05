package httpx

import (
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Rate limits, per client address.
//
// The one that matters is the booking limit, and it is not about server load.
// POST /api/bookings needs no account and no payment, and every call takes a
// real room off sale for hold_ttl_minutes. There are seven rooms. Unlimited,
// a trivial script holds the entire inn indefinitely — re-firing as the sweeper
// frees each hold — and the owner watches an empty house show as fully booked.
//
// The numbers are sized for a seven-room inn rather than for a busy API. A
// couple booking two rooms, mistyping a card and trying again, stays far inside
// the booking allowance; a script does not.
const (
	// apiRate is the sustained allowance for reads. The date picker fires a
	// calendar request per month browsed, so this has to be comfortable.
	apiRate  = time.Second
	apiBurst = 40

	// bookingRate is deliberately harsh. Ten holds an hour from one address is
	// already more than any real guest does.
	bookingRate  = 6 * time.Minute
	bookingBurst = 5

	// paymentRate covers opening a payment. Looser than booking because it
	// takes nothing off sale and the processor deduplicates the calls anyway,
	// tighter than a read because each one is a round trip to somebody else's
	// API on our account. A guest reloading the pay page a few times, or
	// retrying after a declined card, stays well inside it.
	paymentRate  = 30 * time.Second
	paymentBurst = 10
)

// idleBucketTTL is how long an unused bucket is kept before the sweeper drops
// it. Long enough that a returning guest is not handed a fresh burst, short
// enough that the map tracks current traffic rather than all traffic ever.
const idleBucketTTL = time.Hour

// limiter is a token bucket per client, refilled lazily.
//
// In-process and not shared between servers, which suits a single binary on a
// single VPS (decision #2). If this ever runs on two boxes the limit becomes
// per-box, which is a reason to move it to Postgres or Caddy, not a reason to
// have skipped it.
type limiter struct {
	interval time.Duration // how long one token takes to refill
	burst    int

	mu      sync.Mutex
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	tokens int
	seen   time.Time
}

func newLimiter(interval time.Duration, burst int) *limiter {
	return &limiter{
		interval: interval,
		burst:    burst,
		buckets:  make(map[string]*bucket),
		swept:    time.Now(),
	}
}

// allow takes a token for key, reporting whether there was one.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		// A new caller starts full, so the limit is invisible to everyone
		// except whoever is hammering it.
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
	}

	// Refill for the time that has passed, then charge for this request.
	if earned := int(now.Sub(b.seen) / l.interval); earned > 0 {
		b.tokens = min(b.tokens+earned, l.burst)
		// Credit only the whole tokens taken, so a stream of requests inside
		// one interval cannot keep resetting the clock and refill for free.
		b.seen = b.seen.Add(time.Duration(earned) * l.interval)
	}
	if b.seen.After(now) {
		b.seen = now
	}

	if b.tokens <= 0 {
		return false
	}
	b.tokens--
	return true
}

// sweep drops buckets nobody has used lately. Called under the lock, and at
// most once per TTL, so it costs nothing on the hot path.
func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.swept) < idleBucketTTL {
		return
	}
	for key, b := range l.buckets {
		if now.Sub(b.seen) > idleBucketTTL {
			delete(l.buckets, key)
		}
	}
	l.swept = now
}

// rateLimit rejects callers who are asking for more than their share.
//
// 429 with Retry-After, so a client that is merely enthusiastic knows to wait
// rather than treating it as a failure of the request itself.
func rateLimit(l *limiter, behindProxy bool) func(http.Handler) http.Handler {
	retryAfter := max(int(l.interval.Seconds()), 1)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.allow(clientIP(r, behindProxy), time.Now()) {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error": "too many requests; please slow down and try again shortly",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
