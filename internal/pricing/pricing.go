// Package pricing turns nightly rates into the numbers a guest is charged.
//
// Every amount is integer cents. There are no floats anywhere in this package,
// and there should never be: a float64 cannot hold 0.1 exactly, and this is a
// business handling money.
//
// The package is deliberately pure — no database, no clock, no Stripe. Callers
// resolve dates and rates and hand in the results, which is what makes the
// arithmetic exhaustively testable in isolation.
package pricing

import "time"

// Rate is a tax rate in hundred-thousandths, mirroring settings.tax_rate's
// numeric(6,5) so the two can never drift. NH Meals & Rooms at 8.5% is
// Rate(8500).
type Rate int64

const (
	// BalanceLeadDays is how far before arrival the balance is charged
	// off-session (decision #6).
	BalanceLeadDays = 7

	// ShortNoticeDays is the window inside which there is no time left to run
	// the T-7 job, so the stay is charged in full at booking instead
	// (decision #7).
	ShortNoticeDays = 8
)

const rateScale = 100_000

// IsShortNotice reports whether an arrival is too close for the balance to be
// charged on schedule.
//
// Both arguments are civil dates. The clock stays outside this package: the
// caller resolves "today" at the inn and hands it in, which is what keeps the
// boundary exhaustively testable rather than dependent on when the test runs.
func IsShortNotice(today, checkin time.Time) bool {
	return checkin.Before(today.AddDate(0, 0, ShortNoticeDays))
}

// BalanceChargeDate is when the balance is charged off-session: T-7.
//
// Meaningful only when the arrival is not short notice — inside that window the
// date has already passed, which is exactly why those bookings are charged in
// full up front instead.
func BalanceChargeDate(checkin time.Time) time.Time {
	return checkin.AddDate(0, 0, -BalanceLeadDays)
}

// Input is everything needed to quote one stay in one room.
type Input struct {
	// NightlyCents is the price of each night, snapshotted at booking time.
	// One entry per night: a 2-night stay has two entries.
	NightlyCents []int64

	// PetFeeCents is charged once per stay, not per night, and is taxed the
	// same as the room charge.
	PetFeeCents int64

	TaxRate Rate
}

// Quote is a fully resolved set of amounts. Every field is derived, so a
// booking can snapshot the whole struct and never recompute.
//
// The tags are here rather than in a separate wire type because a quote is
// handed to the browser verbatim by three endpoints, and a second struct that
// existed only to rename these fields would be one more place for them to drift
// apart.
type Quote struct {
	Nights            int   `json:"nights"`
	RoomSubtotalCents int64 `json:"roomSubtotalCents"`
	PetFeeCents       int64 `json:"petFeeCents"`
	TaxableCents      int64 `json:"taxableCents"`
	TaxCents          int64 `json:"taxCents"`
	TotalCents        int64 `json:"totalCents"`
	DepositCents      int64 `json:"depositCents"`
	BalanceCents      int64 `json:"balanceCents"`
}

// Compute resolves an Input into a Quote.
func Compute(in Input) Quote {
	var q Quote

	q.Nights = len(in.NightlyCents)
	for _, n := range in.NightlyCents {
		q.RoomSubtotalCents += n
	}

	q.PetFeeCents = in.PetFeeCents

	// Tax is computed once over the whole base rather than per night. Rounding
	// each night separately would drift by a cent or two on longer stays, and
	// the advertised price is the all-in total, not a sum of rounded nights.
	q.TaxableCents = q.RoomSubtotalCents + q.PetFeeCents
	q.TaxCents = tax(q.TaxableCents, in.TaxRate)

	q.TotalCents = q.TaxableCents + q.TaxCents

	// The deposit is half the all-in total, rounded up. The balance is defined
	// as the remainder rather than computed independently, so deposit + balance
	// always equals the total exactly and an odd cent can never go missing.
	q.DepositCents = halfRoundedUp(q.TotalCents)
	q.BalanceCents = q.TotalCents - q.DepositCents

	return q
}

// ChargeAtBooking returns the amount to capture at checkout.
//
// Arrivals inside the balance-charge window are charged in full: there is no
// time left to run the T-7 job, so splitting the payment would leave the
// balance uncollected.
func (q Quote) ChargeAtBooking(shortNotice bool) int64 {
	if shortNotice {
		return q.TotalCents
	}
	return q.DepositCents
}

// Penalty is the amount the inn keeps when a stay is cancelled.
//
// Cancelling on time costs nothing. Cancelling late forfeits half the total,
// which is exactly the deposit — so in the ordinary case, where the balance has
// already been charged at T-7, a late cancellation refunds the balance and
// keeps the deposit.
func (q Quote) Penalty(late bool) int64 {
	if !late {
		return 0
	}
	return q.DepositCents
}

// Refund is what to return to the card, given what has actually been collected.
//
// Deriving this from amount paid rather than from the total is what makes it
// correct when the T-7 balance charge failed: the guest has paid only the
// deposit, the penalty is the deposit, and the refund is zero rather than a
// negative number the inn would try to collect.
func (q Quote) Refund(paidCents int64, late bool) int64 {
	refund := paidCents - q.Penalty(late)
	if refund < 0 {
		return 0
	}
	return refund
}

// tax rounds half up. Integer-only: base*rate stays well inside int64 for any
// plausible stay (a $10,000 booking reaches 8.5e9).
func tax(baseCents int64, rate Rate) int64 {
	return (baseCents*int64(rate) + rateScale/2) / rateScale
}

// halfRoundedUp returns ceil(n/2) for non-negative n.
func halfRoundedUp(n int64) int64 {
	return (n + 1) / 2
}
