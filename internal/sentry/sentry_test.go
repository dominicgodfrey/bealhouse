package sentry

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// capture is one delivery, taken apart the way the ingest would.
type capture struct {
	auth   string
	header map[string]any
	item   map[string]any
	event  map[string]any
}

// ingest stands in for Sentry. Nothing here has ever spoken to the real one, so
// what is worth asserting is the shape of the request it would receive.
func ingest(t *testing.T, status int) (*httptest.Server, <-chan capture) {
	t.Helper()
	got := make(chan capture, 16)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the envelope: %v", err)
			return
		}

		// Three newline-terminated lines: envelope header, item header, item.
		lines := bytes.Split(bytes.TrimRight(body, "\n"), []byte("\n"))
		if len(lines) != 3 {
			t.Errorf("an envelope is 3 lines, got %d: %q", len(lines), body)
			return
		}

		c := capture{auth: r.Header.Get("X-Sentry-Auth")}
		for i, into := range []*map[string]any{&c.header, &c.item, &c.event} {
			if err := json.Unmarshal(lines[i], into); err != nil {
				t.Errorf("line %d is not JSON: %v", i, err)
				return
			}
		}
		got <- c
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)

	return srv, got
}

// handler wires a Handler at an ingest, discarding what the wrapped handler
// writes so the test output stays readable.
func handler(t *testing.T, endpoint string, opts Options) (*Handler, *slog.Logger, *bytes.Buffer) {
	t.Helper()

	var logged bytes.Buffer
	next := slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug})

	opts.Endpoint = endpoint
	h := New(next, DSN{Endpoint: endpoint, PublicKey: "abc123"}, opts)
	t.Cleanup(h.Close)

	return h, slog.New(h), &logged
}

func waitFor(t *testing.T, got <-chan capture) capture {
	t.Helper()
	select {
	case c := <-got:
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("nothing reached the ingest")
		return capture{}
	}
}

func TestAnErrorIsReportedAsAnEnvelope(t *testing.T) {
	srv, got := ingest(t, http.StatusOK)
	_, log, _ := handler(t, srv.URL, Options{Environment: "production", ServerName: "beal-1"})

	log.Error("could not charge the balance", "code", "AB12CD", "err", "card declined")

	c := waitFor(t, got)

	if !strings.Contains(c.auth, "sentry_key=abc123") {
		t.Errorf("the auth header does not carry the public key: %q", c.auth)
	}
	if !strings.Contains(c.auth, "sentry_version=7") {
		t.Errorf("the auth header does not carry the version: %q", c.auth)
	}
	if c.item["type"] != "event" {
		t.Errorf("item type = %v, want event", c.item["type"])
	}
	if c.header["event_id"] != c.event["event_id"] {
		t.Errorf("the envelope header names a different event than the item")
	}

	if got, want := c.event["level"], "error"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}
	if got, want := c.event["environment"], "production"; got != want {
		t.Errorf("environment = %v, want %v", got, want)
	}
	if got, want := c.event["server_name"], "beal-1"; got != want {
		t.Errorf("server_name = %v, want %v", got, want)
	}

	// Grouping is by message, so the message must be the static one and the
	// variable part must be beside it rather than in it.
	msg, _ := c.event["message"].(map[string]any)
	if got, want := msg["formatted"], "could not charge the balance"; got != want {
		t.Errorf("message = %v, want %v", got, want)
	}
	extra, _ := c.event["extra"].(map[string]any)
	if got, want := extra["code"], "AB12CD"; got != want {
		t.Errorf("extra.code = %v, want %v", got, want)
	}
	if got, want := extra["err"], "card declined"; got != want {
		t.Errorf("extra.err = %v, want %v", got, want)
	}
}

// The point of wrapping rather than replacing: Sentry is a view of the log, not
// a replacement for it, and the copy on the box is the one an operator reads.
func TestTheLogStillGetsEverythingItWouldHave(t *testing.T) {
	srv, _ := ingest(t, http.StatusOK)
	_, log, logged := handler(t, srv.URL, Options{})

	log.Info("listening", "addr", ":8080")
	log.Error("fatal", "err", "no database")

	out := logged.String()
	for _, want := range []string{"listening", ":8080", "fatal", "no database"} {
		if !strings.Contains(out, want) {
			t.Errorf("the wrapped handler never saw %q; got %s", want, out)
		}
	}
}

// Warn is where this system says "no Stripe key" and "no media directory" —
// conditions it is designed to survive and which would otherwise open an issue
// on every boot.
func TestBelowTheFloorIsNotReported(t *testing.T) {
	srv, got := ingest(t, http.StatusOK)
	_, log, _ := handler(t, srv.URL, Options{})

	log.Debug("claiming a job")
	log.Info("database connected")
	log.Warn("Stripe is not configured; rooms can be held but not paid for")

	select {
	case c := <-got:
		t.Fatalf("reported something below Error: %v", c.event["message"])
	case <-time.After(300 * time.Millisecond):
	}
}

func TestTheFloorCanBeLowered(t *testing.T) {
	srv, got := ingest(t, http.StatusOK)
	_, log, _ := handler(t, srv.URL, Options{MinLevel: slog.LevelWarn})

	log.Warn("half a configuration")

	c := waitFor(t, got)
	if got, want := c.event["level"], "warning"; got != want {
		t.Errorf("level = %v, want %v", got, want)
	}
}

// slog.Record carries the PC of the call site, so the frame in the report names
// the line that reported the error rather than slog's own internals — which is
// all a stack captured from inside the handler would contain.
func TestTheReportNamesWhereItCameFrom(t *testing.T) {
	srv, got := ingest(t, http.StatusOK)
	_, log, _ := handler(t, srv.URL, Options{})

	log.Error("something went wrong")

	c := waitFor(t, got)
	culprit, _ := c.event["culprit"].(string)
	if !strings.Contains(culprit, "sentry_test.go") {
		t.Errorf("culprit = %q, want this test file", culprit)
	}

	extra, _ := c.event["extra"].(map[string]any)
	source, _ := extra["source"].(string)
	if !strings.Contains(source, "TestTheReportNamesWhereItCameFrom") {
		t.Errorf("source = %q, want the calling function", source)
	}
}

// A logger built with With() or WithGroup() must report the fields it carries.
// Getting this wrong loses exactly the context somebody attached on purpose.
func TestAttributesFromWithAndGroupsArrive(t *testing.T) {
	srv, got := ingest(t, http.StatusOK)
	_, log, _ := handler(t, srv.URL, Options{})

	log.With("booking", "AB12CD").WithGroup("http").Error("upstream failed", "status", 502)

	c := waitFor(t, got)
	extra, _ := c.event["extra"].(map[string]any)
	if got, want := extra["booking"], "AB12CD"; got != want {
		t.Errorf("extra.booking = %v, want %v", got, want)
	}
	if got, want := extra["http.status"], "502"; got != want {
		t.Errorf("extra[http.status] = %v, want %v", got, want)
	}
}

// Two loggers derived from one parent must not end up with each other's
// fields, which is what appending into a shared backing array does.
func TestTwoLoggersDoNotShareFields(t *testing.T) {
	srv, got := ingest(t, http.StatusOK)
	_, log, _ := handler(t, srv.URL, Options{})

	parent := log.With("shared", "yes")
	parent.With("only", "first").Error("first")
	parent.With("only", "second").Error("second")

	seen := map[string]string{}
	for range 2 {
		c := waitFor(t, got)
		msg, _ := c.event["message"].(map[string]any)
		extra, _ := c.event["extra"].(map[string]any)
		only, _ := extra["only"].(string)
		seen[msg["formatted"].(string)] = only

		if extra["shared"] != "yes" {
			t.Errorf("the parent's field is missing from %v", msg["formatted"])
		}
	}

	if seen["first"] != "first" || seen["second"] != "second" {
		t.Errorf("the two loggers' fields ran together: %v", seen)
	}
}

// Reporting must never be able to cost the process the thing it was doing.
func TestAnIngestThatRefusesCostsNothing(t *testing.T) {
	srv, got := ingest(t, http.StatusTooManyRequests)
	_, log, logged := handler(t, srv.URL, Options{})

	log.Error("still logged")
	waitFor(t, got)

	if !strings.Contains(logged.String(), "still logged") {
		t.Error("a refused report took the log entry with it")
	}
}

// The queue is a buffer against a slow network, not a spool. What matters is
// that a jammed ingest never blocks whoever logged — the drop is the designed
// outcome, and Close must still return.
func TestAJammedIngestDoesNotBlockTheCaller(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()

	h, log, logged := handler(t, srv.URL, Options{})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range queueDepth * 4 {
			log.Error("flood", "i", i)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("logging blocked on a jammed ingest")
	}

	// Every one of them still reached the log, which is the copy that matters.
	if n := strings.Count(logged.String(), `"flood"`); n != queueDepth*4 {
		t.Errorf("the log has %d of %d entries", n, queueDepth*4)
	}

	if h.client.dropped.Load() == 0 {
		t.Error("nothing was counted as dropped, so the queue silently grew")
	}

	// Let the worker out before Close waits on it, and before the deferred
	// srv.Close pulls the server out from under an in-flight request.
	close(release)
	h.Close()
}

func TestCloseIsSafeTwice(t *testing.T) {
	srv, _ := ingest(t, http.StatusOK)
	h, _, _ := handler(t, srv.URL, Options{})

	h.Close()
	h.Close() // and the t.Cleanup makes three
}

// A DSN is typed into an environment file by hand, so a typo in it is the
// likely mistake — and a reporter that quietly reports nothing is the one
// failure that cannot be noticed by watching for reports.
func TestParseDSN(t *testing.T) {
	t.Run("a real one", func(t *testing.T) {
		dsn, err := ParseDSN("https://a1b2c3@o12345.ingest.us.sentry.io/6789")
		if err != nil {
			t.Fatalf("ParseDSN: %v", err)
		}
		if got, want := dsn.PublicKey, "a1b2c3"; got != want {
			t.Errorf("PublicKey = %q, want %q", got, want)
		}
		if got, want := dsn.Endpoint, "https://o12345.ingest.us.sentry.io/api/6789/envelope/"; got != want {
			t.Errorf("Endpoint = %q, want %q", got, want)
		}
	})

	for name, raw := range map[string]string{
		"empty":       "",
		"no key":      "https://o12345.ingest.us.sentry.io/6789",
		"no project":  "https://a1b2c3@o12345.ingest.us.sentry.io",
		"not a URL":   "o12345.ingest.us.sentry.io/6789",
		"wrong shape": "sentry://a1b2c3@host/1",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseDSN(raw); err == nil {
				t.Errorf("ParseDSN(%q) was accepted", raw)
			}
		})
	}
}
