package httpx

import (
	"net"
	"net/http"
	"strings"
)

// clientIP resolves who a request is from, for rate limiting and abuse logging.
//
// chi's middleware.RealIP used to do this and was replaced, because it takes the
// **first** entry of X-Forwarded-For. That entry is whatever the client sent:
// Caddy appends the peer it saw rather than replacing the header, so a caller
// who sets `X-Forwarded-For: 1.2.3.4` arrives as `1.2.3.4, <real client>` and
// the first value is the attacker's choice. Anything keyed on it — a rate limit
// above all — is then bypassed by sending a different header each time.
//
// The last entry is the one the trusted proxy appended, so that is the one to
// read, and only when the deployment actually has a proxy in front of it.
// Without BEHIND_PROXY the header is ignored entirely: an app on a public port
// has no trustworthy way to tell a forwarded address from an invented one.
func clientIP(r *http.Request, behindProxy bool) string {
	if behindProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			hops := strings.Split(fwd, ",")
			if last := strings.TrimSpace(hops[len(hops)-1]); last != "" {
				return last
			}
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiterKey is the address as the rate limiter counts it.
//
// An IPv4 address is itself. An IPv6 address is folded onto its /64, because
// that is the smallest allocation anybody is handed — a home connection, a VPS
// — and every address inside it is one caller. Keyed on the full address, a
// caller with a /64 has 2^64 fresh buckets to walk through, and the booking
// limit that keeps a loop from holding the whole inn is worth nothing on a
// network with an AAAA record. Folding costs a household sharing a /64 one
// bucket between them, which is what an IPv4 household already gets behind
// its NAT.
//
// Anything that does not parse is used as it is: a bucket under a strange key
// is still a bucket, where dropping the request would be an outage caused by a
// malformed header.
func limiterKey(addr string) string {
	ip := net.ParseIP(addr)
	if ip == nil {
		return addr
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}

// contentSecurityPolicy is written for what the app actually loads.
//
// The Vite build emits one external module script and one stylesheet with no
// inline script, so 'self' is enough without any unsafe-inline escape hatch for
// scripts. The Stripe entries are here ahead of the Payment Element rather than
// after it: js.stripe.com serves the script, and the card fields and any 3-D
// Secure challenge are iframes from js.stripe.com and hooks.stripe.com. Stripe
// declines to work at all without those three, and finding that out while
// debugging a failed payment is a bad afternoon.
//
// style-src keeps 'unsafe-inline' because React writes style attributes, which
// this directive governs; script-src deliberately does not.
//
// openstreetmap.org is in frame-src for the About page's map: one entry, no
// key, and no third-party JavaScript on a page that also has a form on it.
// Google Maps would have wanted script-src too — a larger hole for the same pin.
const contentSecurityPolicy = "default-src 'self'; " +
	"base-uri 'self'; " +
	"object-src 'none'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'; " +
	"img-src 'self' data: https:; " +
	"font-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"script-src 'self' https://js.stripe.com; " +
	"frame-src https://js.stripe.com https://hooks.stripe.com https://www.openstreetmap.org; " +
	"connect-src 'self' https://api.stripe.com"

// permissionsPolicy switches off the browser features nothing here uses.
//
// The page has no reason to ask for a camera, a microphone or a location, and
// saying so means a script that somehow ran here could not either. Payment is
// left to this origin and Stripe's: the Payment Element's wallet buttons —
// Apple Pay, Google Pay — use the Payment Request API from inside the Stripe
// iframe, and a policy that denied it would fail those silently.
//
// Cross-Origin-Resource-Policy is deliberately not set. Its only useful value
// here would be same-origin, and the letterhead in every email is an <img>
// pointing at this origin from whatever client renders the message — which is
// exactly the cross-origin no-cors load that header refuses.
const permissionsPolicy = "camera=(), microphone=(), geolocation=(), " +
	`payment=(self "https://js.stripe.com")`

// secureHeaders sets the headers that cost nothing and prevent whole classes of
// problem.
//
// HSTS only on a request that actually arrived over TLS, which is the rule that
// keeps it from being a foot-gun: asserted on a plain-HTTP response it would be
// ignored, and asserted before the domain is fully on HTTPS it would lock
// visitors out of a site that cannot yet serve them.
func secureHeaders(behindProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
			h.Set("Content-Security-Policy", contentSecurityPolicy)
			h.Set("Permissions-Policy", permissionsPolicy)

			// Cuts the window off from anything that opened it, so a page that
			// reached this site through window.open cannot script it back.
			// allow-popups rather than plain same-origin, because a 3-D Secure
			// challenge or a wallet sheet may open one and must be able to
			// return.
			h.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")

			if isHTTPS(r, behindProxy) {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}

// isHTTPS reports whether the guest's connection was encrypted, which behind a
// TLS-terminating proxy is only knowable from the header Caddy sets.
func isHTTPS(r *http.Request, behindProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	return behindProxy && r.Header.Get("X-Forwarded-Proto") == "https"
}
