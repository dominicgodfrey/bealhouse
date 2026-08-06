// Package pdf renders the documents a guest keeps.
//
// Pure, like internal/pricing: no database, no clock, no HTTP. The caller
// assembles what the document says and this decides how it looks, which is what
// makes the layout testable without a booking behind it.
//
// Every amount arrives as integer cents and is formatted here. Nothing in this
// package does arithmetic on money — the figures are the booking's snapshot and
// a document that recomputed them could disagree with the email beside it.
package pdf

import (
	"bytes"
	"fmt"
	"strconv"
	"time"

	"github.com/go-pdf/fpdf"

	"bealhouse/internal/email"
)

// Night is one night of a stay at the price it was booked at.
type Night struct {
	Date        time.Time
	AmountCents int64
}

// Confirmation is everything the document says.
type Confirmation struct {
	InnName  string
	Code     string
	Guest    string
	RoomName string

	Checkin  time.Time
	Checkout time.Time
	Nights   []Night
	Guests   int

	PetFeeCents int64
	TaxCents    int64
	TotalCents  int64

	// PaidCents is the gross collected. BalanceCents and BalanceChargeOn are
	// zero and zero on a stay paid in full at booking (decision #7), which is how
	// the document tells the two apart without being told which it is rendering.
	PaidCents       int64
	BalanceCents    int64
	BalanceChargeOn time.Time
}

// Page geometry, in millimetres. A4 because the inn is more likely to email this
// than print it, and A4 is what a phone and a European printer both expect.
const (
	marginX     = 20.0
	pageWidth   = 210.0
	contentWide = pageWidth - 2*marginX
)

var (
	ink   = [3]int{28, 25, 23} // #1c1917, the same ink as the site and the email
	faint = [3]int{120, 113, 108}
	rule  = [3]int{231, 229, 228}
)

// Render produces the confirmation as PDF bytes.
func Render(in Confirmation) ([]byte, error) {
	if in.InnName == "" {
		in.InnName = "Beal House"
	}

	doc := fpdf.New("P", "mm", "A4", "")
	doc.SetMargins(marginX, 18, marginX)
	doc.SetAutoPageBreak(true, 18)
	doc.AddPage()

	// The built-in fonts are single-byte, so Go's UTF-8 has to be translated on
	// the way in or every non-ASCII character arrives as mojibake — which it did,
	// visibly, on the separator in the footer. cp1252 covers Western European
	// names, which is what a New Hampshire inn's guest list is; anything outside
	// it would need an embedded TrueType font, and that is a real change to make
	// when a guest turns up needing one rather than a megabyte carried on the
	// chance.
	d := &render{doc: doc, tr: doc.UnicodeTranslatorFromDescriptor("")}

	doc.SetTitle(d.tr(in.InnName+" booking "+in.Code), true)

	d.letterhead(in)
	d.summary(in)
	d.nightly(in)
	d.payment(in)
	d.footer(in)

	var out bytes.Buffer
	if err := doc.Output(&out); err != nil {
		return nil, fmt.Errorf("pdf: rendering booking %s: %w", in.Code, err)
	}
	return out.Bytes(), nil
}

// render is the document being built and the encoder every string goes through.
type render struct {
	doc *fpdf.Fpdf
	tr  func(string) string
}

// text writes one line, translated. Everything that puts a string on the page
// goes through here, so no call site has to remember the encoding.
func (d *render) text(width, height float64, s string, ln int, align string) {
	d.doc.CellFormat(width, height, d.tr(s), "", ln, align, false, 0, "")
}

// letterhead draws the mark and the reference.
func (d *render) letterhead(in Confirmation) {
	mark(d.doc, marginX, 18, 34)

	d.doc.SetXY(marginX, 32)
	d.doc.SetTextColor(ink[0], ink[1], ink[2])
	d.doc.SetFont("Times", "", 20)
	d.text(contentWide/2, 8, in.InnName, 0, "L")

	// The reference, right-aligned and the largest thing on the page after the
	// name. It is what a guest is asked for at the door and what they quote in
	// an email, so it has to be findable at arm's length.
	d.doc.SetFont("Courier", "B", 16)
	d.text(contentWide/2, 8, in.Code, 1, "R")

	d.doc.SetFont("Helvetica", "", 9)
	d.doc.SetTextColor(faint[0], faint[1], faint[2])
	d.doc.SetX(marginX)
	d.text(contentWide, 5, "Booking confirmation", 1, "L")

	d.doc.Ln(4)
	d.line()
	d.doc.Ln(6)
}

// mark draws the inn's three buildings, in ink.
//
// Drawn rather than embedded: the shape is a dozen rectangles and triangles, so
// vector primitives here beat shipping a raster and keeping it in step with
// web/public/logo.svg — which is the same geometry, on the same 316 x 108 grid.
func mark(doc *fpdf.Fpdf, x, y, width float64) {
	s := width / 316
	doc.SetFillColor(ink[0], ink[1], ink[2])

	rect := func(rx, ry, rw, rh float64) {
		doc.Rect(x+rx*s, y+ry*s, rw*s, rh*s, "F")
	}
	roof := func(x1, y1, x2, y2, x3, y3 float64) {
		doc.Polygon([]fpdf.PointType{
			{X: x + x1*s, Y: y + y1*s},
			{X: x + x2*s, Y: y + y2*s},
			{X: x + x3*s, Y: y + y3*s},
		}, "F")
	}

	// The tall house, two chimneys.
	rect(30, 16, 6, 42)
	rect(64, 16, 6, 42)
	roof(6, 56, 54, 24, 102, 56)
	rect(20, 55, 68, 45)

	// The long low range.
	rect(134, 22, 6, 46)
	roof(106, 66, 162, 40, 218, 66)
	rect(118, 65, 88, 35)

	// The house with the cupola.
	doc.Circle(x+266*s, y+22*s, 8*s, "F")
	roof(222, 58, 266, 30, 310, 58)
	rect(234, 57, 64, 43)
}

// summary is who the stay is for and when.
func (d *render) summary(in Confirmation) {
	pairs := [][2]string{
		{"Guest", in.Guest},
		{"Room", in.RoomName},
		{"Arriving", day(in.Checkin)},
		{"Departing", day(in.Checkout)},
		{"Nights", strconv.Itoa(len(in.Nights))},
		{"Party", plural(in.Guests, "guest", "guests")},
	}

	d.doc.SetFont("Helvetica", "", 10)
	for _, p := range pairs {
		d.doc.SetTextColor(faint[0], faint[1], faint[2])
		d.text(34, 6, p[0], 0, "L")

		d.doc.SetTextColor(ink[0], ink[1], ink[2])
		d.text(contentWide-34, 6, p[1], 1, "L")
	}

	d.doc.Ln(4)
}

// nightly is the per-night breakdown, then the fee, the tax and the total.
//
// Spelled out per night rather than summarised, because it is the answer to the
// only pricing question a guest ever asks after the fact — why that number — and
// it is the snapshot taken when they booked, which no later rate edit changes.
func (d *render) nightly(in Confirmation) {
	d.heading("Your stay")

	var room int64
	d.doc.SetFont("Helvetica", "", 10)
	for _, n := range in.Nights {
		room += n.AmountCents
		d.row(day(n.Date), money(n.AmountCents), false)
	}

	d.line()
	d.doc.Ln(1)

	d.row("Room", money(room), false)
	if in.PetFeeCents > 0 {
		d.row("Pet fee", money(in.PetFeeCents), false)
	}
	d.row("Tax", money(in.TaxCents), false)
	d.row("Total", money(in.TotalCents), true)

	d.doc.Ln(4)
}

// payment is what has been taken and what has not.
func (d *render) payment(in Confirmation) {
	d.heading("Payment")

	d.doc.SetFont("Helvetica", "", 10)
	d.row("Paid", money(in.PaidCents), false)

	if in.BalanceCents <= 0 {
		d.row("Outstanding", money(0), true)
		d.doc.Ln(2)
		d.note("This stay is paid in full.")
		return
	}

	d.row("Balance", money(in.BalanceCents), true)
	d.doc.Ln(2)

	// The one sentence in this document a guest is likely to be surprised by, so
	// it says the date and says it is automatic (decision #6). They are also
	// emailed the day before.
	if !in.BalanceChargeOn.IsZero() {
		d.note("The balance is charged automatically to the card you paid with on " +
			day(in.BalanceChargeOn) + ". We will email you the day before.")
	}
}

func (d *render) footer(in Confirmation) {
	d.doc.Ln(6)
	d.line()
	d.doc.Ln(3)

	d.doc.SetFont("Helvetica", "", 8)
	d.doc.SetTextColor(faint[0], faint[1], faint[2])
	d.text(contentWide, 4,
		in.InnName+" · Littleton, New Hampshire · reference "+in.Code, 1, "L")
}

func (d *render) heading(s string) {
	d.doc.SetFont("Helvetica", "B", 10)
	d.doc.SetTextColor(ink[0], ink[1], ink[2])
	d.text(contentWide, 6, s, 1, "L")
	d.doc.Ln(1)
}

// row is a label on the left and an amount right-aligned against the margin.
func (d *render) row(label, amount string, strong bool) {
	style := ""
	if strong {
		style = "B"
	}
	d.doc.SetFont("Helvetica", style, 10)
	d.doc.SetTextColor(ink[0], ink[1], ink[2])

	d.text(contentWide-32, 5.5, label, 0, "L")
	d.text(32, 5.5, amount, 1, "R")
}

func (d *render) note(text string) {
	d.doc.SetFont("Helvetica", "", 9)
	d.doc.SetTextColor(faint[0], faint[1], faint[2])
	d.doc.MultiCell(contentWide, 4.5, d.tr(text), "", "L", false)
}

func (d *render) line() {
	d.doc.SetDrawColor(rule[0], rule[1], rule[2])
	d.doc.SetLineWidth(0.2)
	y := d.doc.GetY()
	d.doc.Line(marginX, y, pageWidth-marginX, y)
}

// money and day are the email package's, not copies of them.
//
// How the inn writes an amount or a date to a guest is one rule, and a document
// that spelled either differently from the message beside it would look like two
// systems. The import goes this way round because email owns the templates those
// rules were written for; it does not know this package exists.
func money(cents int64) string { return email.Money(cents) }
func day(d time.Time) string   { return email.Day(d) }

func plural(n int, one, many string) string {
	if n == 1 {
		return strconv.Itoa(n) + " " + one
	}
	return strconv.Itoa(n) + " " + many
}
