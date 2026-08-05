package httpx

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// A router with no database. Every route that needs one answers 503, which is
// plenty to exercise the middleware — and lets these tests run anywhere.
func router(t *testing.T, behindProxy bool) http.Handler {
	t.Helper()
	return NewRouter(Deps{
		SPA: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Beal House</title>")},
		},
		BehindProxy: behindProxy,
	})
}

func get(t *testing.T, h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "203.0.113.7:54321"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The trap that made the Stripe webhook worth writing carefully.
//
// The SPA catches every unrouted path so client-side routes survive a refresh.
// It used to catch them for any method, so `POST /webhooks/stripe` — before
// that handler exists — returned index.html with a 200. Stripe reads 2xx as
// delivered, marks the event done and never retries, so a misconfigured or
// not-yet-deployed webhook would silently drop real payments.
func TestUnroutedPostIsNotAnswered200ByTheSPA(t *testing.T) {
	h := router(t, false)

	rec := get(t, h, http.MethodPost, "/webhooks/stripe", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("POST to an unrouted path answered %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<!doctype html") {
		t.Error("POST to an unrouted path was served the SPA")
	}

	// A GET is still a client-side route and still gets the page.
	page := get(t, h, http.MethodGet, "/rooms/rose-chamber", nil)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "<!doctype html") {
		t.Errorf("GET of a client route answered %d without the SPA", page.Code)
	}
}

func TestSecurityHeadersAreSet(t *testing.T) {
	rec := get(t, router(t, false), http.MethodGet, "/api/health", nil)

	for header, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}

	// The Payment Element is iframed from Stripe and will not load without
	// these, which is a thing to discover here rather than mid-checkout.
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{
		"default-src 'self'",
		"script-src 'self' https://js.stripe.com",
		"frame-src https://js.stripe.com",
		"connect-src 'self' https://api.stripe.com",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP is missing %q\ngot: %s", want, csp)
		}
	}
}

// HSTS on a plain-HTTP response is meaningless, and asserted before the domain
// is fully on HTTPS it locks visitors out of a site that cannot serve them.
func TestHSTSOnlyOverTLS(t *testing.T) {
	plain := get(t, router(t, true), http.MethodGet, "/api/health", nil)
	if got := plain.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS set on a plain HTTP response: %q", got)
	}

	forwarded := get(t, router(t, true), http.MethodGet, "/api/health",
		map[string]string{"X-Forwarded-Proto": "https"})
	if forwarded.Header().Get("Strict-Transport-Security") == "" {
		t.Error("no HSTS on a request Caddy forwarded over TLS")
	}

	// ...and the header is not believed when nothing trustworthy sets it.
	direct := get(t, router(t, false), http.MethodGet, "/api/health",
		map[string]string{"X-Forwarded-Proto": "https"})
	if got := direct.Header().Get("Strict-Transport-Security"); got != "" {
		t.Errorf("HSTS set from a client-supplied header with no proxy: %q", got)
	}
}

// The one that protects the owner's revenue.
//
// POST /api/bookings needs no account and no payment, and each call takes a
// room off sale for the hold TTL. With seven rooms and no limit, a loop holds
// the whole inn indefinitely.
func TestBookingsAreRateLimitedHarderThanReads(t *testing.T) {
	h := router(t, false)

	// The burst is allowed: a guest booking two rooms and retrying a mistake
	// must never see this.
	for i := range bookingBurst {
		rec := get(t, h, http.MethodPost, "/api/bookings", nil)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d of the allowed burst was rate limited", i+1)
		}
	}

	rec := get(t, h, http.MethodPost, "/api/bookings", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("a script past the burst answered %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("no Retry-After on a 429; a well-behaved client cannot tell how long to wait")
	}

	// Reads are on their own, far looser bucket and are unaffected.
	if read := get(t, h, http.MethodGet, "/api/health", nil); read.Code != http.StatusOK {
		t.Errorf("reads answered %d after the booking limit bit, want 200", read.Code)
	}
}

// Rate limiting is only worth anything if the key cannot be chosen by the
// caller. chi's middleware.RealIP took the first X-Forwarded-For entry, which
// is whatever the client sent — Caddy appends rather than replaces — so a new
// header value per request bought a fresh bucket every time.
func TestRateLimitKeyCannotBeSpoofed(t *testing.T) {
	h := router(t, true)

	// Same real client, a different invented address on every request.
	spoof := func(i int) map[string]string {
		return map[string]string{"X-Forwarded-For": "10.0.0." + string(rune('0'+i%10)) + ", 203.0.113.7"}
	}

	var limited bool
	for i := range bookingBurst + 3 {
		if get(t, h, http.MethodPost, "/api/bookings", spoof(i)).Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("a caller changed X-Forwarded-For per request and was never limited")
	}
}

// The bucket refills, or a guest who hit the limit once is locked out for the
// rest of the process's life.
func TestBucketRefillsOverTime(t *testing.T) {
	l := newLimiter(time.Second, 2)
	now := time.Now()

	if !l.allow("guest", now) || !l.allow("guest", now) {
		t.Fatal("the burst itself was refused")
	}
	if l.allow("guest", now) {
		t.Fatal("a third request inside the same instant was allowed")
	}

	if !l.allow("guest", now.Add(time.Second)) {
		t.Error("no token a full interval later")
	}

	// Refill is capped at the burst rather than accumulating forever.
	l.allow("guest", now.Add(time.Hour))
	l.allow("guest", now.Add(time.Hour))
	if l.allow("guest", now.Add(time.Hour)) {
		t.Error("an idle hour banked more than the burst")
	}
}

// A steady stream inside one interval must not keep pushing the refill clock
// forward and earn free tokens.
func TestSteadyStreamDoesNotRefillForFree(t *testing.T) {
	l := newLimiter(time.Second, 1)
	now := time.Now()

	if !l.allow("guest", now) {
		t.Fatal("the first request was refused")
	}
	for i := range 20 {
		if l.allow("guest", now.Add(time.Duration(i)*50*time.Millisecond)) {
			t.Fatalf("request at +%dms was allowed; the bucket refilled early", i*50)
		}
	}
}
