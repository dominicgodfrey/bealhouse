package pdf

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func stay() Confirmation {
	start := time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)
	return Confirmation{
		Code:     "BH-K4M27P",
		Guest:    "Ada Lovelace",
		RoomName: "Rose Chamber",
		Checkin:  start,
		Checkout: start.AddDate(0, 0, 3),
		Guests:   2,
		Nights: []Night{
			{Date: start, AmountCents: 24500},
			{Date: start.AddDate(0, 0, 1), AmountCents: 24500},
			{Date: start.AddDate(0, 0, 2), AmountCents: 28000},
		},
		PetFeeCents:     5000,
		TaxCents:        6970,
		TotalCents:      88970,
		PaidCents:       44485,
		BalanceCents:    44485,
		BalanceChargeOn: start.AddDate(0, 0, -7),
	}
}

func rendered(t *testing.T, in Confirmation) []byte {
	t.Helper()
	out, err := Render(in)
	if err != nil {
		t.Fatalf("rendering: %v", err)
	}
	return out
}

func TestRenderProducesAPDF(t *testing.T) {
	out := rendered(t, stay())

	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Errorf("output does not start with a PDF header: %q", out[:min(8, len(out))])
	}
	if !bytes.Contains(out, []byte("%%EOF")) {
		t.Error("output has no PDF trailer")
	}
	if len(out) < 1000 {
		t.Errorf("output is %d bytes, which is too small to be the document", len(out))
	}
}

// The document renders for a stay with nothing in it rather than panicking on a
// nil slice or a zero date. It is reached from a URL, so the shapes it can be
// asked for are not all ones a booking produces.
func TestRenderSurvivesAnEmptyStay(t *testing.T) {
	out := rendered(t, Confirmation{Code: "BH-EMPTY"})
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Error("an empty confirmation did not produce a PDF")
	}
}

// Names outside ASCII must not turn into mojibake on the page. The built-in
// fonts are single-byte, so this only works because Render translates on the way
// in — and the failure mode without it is silent and visible only to the guest
// whose name it is.
func TestAnAccentedNameIsNotMangled(t *testing.T) {
	in := stay()
	in.Guest = "Émilie du Châtelet"

	out := rendered(t, in)

	// The raw UTF-8 bytes must not survive into the content stream: seeing them
	// there is exactly what "not translated" looks like.
	if bytes.Contains(out, []byte("Châtelet")) {
		t.Error("the name reached the page as raw UTF-8 rather than cp1252")
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Error("an accented name broke the document")
	}
}

// The two payment states are genuinely different documents, not the same one
// with a different number: a stay paid in full says so and promises no charge.
func TestPaidInFullAndBalanceOutstandingDiffer(t *testing.T) {
	outstanding := stay()

	full := stay()
	full.PaidCents = full.TotalCents
	full.BalanceCents = 0
	full.BalanceChargeOn = time.Time{}

	if bytes.Equal(rendered(t, outstanding), rendered(t, full)) {
		t.Error("a stay paid in full renders identically to one with a balance due")
	}
}

func TestMoneyAndDatesReadTheWayTheEmailDoes(t *testing.T) {
	if got := money(88970); got != "$889.70" {
		t.Errorf("money(88970) = %q", got)
	}
	if got := money(0); got != "$0.00" {
		t.Errorf("money(0) = %q", got)
	}
	if got := day(time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)); !strings.HasPrefix(got, "Friday") {
		t.Errorf("day() = %q, want the weekday spelled out", got)
	}
}

func TestPlural(t *testing.T) {
	if got := plural(1, "guest", "guests"); got != "1 guest" {
		t.Errorf("plural(1) = %q", got)
	}
	if got := plural(2, "guest", "guests"); got != "2 guests" {
		t.Errorf("plural(2) = %q", got)
	}
}
