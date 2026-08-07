// Package console is the owner's side of the inn: everything the admin screens
// read and write, with the HTTP layer above it doing nothing but decoding a
// request and encoding an answer.
//
// # Why this is a package and not a pile of handlers
//
// The console is the only place where the invariants the rest of the system
// enforces can be walked around by accident. It creates bookings, takes rooms
// off sale, rewrites the rate calendar and moves money — the same things the
// guest-facing code does, but with a person on the other end who is allowed to.
// "Allowed to" is not "unconstrained": an owner taking a booking on the phone
// must not be able to double-book a room a guest on the website could not, and
// a season saved from a phone must go through the same generator the monthly
// job uses.
//
// So nothing here reimplements a rule. Claiming a room is occupancy.Create,
// pricing a stay is availability.Search plus pricing.Compute, refunding is
// payments.Cancel and payments.QueueRefund, regenerating the calendar is the
// SQL function in the migration, and saving email copy is email.Parse. What
// this package adds is the read models — one query per screen — and the
// transactions that hold a multi-row save together.
//
// # What it deliberately does not do
//
// It does not authenticate anybody. Every function here assumes the caller is
// the owner, because the session middleware in httpx has already established
// that and there is exactly one account. Nothing in here takes a user id except
// where one is being *recorded* — the author of a guest note — and that comes
// from the session rather than from the request body.
package console

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/payments"
)

// ErrNotFound is a record the console asked for by id or code that is not
// there.
//
// Unlike the authentication package's single ErrDenied, this one is allowed to
// be specific: the caller is already signed in, so telling them a booking code
// does not exist reveals nothing they could not learn from the list they are
// looking at.
var ErrNotFound = errors.New("console: no such record")

// BadRequest is something the owner sent that cannot be acted on, carrying the
// sentence to put on screen.
//
// A type rather than a wrapped sentinel so the reason survives to the HTTP
// layer intact. "A season cannot end before it starts" is worth saying; "400"
// is not.
type BadRequest struct{ Reason string }

func (e BadRequest) Error() string { return e.Reason }

func badf(format string, a ...any) error {
	return BadRequest{Reason: fmt.Sprintf(format, a...)}
}

// Store is what the console runs against.
//
// Both halves are needed: the query methods, because most of these operations
// are a single read, and Begin, because several are transactions over many rows
// — saving a menu, saving a season and rebuilding the calendar behind it — and a
// half-applied one of those is a menu missing its main courses or a calendar
// priced from a season that was never saved.
//
// A *pgxpool.Pool in production. In tests an already-open transaction, whose
// nested Begin is a savepoint, which is what lets this package's tests roll back
// cleanly like the rest of the suite — the same arrangement booking.Beginner
// exists for.
type Store interface {
	db.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Letterhead is what a message queued from here needs and this package cannot
// know: where the inn's own copy goes, and how to sign the guest's manage link.
//
// Both are allowed to be absent and both fail softly. No owner address sends no
// owner copy; no signer means a confirmation with no manage link in it, which is
// what a deploy without BOOKING_LINK_SECRET already gets on the payment path.
// Neither is a reason to refuse to write down a booking somebody took on the
// phone.
type Letterhead struct {
	OwnerEmail string

	// ManageURL is nil rather than a function returning "" when no secret is
	// configured, so the template checks one thing — the same choice httpx makes
	// when it fills in a Charge.
	ManageURL func(code string, expires time.Time) string

	// SiteURL is the public origin, and the only thing a payment link needs:
	// unlike the manage link it carries no capability, because the pay page is
	// already reachable by anybody holding the booking code and paying somebody
	// else's bill is not an attack anyone wants to mount. Empty means no link in
	// the message, and the template says so instead of offering a button to
	// nowhere.
	SiteURL string
}

// PayURL is where a guest settles what is outstanding on a booking.
//
// The same page the booking flow sends a guest to, rather than a second one:
// there is one way to pay for a stay at this inn, and a phone booking should not
// be a different screen with its own bugs.
func (l Letterhead) PayURL(code string) string {
	if l.SiteURL == "" {
		return ""
	}
	return strings.TrimSuffix(l.SiteURL, "/") + "/bookings/" + code + "/pay"
}

// Ops is the console's access to the inn.
type Ops struct {
	store Store
	q     *db.Queries

	// mail renders the seven messages and reads the owner's overrides. Nil is
	// possible in principle and would only cost the email-copy screen, so the
	// two methods that use it check rather than the constructor refusing.
	mail *email.Renderer

	letterhead Letterhead

	// The card processor, for the one console operation that moves money on the
	// spot: an owner keying in a card a guest is reading out. Never nil — with
	// no keys configured it is gateway.Disabled, whose every call fails, so
	// there is one way to express "payments are off" rather than a nil check
	// that is a second one.
	gateway   payments.Gateway
	stripeKey string

	// fakeGateway is the processor being the development stand-in, which the
	// console has to say out loud: the button it puts on screen takes no money.
	fakeGateway bool
}

// Processor is what the console needs to take a card at the desk.
type Processor struct {
	Gateway payments.Gateway

	// PublishableKey identifies the account to the card form in the browser.
	// Public by design and useless on its own.
	PublishableKey string

	// Fake says money cannot actually move. Carried rather than inferred, so the
	// screen that says so and the decision that made it true are the same fact.
	Fake bool
}

func New(store Store, mail *email.Renderer, letterhead Letterhead, processor Processor) *Ops {
	return &Ops{
		store:       store,
		q:           db.New(store),
		mail:        mail,
		letterhead:  letterhead,
		gateway:     processor.Gateway,
		stripeKey:   processor.PublishableKey,
		fakeGateway: processor.Fake,
	}
}

// tx runs fn inside a transaction, rolling back unless it returns nil.
//
// WithoutCancel on the rollback for the same reason booking.Create uses it: a
// request the browser abandoned should still release its locks rather than
// leave the transaction to time out holding a per-room advisory lock.
func (o *Ops) tx(ctx context.Context, fn func(*db.Queries) error) error {
	t, err := o.store.Begin(ctx)
	if err != nil {
		return fmt.Errorf("console: beginning transaction: %w", err)
	}
	defer func() { _ = t.Rollback(context.WithoutCancel(ctx)) }()

	if err := fn(db.New(t)); err != nil {
		return err
	}
	return t.Commit(ctx)
}

// ---------------------------------------------------------------------------
// Dates on the wire
// ---------------------------------------------------------------------------
//
// Civil dates cross this boundary as YYYY-MM-DD strings, exactly as they do on
// the guest side (web/src/lib/dates.ts, internal/civil). Nothing here ever hands
// the browser an instant for something that is a day at the inn: a timestamp
// would be re-rendered in the phone's timezone, and an owner in Boston looking
// at a console that says a guest arrives on the 3rd when the row says the 4th
// has no way to tell which is wrong.

// day formats a date column, or "" when it is NULL.
func day(d pgtype.Date) string {
	if !d.Valid {
		return ""
	}
	return d.Time.Format(time.DateOnly)
}

// parseDay reads a YYYY-MM-DD from a query string or a request body.
func parseDay(s string) (time.Time, error) {
	t, err := time.Parse(time.DateOnly, strings.TrimSpace(s))
	if err != nil {
		return time.Time{}, badf("%q is not a date; expected YYYY-MM-DD", s)
	}
	return t, nil
}

// optionalDay is parseDay for a field the caller may leave out.
func optionalDay(s string) (pgtype.Date, error) {
	if strings.TrimSpace(s) == "" {
		return pgtype.Date{}, nil
	}
	t, err := parseDay(s)
	if err != nil {
		return pgtype.Date{}, err
	}
	return pgtype.Date{Time: t, Valid: true}, nil
}

func dateOf(t time.Time) pgtype.Date { return pgtype.Date{Time: t, Valid: true} }

// clock formats settings.checkin_time and settings.checkout_time.
//
// Through email.Clock rather than a second formatter, because how the inn
// writes half past three is one rule: the console, the confirmation email and
// the departure-morning note must not disagree about the hour they are all
// reading out of the same column.
func clock(t pgtype.Time) string {
	return email.Clock(time.Duration(t.Microseconds) * time.Microsecond)
}

// timeOfDay is clock's inverse, for the settings screen saving one back.
func timeOfDay(s string) (pgtype.Time, error) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return pgtype.Time{}, badf("%q is not a time of day; expected HH:MM on the 24-hour clock", s)
	}
	midnight := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC)
	return pgtype.Time{
		Microseconds: int64(parsed.Sub(midnight) / time.Microsecond),
		Valid:        true,
	}, nil
}

// instant returns a nullable timestamp as a pointer, so "never" is absent from
// the JSON rather than being the zero time.
func instant(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	moment := t.Time
	return &moment
}

// notFound turns pgx's no-rows into the console's own error, so callers test
// for one thing rather than importing pgx to test for the other.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
