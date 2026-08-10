// Package email renders the messages the inn sends.
//
// Rendering only. Nothing here talks to Resend or opens a socket: sending is
// build-order step 5 and goes through the durable job queue, because an email
// provider having a bad afternoon must delay a confirmation rather than fail a
// booking (ARCHITECTURE.md, "The job runner").
//
// **The templates are deliberately blank.** Each one carries its subject, the
// shared shell, and a single line saying what it is for. The words a guest
// actually reads are the owner's to write, the same way room descriptions and
// photos are, and a plausible-looking draft left in place is one somebody has
// to remember to replace before launch.
package email

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/*.html
var files embed.FS

// The messages the booking engine needs. Named as constants so a typo is a
// compile error rather than a send that silently never happens.
const (
	// BookingConfirmation follows a successful payment: the stay is confirmed
	// and the guest needs their reference, dates and what was charged.
	BookingConfirmation = "booking_confirmation"

	// BalanceWarning is the T-8 heads-up that a card is about to be charged
	// (decision #6). Sending it is what stops the T-7 charge being a surprise.
	BalanceWarning = "balance_warning"

	// BalanceReceipt confirms the T-7 charge went through.
	BalanceReceipt = "balance_receipt"

	// BalanceFailed tells a guest their balance could not be taken and the inn
	// needs to hear from them.
	BalanceFailed = "balance_failed"

	// CancellationRefund confirms a cancellation and states the refund, which
	// is computed from what was collected (decisions #9 and #26).
	CancellationRefund = "cancellation_refund"

	// OwnerNotification is the inn's own copy of a new booking.
	OwnerNotification = "owner_notification"

	// CheckoutReminder reaches the guest at the start of the day they leave:
	// the checkout time and the inn's goodbye. Sent by the checkout.remind scan
	// rather than by anything the guest did, and only on the day itself.
	CheckoutReminder = "checkout_reminder"

	// PaymentRequest asks a guest to pay for a booking the owner took over the
	// telephone. The room is already theirs and nothing about it expires, so it
	// is an invoice rather than a countdown — unlike everything the booking flow
	// sends, which is all attached to a hold.
	PaymentRequest = "payment_request"
)

// Names lists every template, so a test can render them all without a list that
// drifts from the directory.
func Names() []string {
	return []string{
		BookingConfirmation,
		BalanceWarning,
		BalanceReceipt,
		BalanceFailed,
		CancellationRefund,
		OwnerNotification,
		CheckoutReminder,
		PaymentRequest,
	}
}

// Brand is the letterhead every message shares.
type Brand struct {
	// InnName is the fallback when there is no logo, and the alt text when
	// there is. An email that renders as a blank box in a client with images
	// switched off is worse than one that just says the name.
	InnName string

	// LogoURL must be absolute and publicly reachable. Mail clients do not
	// resolve relative paths, Gmail strips data: URIs from <img>, and CID
	// attachments trip spam heuristics — so a hosted file it is.
	//
	// Empty renders the inn's name as text instead, which is what a deploy with
	// no SITE_URL gets: the asset ships in the bundle, so the only thing that
	// can be missing is an origin to address it by.
	LogoURL string

	// SiteURL is the public origin, used to build links back into the site.
	SiteURL string
}

// Message is a rendered email.
type Message struct {
	Subject string
	HTML    string
}

// Renderer renders templates against one brand.
type Renderer struct {
	brand     Brand
	templates *template.Template

	// store is where the owner's edits live, and is optional. Nil renders the
	// copy that shipped with the binary and nothing else, which is what a
	// deploy with no database gets.
	store Store
}

// New parses the shipped templates once. A parse failure is a programming error
// and is returned rather than panicking, so the server can report it and start
// without the ability to send instead of dying at boot.
//
// `store` may be nil. When it is not, an owner's edit for a message wins over
// the file for it — see custom.go for where that line is drawn.
func New(brand Brand, store Store) (*Renderer, error) {
	if brand.InnName == "" {
		brand.InnName = "The Beal House"
	}

	tmpl, err := template.ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("email: parsing templates: %w", err)
	}
	return &Renderer{brand: brand, templates: tmpl, store: store}, nil
}

// data is what every template is rendered against: the caller's own values
// under .Data, with the letterhead alongside so the layout can reach it.
type data struct {
	Brand Brand
	Data  any
}

// Render produces one message.
//
// Each template file defines two blocks: `<name>_subject` and `<name>_body`.
// Keeping the subject beside the body is what stops the two describing
// different emails after somebody edits one of them, and it is why an owner's
// edit saves both halves or neither.
//
// The context is here for the lookup of that edit. A Renderer with no store
// never touches it.
func (r *Renderer) Render(ctx context.Context, name string, payload any) (Message, error) {
	// The owner's copy when they have written some, the shipped file when they
	// have not. Everything downstream of this is identical either way.
	set, err := r.custom(ctx, name)
	if err != nil {
		return Message{}, err
	}
	if set == nil {
		set = r.templates
	}

	return r.assemble(set, name, payload)
}

// assemble turns one template set and one payload into a finished message.
//
// Split out of Render so that the console's Preview reaches the same code with
// a set parsed from unsaved text. Two paths that each assembled a message would
// be two chances for the preview to show something the send does not — which is
// the one failure a preview cannot have.
func (r *Renderer) assemble(set *template.Template, name string, payload any) (Message, error) {
	in := data{Brand: r.brand, Data: payload}

	subject, err := render(set, name+"_subject", in)
	if err != nil {
		return Message{}, err
	}

	body, err := render(set, name+"_body", in)
	if err != nil {
		return Message{}, err
	}

	// The layout always comes from the shipped set, never from an edit. It
	// carries the letterhead and the table scaffolding that survives Outlook,
	// and one mistake in it would break every message the inn sends rather than
	// the one being edited.
	html, err := render(r.templates, "layout", data{Brand: r.brand, Data: template.HTML(body)})
	if err != nil {
		return Message{}, err
	}

	return Message{Subject: strings.TrimSpace(subject), HTML: html}, nil
}

func render(set *template.Template, block string, in data) (string, error) {
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, block, in); err != nil {
		return "", fmt.Errorf("email: rendering %q: %w", block, err)
	}
	return buf.String(), nil
}
