// Package sentry reports the inn's errors to Sentry.
//
// It is a slog.Handler and not a function anybody has to remember to call. Every
// package here already logs through slog, so wrapping the handler reports what
// is already reported and needs no audit of call sites — and a report that
// depends on somebody adding a second line beside their slog.Error is a report
// that will be missing from exactly the code paths nobody reviewed.
//
// Written against net/http rather than the Sentry SDK, on the same reasoning as
// email.Resend: one endpoint, one JSON body, one header. The SDK carries
// breadcrumbs, integrations, its own transport and its own panic recovery, and
// this binary already has panic recovery in the two places that need it — chi's
// middleware and jobs.run. What it would genuinely add is multi-frame stack
// traces, and slog.Record carries the PC of the call site, which is the frame
// that names where the error was reported from.
//
// There is no Sentry project yet, so unlike gateway.Stripe and email.Resend
// this has been pointed at the real ingest exactly once, with an invented DSN,
// and was answered 400 — which is what a well-formed envelope with credentials
// for no project gets, and is therefore the most this can be proved without an
// account. Everything that does not need one is checked against an httptest
// server: the DSN it parses, the envelope it builds, the header it signs with,
// which records it forwards, and what it does when the queue is full.
package sentry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// authVersion is the only value Sentry's ingest API has ever taken.
const authVersion = "7"

// queueDepth is how many reports may be waiting to be sent.
//
// Small on purpose. This is a buffer against a slow network, not a spool: an
// inn producing more than sixty-four unsent errors has one fault repeating, and
// the sixty-fifth copy of it is worth less than the memory. Overflow is counted
// and reported rather than hidden — see drain.
const queueDepth = 64

// DSN is the project address Sentry hands out, taken apart into the two things
// a request needs.
//
// The public key in a DSN is not a secret — it identifies the project and is
// designed to be shipped in browser bundles. It is still configuration and not
// a constant, because it names which project the inn's errors land in.
type DSN struct {
	// Endpoint is where an envelope is POSTed.
	Endpoint string
	// PublicKey goes in the X-Sentry-Auth header.
	PublicKey string
}

// ParseDSN takes https://<key>@<host>/<project> apart.
//
// A malformed DSN is an error and not a silent no-op. It is set by hand in an
// environment file, a typo in it is the likely mistake, and a reporter that
// quietly reports nothing is the one failure mode that cannot be noticed by
// watching for reports.
func ParseDSN(raw string) (DSN, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return DSN{}, fmt.Errorf("sentry: %q is not a URL: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return DSN{}, fmt.Errorf("sentry: a DSN is http(s), not %q", u.Scheme)
	}
	if u.User == nil || u.User.Username() == "" {
		return DSN{}, fmt.Errorf("sentry: the DSN has no public key before the @")
	}
	project := strings.Trim(u.Path, "/")
	if project == "" {
		return DSN{}, fmt.Errorf("sentry: the DSN names no project after the host")
	}

	return DSN{
		Endpoint:  fmt.Sprintf("%s://%s/api/%s/envelope/", u.Scheme, u.Host, project),
		PublicKey: u.User.Username(),
	}, nil
}

// Options is everything the handler needs that is not the DSN.
type Options struct {
	// Environment separates the laptop's errors from the inn's. Sentry's own
	// field, and the reason a dev run pointed at a real project is legible
	// rather than confusing.
	Environment string

	// ServerName is which box reported. One box today (decision #2), so it is
	// mostly documentation — and exactly what stops being obvious the day there
	// are two.
	ServerName string

	// MinLevel is the floor for reporting. Error by default: slog.Warn here is
	// used for conditions the system is designed to survive — no Stripe key, no
	// media directory — and every one of them would otherwise open an issue on
	// every boot.
	MinLevel slog.Level

	// Endpoint overrides the DSN's, for tests.
	Endpoint string

	// HTTPClient overrides the default, for tests.
	HTTPClient *http.Client
}

// Handler forwards records to Sentry and passes every one to the next handler.
//
// Wrapping rather than replacing: the log is the record of what happened and
// Sentry is a view of the part of it somebody should look at. A handler that
// swallowed what it reported would make the journal on the box less useful than
// it is today, which is the wrong trade for a system whose operator can ssh in.
type Handler struct {
	next   slog.Handler
	client *client

	// Attributes carried by this logger, already flattened and already
	// qualified by whatever groups were open when they were added. Qualifying
	// them then rather than at Handle time is the whole point: log.With("code",
	// x).WithGroup("http") must report `code`, not `http.code` — the group was
	// opened after the attribute and does not contain it.
	//
	// Copied rather than mutated, because slog requires the handler returned by
	// WithAttrs to be independent of its parent, and appending into a shared
	// backing array is how two loggers end up with each other's fields.
	preformatted map[string]string

	// Groups open now, which qualify the record's own attributes.
	groups []string
}

// New wraps next with a handler that reports to Sentry.
func New(next slog.Handler, dsn DSN, opts Options) *Handler {
	if opts.MinLevel == 0 {
		opts.MinLevel = slog.LevelError
	}
	endpoint := dsn.Endpoint
	if opts.Endpoint != "" {
		endpoint = opts.Endpoint
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		// Bounded on its own rather than left to a caller's context, because
		// the caller here is whoever happened to log an error. An ingest that
		// accepts a connection and then says nothing must cost this report,
		// never the request that produced it.
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}

	c := &client{
		endpoint: endpoint,
		auth: fmt.Sprintf("Sentry sentry_version=%s, sentry_client=bealhouse/1, sentry_key=%s",
			authVersion, dsn.PublicKey),
		http:     httpClient,
		env:      opts.Environment,
		server:   opts.ServerName,
		minLevel: opts.MinLevel,
		queue:    make(chan *event, queueDepth),
		stopped:  make(chan struct{}),
	}
	c.wg.Add(1)
	go c.drain()

	return &Handler{next: next, client: c}
}

func (h *Handler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.next.Enabled(ctx, l)
}

func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	carried := make(map[string]string, len(h.preformatted)+len(attrs))
	maps.Copy(carried, h.preformatted)
	for _, a := range attrs {
		putAttr(carried, h.groups, a)
	}
	return &Handler{
		next:         h.next.WithAttrs(attrs),
		client:       h.client,
		preformatted: carried,
		groups:       h.groups,
	}
}

func (h *Handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	return &Handler{
		next:         h.next.WithGroup(name),
		client:       h.client,
		preformatted: h.preformatted,
		groups:       append(append([]string(nil), h.groups...), name),
	}
}

// Handle writes the record onward and, if it is bad enough, reports it.
//
// The next handler goes first and its error is what Handle returns. Reporting
// is the addition; a Sentry project having a bad afternoon must not be able to
// cost the box its own log.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	err := h.next.Handle(ctx, r)
	if r.Level >= h.client.minLevel {
		h.client.report(h.build(r))
	}
	return err
}

// Close flushes what is queued and stops the worker. Safe to call twice.
func (h *Handler) Close() { h.client.close() }

// build turns a record into the event that will be sent.
//
// Done on the calling goroutine rather than in the worker because a slog.Record
// is not safe to hold: Attrs iterates state the caller may reuse once Handle
// returns.
func (h *Handler) build(r slog.Record) *event {
	e := &event{
		EventID:     newEventID(),
		Timestamp:   r.Time.UTC().Format(time.RFC3339Nano),
		Platform:    "go",
		Level:       sentryLevel(r.Level),
		Logger:      "slog",
		Environment: h.client.env,
		ServerName:  h.client.server,
		Extra:       make(map[string]string, len(h.preformatted)+r.NumAttrs()+1),
	}
	e.Message.Formatted = r.Message

	// Grouping is by message, which is why every extra goes in Extra and not
	// into the message. The convention throughout this repository is a static
	// slog message with the variable part in attrs — slog.Error("fatal", "err",
	// err) — and that is exactly the shape Sentry groups well: one issue per
	// thing that can go wrong, with the instances underneath it.
	maps.Copy(e.Extra, h.preformatted)
	r.Attrs(func(a slog.Attr) bool {
		putAttr(e.Extra, h.groups, a)
		return true
	})

	// One frame, and it is the right one: slog.Record carries the PC of the call
	// that produced it, so this names the line that reported the error rather
	// than slog's own internals — which is what a stack captured from inside a
	// handler would be full of.
	if r.PC != 0 {
		f, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
		if f.File != "" {
			e.Culprit = fmt.Sprintf("%s:%d", f.File, f.Line)
			e.Extra["source"] = fmt.Sprintf("%s (%s:%d)", f.Function, f.File, f.Line)
		}
	}

	return e
}

// putAttr flattens one attribute into the event's extra data.
//
// Groups become dotted prefixes rather than nested objects. Sentry renders
// extra as a flat table either way, and "http.status" beside "http.method"
// reads better there than an object somebody has to expand.
func putAttr(into map[string]string, groups []string, a slog.Attr) {
	a.Value = a.Value.Resolve()
	if a.Equal(slog.Attr{}) {
		return
	}

	key := a.Key
	if len(groups) > 0 {
		key = strings.Join(groups, ".") + "." + key
	}

	if a.Value.Kind() == slog.KindGroup {
		for _, g := range a.Value.Group() {
			putAttr(into, append(groups, a.Key), g)
		}
		return
	}

	into[key] = a.Value.String()
}

// client is the transport half: a queue, a worker, and one POST.
type client struct {
	endpoint string
	auth     string
	http     *http.Client
	env      string
	server   string
	minLevel slog.Level

	queue   chan *event
	stopped chan struct{}
	wg      sync.WaitGroup

	closeOnce sync.Once
	dropped   atomic.Int64
}

// report hands an event to the worker, or drops it.
//
// Never blocks. Handle is called from whatever goroutine logged — a request, a
// job, the shutdown path — and a reporter that waited on a network would turn a
// slow Sentry into a slow inn. A dropped event is counted; see drain.
//
// The queue is deliberately **never closed**. Shutdown is signalled by closing
// `stopped` instead, because a closed channel would make this send panic — and
// it would do so on whichever goroutine happened to log an error while the
// server was shutting down, which is precisely when errors get logged.
func (c *client) report(e *event) {
	select {
	case c.queue <- e:
	default:
		c.dropped.Add(1)
	}
}

func (c *client) drain() {
	defer c.wg.Done()

	for {
		select {
		case e := <-c.queue:
			c.send(e)

		case <-c.stopped:
			// Send what is already queued before going. These are the last
			// errors before a shutdown, which are usually the ones explaining
			// it.
			for {
				select {
				case e := <-c.queue:
					c.send(e)
				default:
					c.reportDrops()
					return
				}
			}
		}
	}
}

// reportDrops says so, once, at the end — and through stderr rather than slog,
// because a handler that logs about itself is a handler that calls itself.
func (c *client) reportDrops() {
	if n := c.dropped.Load(); n > 0 {
		fmt.Fprintf(os.Stderr, "sentry: dropped %d report(s); the queue was full\n", n)
	}
}

func (c *client) close() {
	c.closeOnce.Do(func() {
		close(c.stopped)
		c.wg.Wait()
	})
}

func (c *client) send(e *event) {
	body, err := envelope(e)
	if err != nil {
		c.complain("building an envelope: %v", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		c.complain("building the request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/x-sentry-envelope")
	req.Header.Set("X-Sentry-Auth", c.auth)

	resp, err := c.http.Do(req)
	if err != nil {
		c.complain("posting: %v", err)
		return
	}
	defer resp.Body.Close()

	// No retry. The report is the second-order thing here — the error itself is
	// already in the log on the box, which is the copy that matters — and a
	// retry loop against an ingest that is rate limiting the inn would turn one
	// bad afternoon into two.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		c.complain("ingest answered %d", resp.StatusCode)
	}
}

// complain reports the reporter's own troubles, to stderr and deliberately not
// through slog: this code runs underneath the default logger, so logging here
// would hand the message straight back to itself.
func (c *client) complain(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sentry: "+format+"\n", args...)
}

// envelope is Sentry's ingest format: a header line, an item header line, and
// the item, each terminated by a newline.
func envelope(e *event) ([]byte, error) {
	item, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	header, err := json.Marshal(map[string]any{
		"event_id": e.EventID,
		"sent_at":  time.Now().UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return nil, err
	}
	itemHeader, err := json.Marshal(map[string]any{
		"type":   "event",
		"length": len(item),
	})
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	buf.Write(header)
	buf.WriteByte('\n')
	buf.Write(itemHeader)
	buf.WriteByte('\n')
	buf.Write(item)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

// event is the subset of Sentry's event schema this sends.
type event struct {
	EventID   string `json:"event_id"`
	Timestamp string `json:"timestamp"`
	Platform  string `json:"platform"`
	Level     string `json:"level"`
	Logger    string `json:"logger"`
	Message   struct {
		Formatted string `json:"formatted"`
	} `json:"message"`
	Culprit     string `json:"culprit,omitempty"`
	Environment string `json:"environment,omitempty"`
	ServerName  string `json:"server_name,omitempty"`

	// Strings throughout, because every value here came from
	// slog.Value.String(). An error or a time reaching json.Marshal as itself
	// is either "{}" or a format nobody chose, and the value of these in an
	// issue is that a person reads them.
	Extra map[string]string `json:"extra,omitempty"`
}

// newEventID is 16 random bytes as hex, which is what the ingest expects — a
// UUID with the dashes taken out.
func newEventID() string {
	var b [16]byte
	// rand.Read is documented never to fail since Go 1.24.
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func sentryLevel(l slog.Level) string {
	switch {
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warning"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}
