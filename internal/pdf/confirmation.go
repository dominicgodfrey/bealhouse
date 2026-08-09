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
	"golang.org/x/text/encoding/charmap"

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
		in.InnName = "The Beal House"
	}

	doc := fpdf.New("P", "mm", "A4", "")
	doc.SetMargins(marginX, 18, marginX)
	doc.SetAutoPageBreak(true, 18)
	doc.AddPage()

	d := &render{doc: doc}

	doc.SetTitle(in.InnName+" booking "+in.Code, true)

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

// render is the document being built.
type render struct {
	doc *fpdf.Fpdf
}

// text writes one line, encoded. Everything that puts a string on the page goes
// through here or through note, so no call site has to remember the encoding.
func (d *render) text(width, height float64, s string, ln int, align string) {
	d.doc.CellFormat(width, height, cp1252(s), "", ln, align, false, 0, "")
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

// markOutline is the inn's mark: three connected buildings, as one closed ring
// on a 211 x 58 grid.
//
// The same outline as web/public/logo.svg, and it has to stay the same one —
// this is the fourth place the shape is drawn, after the logo, the favicon and
// the rasterised letterhead. It was traced from the owner's artwork rather than
// drawn from a description, so the numbers are measurements and not a reading
// of what the buildings look like; do not tidy them by eye.
//
// One ring rather than a pile of rectangles and triangles because the buildings
// genuinely join: the tall house's roof runs down into the long range.
var markOutline = [...][2]float64{
	{27, 0}, {30, 0}, {30, 7}, {56, 7}, {56, 0}, {60, 0}, {60, 7}, {70, 7},
	{79, 20}, {95, 20}, {95, 14}, {99, 15}, {99, 20}, {143, 20}, {147, 15},
	{169, 15}, {169, 10}, {172, 7}, {176, 7}, {178, 9}, {179, 15}, {197, 15},
	{211, 29}, {209, 30}, {209, 58}, {160, 58}, {160, 26}, {153, 19},
	{151, 19}, {151, 17}, {148, 18}, {148, 20}, {151, 21}, {151, 58},
	{71, 58}, {71, 20}, {76, 20}, {76, 19}, {73, 17}, {70, 11}, {68, 12},
	{68, 14}, {65, 16}, {65, 18}, {62, 21}, {62, 58}, {2, 58}, {2, 21},
	{0, 20}, {12, 8}, {26, 7},
}

// markAspect is the mark's width over its height, so a caller sizing by width
// does not have to know the grid.
const markAspect = 211.0 / 58.0

// mark draws the inn's three buildings, in ink.
//
// Drawn rather than embedded: a PDF wants vectors, and shipping a raster here
// would be one more copy of the shape to keep in step with the other three.
func mark(doc *fpdf.Fpdf, x, y, width float64) {
	s := width / 211
	doc.SetFillColor(ink[0], ink[1], ink[2])

	points := make([]fpdf.PointType, 0, len(markOutline))
	for _, p := range markOutline {
		points = append(points, fpdf.PointType{X: x + p[0]*s, Y: y + p[1]*s})
	}
	doc.Polygon(points, "F")
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
	d.doc.MultiCell(contentWide, 4.5, cp1252(text), "", "L", false)
}

func (d *render) line() {
	d.doc.SetDrawColor(rule[0], rule[1], rule[2])
	d.doc.SetLineWidth(0.2)
	y := d.doc.GetY()
	d.doc.Line(marginX, y, pageWidth-marginX, y)
}

// cp1252 encodes a Go string for the built-in PDF fonts.
//
// Those fonts are single-byte, so UTF-8 handed to them straight through arrives
// as mojibake — a middot became "Â·" the first time this document was looked at.
// fpdf ships a translator for exactly this, but it reads its table from a .map
// file next to the fonts, and with no font directory configured it silently
// degrades to replacing every non-ASCII character with a full stop. Which is
// worse than mojibake, because it looks deliberate: "Émilie du Châtelet" came
// out as ".milie du Ch.telet" in a document with her name at the top of it.
//
// So the table comes from x/text, which has it compiled in. cp1252 covers
// Western European names; a rune outside it becomes "?", and the day a guest
// needs more than that is the day to embed a TrueType font rather than carry one
// on the chance.
func cp1252(s string) string {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if b, ok := charmap.Windows1252.EncodeRune(r); ok {
			out = append(out, b)
			continue
		}
		out = append(out, '?')
	}
	return string(out)
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
