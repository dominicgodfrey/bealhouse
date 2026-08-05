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
	// Empty renders the inn's name as text instead, which is the state today:
	// no logo has been supplied, and inventing one is not ours to do.
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
}

// New parses the templates once. A parse failure is a programming error and is
// returned rather than panicking, so the server can report it and start without
// the ability to send instead of dying at boot.
func New(brand Brand) (*Renderer, error) {
	if brand.InnName == "" {
		brand.InnName = "Beal House"
	}

	tmpl, err := template.ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("email: parsing templates: %w", err)
	}
	return &Renderer{brand: brand, templates: tmpl}, nil
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
// different emails after somebody edits one of them.
func (r *Renderer) Render(name string, payload any) (Message, error) {
	in := data{Brand: r.brand, Data: payload}

	subject, err := r.render(name+"_subject", in)
	if err != nil {
		return Message{}, err
	}

	body, err := r.render(name+"_body", in)
	if err != nil {
		return Message{}, err
	}

	html, err := r.render("layout", data{Brand: r.brand, Data: template.HTML(body)})
	if err != nil {
		return Message{}, err
	}

	return Message{Subject: strings.TrimSpace(subject), HTML: html}, nil
}

func (r *Renderer) render(block string, in data) (string, error) {
	var buf bytes.Buffer
	if err := r.templates.ExecuteTemplate(&buf, block, in); err != nil {
		return "", fmt.Errorf("email: rendering %q: %w", block, err)
	}
	return buf.String(), nil
}
