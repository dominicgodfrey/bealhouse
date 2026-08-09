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
