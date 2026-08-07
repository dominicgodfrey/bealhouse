package httpx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"

	"bealhouse/internal/admin"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/testdb"
)

// The console's HTTP surface, attacked rather than exercised.
//
// What is worth asserting here is not that a signed-in owner can read their own
// device list. It is that each specific way in — no cookie, somebody else's
// cookie, a forged one, a cross-site form post, the SPA fallback — is closed.

// adminRouter builds a router with a real console behind it.
func adminRouter(t *testing.T) (http.Handler, *pgxpool.Pool, *db.Queries) {
	t.Helper()

	pool := testdb.Connect(t)
	q := db.New(pool)

	rp, err := admin.NewRP("https://admin.test", false)
	if err != nil {
		t.Fatalf("building the relying party: %v", err)
	}
	console, err := admin.New(rp, q, pool)
	if err != nil {
		t.Fatalf("building the console: %v", err)
	}

	h := NewRouter(Deps{
		SPA: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Beal House</title>")},
		},
		SiteURL: "https://admin.test",
		Console: console,
	})
	return h, pool, q
}

// signedIn creates an account with a phone and returns a live session token.
func signedIn(t *testing.T, pool *pgxpool.Pool, q *db.Queries) string {
	t.Helper()

	ctx := context.Background()

	handle := make([]byte, 64)
	if _, err := rand.Read(handle); err != nil {
		t.Fatalf("generating a handle: %v", err)
	}
	u, err := q.CreateUser(ctx, db.CreateUserParams{Handle: handle, Name: "Test Inn"})
	if err != nil {
		t.Fatalf("creating the account: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM users WHERE id = $1", u.ID)
	})

	credID := make([]byte, 32)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("generating a credential id: %v", err)
	}
	encoded, err := json.Marshal(webauthn.Credential{ID: credID})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if err := q.CreatePasskey(ctx, db.CreatePasskeyParams{
		ID: credID, UserID: u.ID, Label: "Test phone", Credential: encoded,
	}); err != nil {
		t.Fatalf("creating the passkey: %v", err)
	}

	// The cookie carries the token; the table carries its hash. Minted here
	// rather than through a ceremony, because the ceremony is the library's job
	// and the middleware is what is under test.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generating a token: %v", err)
	}
	sum := sha256.Sum256(raw)
	if err := q.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: sum[:], UserID: u.ID, PasskeyID: credID,
		LifetimeSeconds: 3600, UserAgent: "test",
	}); err != nil {
		t.Fatalf("creating the session: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func adminRequest(t *testing.T, h http.Handler, method, path, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var body *strings.Reader = strings.NewReader("")
	req := httptest.NewRequest(method, path, body)
	req.RemoteAddr = "203.0.113.9:44444"
	if method != http.MethodGet && method != http.MethodDelete {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if token != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Every endpoint that touches real data is behind the session. Listed
// explicitly rather than checked by walking the router, so adding a route
// without a test is a thing somebody has to do on purpose.
var protectedRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/api/admin/me"},
	{http.MethodGet, "/api/admin/devices"},
	{http.MethodGet, "/api/admin/passkeys"},
	{http.MethodPost, "/api/admin/passkeys/invite"},
	{http.MethodDelete, "/api/admin/passkeys/AAAA"},
	{http.MethodPost, "/api/admin/sessions/revoke-all"},
}

func TestEveryConsoleRouteIsClosedWithoutASession(t *testing.T) {
	h, _, _ := adminRouter(t)

	for _, route := range protectedRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			rec := adminRequest(t, h, route.method, route.path, "", nil)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("answered %d with no cookie, want 401", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "<!doctype html") {
				t.Error("the SPA answered an API route; it must not stand in for the console")
			}
		})
	}
}

// A token that is not a live session — invented, malformed, or the stored hash
// somebody read out of a backup — must be refused exactly like no token at all.
func TestForgedSessionCookiesAreRefused(t *testing.T) {
	h, pool, q := adminRouter(t)
	real := signedIn(t, pool, q)

	rawReal, err := base64.RawURLEncoding.DecodeString(real)
	if err != nil {
		t.Fatalf("decoding: %v", err)
	}
	storedHash := sha256.Sum256(rawReal)

	for _, c := range []struct{ name, token string }{
		{"invented", base64.RawURLEncoding.EncodeToString(make([]byte, 32))},
		{"not base64", "!!!!!!"},
		{"the stored hash", base64.RawURLEncoding.EncodeToString(storedHash[:])},
		{"a real token with a byte changed", flip(real)},
	} {
		t.Run(c.name, func(t *testing.T) {
			rec := adminRequest(t, h, http.MethodGet, "/api/admin/me", c.token, nil)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("answered %d, want 401", rec.Code)
			}
		})
	}

	// ...and the genuine one still works, so the test above is not passing
	// because everything is broken.
	if rec := adminRequest(t, h, http.MethodGet, "/api/admin/me", real, nil); rec.Code != http.StatusOK {
		t.Fatalf("a live session answered %d, want 200", rec.Code)
	}
}

func flip(token string) string {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) == 0 {
		return token + "x"
	}
	raw[0] ^= 0xff
	return base64.RawURLEncoding.EncodeToString(raw)
}

// The session cookie's own policy. Each of these is load-bearing and each is
// one line away from being dropped by a future edit.
func TestTheSessionCookieIsLockedDown(t *testing.T) {
	h, pool, q := adminRouter(t)
	token := signedIn(t, pool, q)

	// Logging out is the one route that writes the cookie without a full
	// WebAuthn ceremony, so it is what these can be read from.
	rec := adminRequest(t, h, http.MethodPost, "/api/admin/auth/logout", token, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout answered %d, want 204", rec.Code)
	}

	cookies := rec.Result().Cookies()
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == sessionCookie {
			found = c
		}
	}
	if found == nil {
		t.Fatal("logout did not write the session cookie back")
	}

	if !found.HttpOnly {
		t.Error("the session cookie is readable by script")
	}
	if found.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite is %v, want Strict — anything looser lets another site send it", found.SameSite)
	}
	if found.Path != adminCookiePath {
		t.Errorf("path %q, want %q so it never rides along on a guest's request", found.Path, adminCookiePath)
	}
	if found.MaxAge >= 0 {
		t.Error("logout did not expire the cookie")
	}
}

// Signing out has to actually end the session server-side, not merely drop the
// cookie — a copy of it taken beforehand must stop working too.
func TestSigningOutEndsTheSessionAndNotJustTheCookie(t *testing.T) {
	h, pool, q := adminRouter(t)
	token := signedIn(t, pool, q)

	if rec := adminRequest(t, h, http.MethodGet, "/api/admin/me", token, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup: a live session answered %d", rec.Code)
	}
	if rec := adminRequest(t, h, http.MethodPost, "/api/admin/auth/logout", token, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout answered %d", rec.Code)
	}
	if rec := adminRequest(t, h, http.MethodGet, "/api/admin/me", token, nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("the token still worked after signing out: %d", rec.Code)
	}
}

// SameSite=Strict is the real defence, but a browser announcing a cross-site
// write is refused outright as well. Sec-Fetch-Site cannot be set by page
// script, so this is not a header an attacker can simply omit from a form post
// — a form post cannot set it at all, and gets caught by the content type.
func TestCrossSiteWritesAreRefused(t *testing.T) {
	h, pool, q := adminRouter(t)
	token := signedIn(t, pool, q)

	for _, site := range []string{"cross-site", "same-site"} {
		t.Run(site, func(t *testing.T) {
			rec := adminRequest(t, h, http.MethodPost, "/api/admin/passkeys/invite", token,
				map[string]string{"Sec-Fetch-Site": site})
			if rec.Code != http.StatusForbidden {
				t.Errorf("a %s write answered %d, want 403", site, rec.Code)
			}
		})
	}

	// The console's own requests say same-origin, and they go through.
	rec := adminRequest(t, h, http.MethodPost, "/api/admin/passkeys/invite", token,
		map[string]string{"Sec-Fetch-Site": "same-origin"})
	if rec.Code != http.StatusCreated {
		t.Errorf("the console's own request answered %d, want 201", rec.Code)
	}
}

// The one cross-origin shape that needs no preflight is an HTML form, and a
// form cannot send application/json. Requiring it on writes means a forged
// request has to pass a preflight this server never answers.
func TestWritesMustBeJSON(t *testing.T) {
	h, pool, q := adminRouter(t)
	token := signedIn(t, pool, q)

	for _, contentType := range []string{
		"application/x-www-form-urlencoded",
		"multipart/form-data",
		"text/plain",
	} {
		t.Run(contentType, func(t *testing.T) {
			rec := adminRequest(t, h, http.MethodPost, "/api/admin/passkeys/invite", token,
				map[string]string{"Content-Type": contentType})
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("a %s write answered %d, want 415", contentType, rec.Code)
			}
		})
	}
}

// An invitation is a permanent way in, so minting one needs an existing phone.
func TestAnInvitationCannotBeMintedWithoutASession(t *testing.T) {
	h, _, _ := adminRouter(t)

	rec := adminRequest(t, h, http.MethodPost, "/api/admin/passkeys/invite", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("answered %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "/admin/enroll") {
		t.Fatal("an enrolment link was handed out to an anonymous caller")
	}
}

// A minted invitation comes back as a link with the token in the fragment.
func TestAMintedInvitationCarriesItsTokenInTheFragment(t *testing.T) {
	h, pool, q := adminRouter(t)
	token := signedIn(t, pool, q)

	rec := adminRequest(t, h, http.MethodPost, "/api/admin/passkeys/invite", token,
		map[string]string{"Sec-Fetch-Site": "same-origin"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("answered %d, want 201", rec.Code)
	}

	var body struct{ URL string }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	if !strings.Contains(body.URL, "#") {
		t.Errorf("url %q has no fragment; the token would be logged by every proxy", body.URL)
	}
	if strings.Contains(body.URL, "?") {
		t.Errorf("url %q puts the token in a query string", body.URL)
	}
}

// The id the list hands out must be the id the revoke route takes.
//
// Passkey.ID was a []byte, which encoding/json writes as *standard* base64 —
// padded, and with + and / in it — while DELETE /api/admin/passkeys/{id}
// decodes base64url unpadded. A 32-byte credential id always carries padding,
// so every id the console read back came straight back as "that is not a
// passkey id" and no phone could be removed from the console at all. The only
// remaining way to strike off a lost handset was shell access to the server,
// which is the thing enrolling a second phone exists to avoid needing.
//
// Asserted as a round trip rather than against a literal, because a literal is
// exactly what let the two encodings disagree unnoticed.
func TestAPasskeyIsRevokedByTheIdTheListGaveOut(t *testing.T) {
	h, pool, q := adminRouter(t)
	token := signedIn(t, pool, q)
	enrolAnotherPhone(t, q, token)

	before := listPasskeys(t, h, token)
	if len(before) != 2 {
		t.Fatalf("the account has %d passkeys, want 2", len(before))
	}

	// The spare, so the last-passkey rule is not what answers.
	var spare string
	for _, p := range before {
		if p.Label == "Spare phone" {
			spare = p.ID
		}
	}
	if spare == "" {
		t.Fatal("the spare phone is not in the list")
	}

	rec := adminRequest(t, h, http.MethodDelete, "/api/admin/passkeys/"+spare, token,
		map[string]string{"Sec-Fetch-Site": "same-origin"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoking %q answered %d (%s), want 204",
			spare, rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	after := listPasskeys(t, h, token)
	if len(after) != 1 {
		t.Fatalf("%d passkeys remain, want 1", len(after))
	}
	if after[0].ID == spare {
		t.Error("the revoked phone is still on the list")
	}
}

type listedPasskey struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func listPasskeys(t *testing.T, h http.Handler, token string) []listedPasskey {
	t.Helper()

	rec := adminRequest(t, h, http.MethodGet, "/api/admin/passkeys", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("listing passkeys answered %d, want 200", rec.Code)
	}

	var out []listedPasskey
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decoding the passkey list: %v", err)
	}
	return out
}

// enrolAnotherPhone adds a second passkey to the account behind a session.
//
// The account is found through the session rather than passed in, so the tests
// that do not care about a second phone keep signedIn's one return value.
func enrolAnotherPhone(t *testing.T, q *db.Queries, token string) {
	t.Helper()

	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("decoding the session token: %v", err)
	}
	sum := sha256.Sum256(raw)

	session, err := q.GetLiveSession(context.Background(), sum[:])
	if err != nil {
		t.Fatalf("loading the session: %v", err)
	}

	credID := make([]byte, 32)
	if _, err := rand.Read(credID); err != nil {
		t.Fatalf("generating a credential id: %v", err)
	}
	encoded, err := json.Marshal(webauthn.Credential{ID: credID})
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}
	if err := q.CreatePasskey(context.Background(), db.CreatePasskeyParams{
		ID: credID, UserID: session.UserID, Label: "Spare phone", Credential: encoded,
	}); err != nil {
		t.Fatalf("creating the second passkey: %v", err)
	}
}

// A console that is not configured must say so rather than let the SPA answer
// an API call with a page.
func TestAnUnconfiguredConsoleAnswers503(t *testing.T) {
	h := NewRouter(Deps{
		SPA: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Beal House</title>")},
		},
	})

	for _, route := range protectedRoutes {
		rec := adminRequest(t, h, route.method, route.path, "", nil)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s answered %d with no console, want 503", route.method, route.path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "<!doctype html") {
			t.Errorf("%s %s was answered by the SPA", route.method, route.path)
		}
	}
}

// Starting a sign-in writes a challenge row on an unauthenticated request, so
// it cannot be unmetered.
func TestSignInIsRateLimited(t *testing.T) {
	h, _, _ := adminRouter(t)

	var limited bool
	for range adminBurst + 5 {
		rec := adminRequest(t, h, http.MethodPost, "/api/admin/auth/login/begin", "",
			map[string]string{"Sec-Fetch-Site": "same-origin"})
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("the sign-in endpoint never rate limited; it writes a row per call")
	}
}

// Finishing a ceremony reads the id from the cookie the begin step set. A body
// that names one is not a way to point at somebody else's ceremony.
func TestFinishingWithNoCeremonyCookieIsRefused(t *testing.T) {
	h, _, _ := adminRouter(t)

	for _, path := range []string{
		"/api/admin/auth/login/finish",
		"/api/admin/auth/enroll/finish",
	} {
		rec := adminRequest(t, h, http.MethodPost, path, "",
			map[string]string{"Sec-Fetch-Site": "same-origin"})
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d with no ceremony cookie, want 401", path, rec.Code)
		}
	}
}
