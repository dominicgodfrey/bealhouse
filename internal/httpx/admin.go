package httpx

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"bealhouse/internal/admin"
)

// The cookies the console runs on.
//
// Both are HttpOnly, so no script on any page — including one injected into the
// admin bundle — can read them; both are SameSite=Strict, so no other site can
// cause the browser to send them at all; and both are scoped to /api/admin, so
// they never ride along on a guest's request to a public endpoint and cannot
// end up in a log line about a room search.
//
// Secure is set from the request rather than hardcoded. Hardcoded on, the
// cookie is silently dropped over plain HTTP and nobody can sign in on a
// laptop; hardcoded off, it would travel in the clear on a deployment that has
// TLS. The condition is the same isHTTPS the HSTS header uses.
const (
	sessionCookie  = "bh_admin"
	ceremonyCookie = "bh_admin_ceremony"

	// adminCookiePath keeps both cookies off every route that is not the
	// console's own API.
	adminCookiePath = "/api/admin"
)

// adminDeps is what the console's routes need.
type adminDeps struct {
	console     *admin.Console
	behindProxy bool
	siteURL     string
}

// setCookie writes one of the console's cookies with the whole policy in one
// place, so a new call site cannot quietly ship a weaker one.
func setCookie(w http.ResponseWriter, r *http.Request, behindProxy bool, name, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     adminCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   isHTTPS(r, behindProxy),
		SameSite: http.SameSiteStrictMode,
	})
}

func clearCookie(w http.ResponseWriter, r *http.Request, behindProxy bool, name string) {
	setCookie(w, r, behindProxy, name, "", -1)
}

func cookieValue(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}

// forbiddenAdmin is the one refusal the console's auth ever gives.
//
// It matches admin.ErrDenied's reasoning: a caller must not be able to tell an
// expired session from a forged one, or a spent invitation from an invented
// one, because the difference is exactly what tells them which guesses are
// worth repeating.
func forbiddenAdmin(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not signed in"})
}

// deniedEnrollment is the same refusal, worded for the page it lands on.
//
// Enrolment is the one anonymous surface where "not signed in" is true of
// everybody and tells the owner nothing about the link in their hand. This
// still says exactly one thing to expired, already spent, forged and
// never-existed alike — the wording changes, not what it distinguishes.
func deniedEnrollment(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnauthorized, map[string]string{
		"error": "that invitation is not valid; it may have expired or already been used",
	})
}

// identityKey is where RequireAdmin leaves who the caller is.
type identityKey struct{}

// currentAdmin reads what requireAdmin established. Only meaningful behind it.
func currentAdmin(r *http.Request) admin.Identity {
	id, _ := r.Context().Value(identityKey{}).(admin.Identity)
	return id
}

func contextWithIdentity(ctx context.Context, id admin.Identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// requireAdmin refuses anything without a live session.
//
// Nothing behind this reads a user id from the request. The only thing that
// says who the caller is, anywhere in the console, is the session cookie
// resolved here — which is what stops one owner's console being pointed at
// another account by editing a path.
func requireAdmin(console *admin.Console) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if console == nil {
				forbiddenAdmin(w)
				return
			}

			id, err := console.Authenticate(r.Context(), cookieValue(r, sessionCookie))
			if err != nil {
				forbiddenAdmin(w)
				return
			}

			ctx := contextWithIdentity(r.Context(), id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// sameSiteOnly is the second lock against a cross-site request forging an
// action inside a live console.
//
// SameSite=Strict on the cookie is the first, and on every browser in use it is
// enough on its own. This is here because "enough on every browser in use" is a
// statement with a date on it, and because the two checks fail in different
// ways:
//
//   - Sec-Fetch-Site is set by the browser and cannot be set by page script. A
//     request that announces itself as cross-site is refused outright.
//   - A JSON content type cannot be produced by an HTML form, which is the one
//     cross-origin request shape that needs no CORS preflight. Requiring it on
//     writes means any forged request has to pass a preflight this server never
//     answers.
//
// GET and HEAD are exempt: they change nothing, and the console's reads are
// behind the session anyway.
func sameSiteOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}

		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "cross-site requests are not accepted here",
			})
			return
		}

		// Empty body is fine — several of these endpoints take none. A body
		// that is present has to be JSON.
		if ct := r.Header.Get("Content-Type"); ct != "" {
			if mediaType := strings.TrimSpace(strings.Split(ct, ";")[0]); mediaType != "application/json" {
				writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{
					"error": "this endpoint takes application/json",
				})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// adminUnavailable answers when the console has no origin to verify a signature
// against.
//
// Registered rather than left absent, so a deploy that forgot SITE_URL gets a
// sentence explaining itself instead of the SPA's index.html and a login page
// that never works.
func adminUnavailable(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"error": "the admin console is not configured on this deployment",
	})
}

// mountAdmin registers the console's API.
func mountAdmin(api chi.Router, d adminDeps, limit func(http.Handler) http.Handler) {
	api.Route("/admin", func(ad chi.Router) {
		ad.Use(sameSiteOnly)

		if d.console == nil {
			ad.HandleFunc("/*", adminUnavailable)
			return
		}

		// Sign-in is the one anonymous surface here, so it carries its own
		// tight allowance. Not because a passkey can be guessed — it cannot —
		// but because each call writes a challenge row, and an endpoint that
		// writes on an unauthenticated request is one somebody will point a
		// loop at.
		ad.Group(func(auth chi.Router) {
			auth.Use(limit)

			auth.Post("/auth/login/begin", adminLoginBegin(d))
			auth.Post("/auth/login/finish", adminLoginFinish(d))
			auth.Post("/auth/enroll/begin", adminEnrollBegin(d))
			auth.Post("/auth/enroll/finish", adminEnrollFinish(d))
		})

		// Signing out needs no session: a caller holding a cookie that has
		// already expired still wants it cleared, and answering 401 would leave
		// a dead cookie in the browser forever.
		ad.Post("/auth/logout", adminLogout(d))

		ad.Group(func(in chi.Router) {
			in.Use(requireAdmin(d.console))

			in.Get("/me", adminMe(d))
			in.Get("/devices", adminDevices(d))
			in.Get("/passkeys", adminPasskeys(d))
			in.Post("/passkeys/invite", adminInvite(d))
			in.Delete("/passkeys/{id}", adminRevokePasskey(d))
			in.Post("/sessions/revoke-all", adminRevokeAllSessions(d))
		})
	})
}

func adminLoginBegin(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assertion, ceremony, err := d.console.BeginLogin(r.Context())
		if err != nil {
			serverError(w, r, err)
			return
		}

		setCookie(w, r, d.behindProxy, ceremonyCookie,
			base64.RawURLEncoding.EncodeToString(ceremony), 300)
		writeJSON(w, http.StatusOK, assertion)
	}
}

func adminLoginFinish(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ceremony := ceremonyFrom(r)
		clearCookie(w, r, d.behindProxy, ceremonyCookie)

		session, id, err := d.console.FinishLogin(r.Context(), ceremony, r)
		if errors.Is(err, admin.ErrDenied) {
			forbiddenAdmin(w)
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}

		signIn(w, r, d, session)
		writeJSON(w, http.StatusOK, meResponse{Name: id.Name})
	}
}

func adminEnrollBegin(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
			badRequest(w, "expected a JSON body with an enrolment token")
			return
		}

		creation, ceremony, err := d.console.BeginEnrollment(r.Context(), body.Token)
		if errors.Is(err, admin.ErrDenied) {
			deniedEnrollment(w)
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}

		setCookie(w, r, d.behindProxy, ceremonyCookie,
			base64.RawURLEncoding.EncodeToString(ceremony), 300)
		writeJSON(w, http.StatusOK, creation)
	}
}

func adminEnrollFinish(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ceremony := ceremonyFrom(r)
		clearCookie(w, r, d.behindProxy, ceremonyCookie)

		session, id, err := d.console.FinishEnrollment(r.Context(), ceremony, r)
		if errors.Is(err, admin.ErrDenied) {
			deniedEnrollment(w)
			return
		}
		if err != nil {
			serverError(w, r, err)
			return
		}

		signIn(w, r, d, session)
		writeJSON(w, http.StatusOK, meResponse{Name: id.Name})
	}
}

func adminLogout(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.console != nil {
			if err := d.console.SignOut(r.Context(), cookieValue(r, sessionCookie)); err != nil {
				serverError(w, r, err)
				return
			}
		}
		clearCookie(w, r, d.behindProxy, sessionCookie)
		w.WriteHeader(http.StatusNoContent)
	}
}

type meResponse struct {
	Name string `json:"name"`
}

func adminMe(_ adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, meResponse{Name: currentAdmin(r).Name})
	}
}

func adminDevices(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		devices, err := d.console.Devices(r.Context(), currentAdmin(r).UserID, cookieValue(r, sessionCookie))
		if err != nil {
			serverError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, devices)
	}
}

func adminPasskeys(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		keys, err := d.console.Passkeys(r.Context(), currentAdmin(r).UserID)
		if err != nil {
			serverError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, keys)
	}
}

// adminInvite mints the link that enrols the second phone.
//
// Behind the session, so the only two ways to create one are an already-trusted
// phone and shell access to the server. The response carries the URL rather
// than the bare token because the owner is about to send it to themselves and a
// naked token is a thing people paste into the wrong box.
func adminInvite(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Label string `json:"label"`
		}
		// A missing body is fine; the label has a default.
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body)

		enrollment, err := d.console.MintEnrollment(r.Context(), strings.TrimSpace(body.Label))
		if err != nil {
			serverError(w, r, err)
			return
		}

		slog.Info("an admin enrolment invitation was created", "label", enrollment.Label)

		writeJSON(w, http.StatusCreated, map[string]any{
			"url":       admin.EnrollURL(d.siteURL, enrollment.Token),
			"label":     enrollment.Label,
			"expiresAt": enrollment.ExpiresAt.Format(time.RFC3339),
		})
	}
}

func adminRevokePasskey(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := base64.RawURLEncoding.DecodeString(chi.URLParam(r, "id"))
		if err != nil {
			badRequest(w, "that is not a passkey id")
			return
		}

		err = d.console.RevokePasskey(r.Context(), currentAdmin(r).UserID, id)
		switch {
		case errors.Is(err, admin.ErrLastPasskey):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, admin.ErrDenied):
			forbiddenAdmin(w)
		case err != nil:
			serverError(w, r, err)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

// adminRevokeAllSessions signs every phone out, including this one.
//
// The button for a handset that has gone missing with the console open. It
// leaves the passkeys alone, so the owner signs straight back in on the phone
// they still have.
func adminRevokeAllSessions(d adminDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.console.SignOutEverywhere(r.Context(), currentAdmin(r).UserID); err != nil {
			serverError(w, r, err)
			return
		}
		clearCookie(w, r, d.behindProxy, sessionCookie)
		w.WriteHeader(http.StatusNoContent)
	}
}

// signIn writes the session cookie.
//
// MaxAge matches the session's own expiry, so a browser that drops the cookie
// and a server that refuses the row happen at roughly the same moment rather
// than leaving a cookie that is sent for months after it stopped working.
func signIn(w http.ResponseWriter, r *http.Request, d adminDeps, s admin.Session) {
	setCookie(w, r, d.behindProxy, sessionCookie, s.Token, int(time.Until(s.ExpiresAt).Seconds()))
}

// ceremonyFrom reads the in-progress ceremony's id.
//
// From the cookie and never from the body: the browser proves which challenge
// is its own by holding the cookie the begin step set, and a body-supplied id
// would let anyone name somebody else's ceremony.
func ceremonyFrom(r *http.Request) []byte {
	raw := cookieValue(r, ceremonyCookie)
	if raw == "" {
		return nil
	}
	id, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil
	}
	return id
}
