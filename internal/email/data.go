package email

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The values each message is rendered against.
//
// One type per message rather than a union with half its fields blank: these
// are the vocabulary somebody writing the copy has to work from, and a struct
// that only sometimes has a `ChargeOn` teaches them nothing about which.
//
// **Money and dates arrive pre-formatted.** An Envelope's Data goes through the
// jobs table as JSON and comes back as `map[string]any`, which turns every
// number into a float64 — and integer cents becoming a float, anywhere, is the
// one rule this system does not bend (CLAUDE.md). Formatting in Go on the way in
// keeps the arithmetic integral and makes the round trip lossless. The cost is
// that a queued message renders with the formatting it was queued with; that
// buys correctness for a difference measured in minutes.
//
// The json tags match the field names on purpose, so `{{.Data.Code}}` reads the
// same before the round trip and after it.

// BookingConfirmationData follows a successful payment. The stay is confirmed,
// the room is the guest's, and this is the message they keep.
type BookingConfirmationData struct {
	Code      string `json:"Code"`
	GuestName string `json:"GuestName"`

	// Rooms is what was booked, by name. A slice because the schema has carried
	// booking_rooms since day one (decision #10) even though the v1 UI books
	// one at a time.
	Rooms []string `json:"Rooms"`

	Checkin  string `json:"Checkin"`
	Checkout string `json:"Checkout"`

	// Nights is a string like everything else here. A number would come back
	// from the queue as a float64 and start a habit this system does not want.
	Nights string `json:"Nights"`

	// PaidNow is what has just been taken, and Total what the stay costs.
	PaidNow string `json:"PaidNow"`
	Total   string `json:"Total"`

	// BalanceDue and BalanceChargeOn are empty on a stay paid in full at
	// booking (decision #7), which is how a template tells the two apart
	// without being told which kind of booking it is rendering.
	BalanceDue      string `json:"BalanceDue"`
	BalanceChargeOn string `json:"BalanceChargeOn"`

	// ManageURL is the signed link to view and cancel the stay (decision #19).
	// Absolute, expiring, and empty on a deploy with no signing secret — so the
	// template has to check before offering it.
	ManageURL string `json:"ManageURL"`
}

// OwnerNotificationData is the inn's own copy of a new booking.
//
// It carries the guest's contact details, which the guest's own confirmation
// does not need and the public booking API deliberately never returns.
type OwnerNotificationData struct {
	Code       string   `json:"Code"`
	GuestName  string   `json:"GuestName"`
	GuestEmail string   `json:"GuestEmail"`
	Rooms      []string `json:"Rooms"`
	Checkin    string   `json:"Checkin"`
	Checkout   string   `json:"Checkout"`
	Nights     string   `json:"Nights"`
	PaidNow    string   `json:"PaidNow"`
	Total      string   `json:"Total"`
}

// BalanceWarningData is the T-8 heads-up: decision #6's promise that the T-7
// charge is never a surprise.
type BalanceWarningData struct {
	Code      string `json:"Code"`
	GuestName string `json:"GuestName"`

	// Amount is what will be taken, formatted: "$412.50".
	Amount string `json:"Amount"`

	// ChargeOn is the day it will be taken — the whole point of the message.
	ChargeOn string `json:"ChargeOn"`

	Checkin  string `json:"Checkin"`
	Checkout string `json:"Checkout"`
}

// CancellationRefundData confirms a cancellation and states the refund.
//
// Used for decision #24 — a stay the inn could not honour, refunded in full —
// and, once self-service cancellation lands in step 5, for a guest who changed
// their mind, where the figure comes from pricing.Refund instead.
type CancellationRefundData struct {
	Code      string `json:"Code"`
	GuestName string `json:"GuestName"`

	// Refunded is what is going back, already formatted.
	Refunded string `json:"Refunded"`

	Checkin  string `json:"Checkin"`
	Checkout string `json:"Checkout"`
}

// BalanceReceiptData confirms the T-7 charge went through. The guest was warned
// a day earlier and this is what closes that loop.
type BalanceReceiptData struct {
	Code      string `json:"Code"`
	GuestName string `json:"GuestName"`

	// Amount is what was just taken; Total is what the stay cost altogether.
	Amount string `json:"Amount"`
	Total  string `json:"Total"`

	Checkin  string `json:"Checkin"`
	Checkout string `json:"Checkout"`
}

// BalanceFailedData tells a guest their card was refused and the inn needs to
// hear from them. The stay is not cancelled — they are still arriving, there is
// just money outstanding.
type BalanceFailedData struct {
	Code      string `json:"Code"`
	GuestName string `json:"GuestName"`

	// Outstanding is what is still owed.
	Outstanding string `json:"Outstanding"`

	Checkin  string `json:"Checkin"`
	Checkout string `json:"Checkout"`
}

// CheckoutReminderData reaches the guest on the morning they leave.
//
// It carries no money at all, deliberately. A stay that gets this far has been
// paid in full — at booking on a short-notice stay, or at T-7 — and a guest
// whose card was refused has had the balance_failed message instead. Putting a
// figure here would either repeat that conversation or start a new one on the
// morning somebody is trying to leave.
type CheckoutReminderData struct {
	Code      string   `json:"Code"`
	GuestName string   `json:"GuestName"`
	Rooms     []string `json:"Rooms"`

	Checkin  string `json:"Checkin"`
	Checkout string `json:"Checkout"`
	Nights   string `json:"Nights"`

	// CheckoutTime is the inn's checkout hour from settings, formatted:
	// "11:00 AM". Passed rather than written into the copy, so the sentence
	// about it stays true when the owner changes the setting.
	CheckoutTime string `json:"CheckoutTime"`
}

// PaymentRequestData asks a guest to pay for a stay the owner took on the
// telephone.
//
// It carries no deadline and no expiry, deliberately. Every other message about
// money in this system is attached to something running out — a hold, a T-7
// date — and this one is not: the room is already the guest's, the booking is
// confirmed, and what is outstanding stays outstanding until somebody settles
// it. Copy written as though it were a countdown would be false.
type PaymentRequestData struct {
	Code      string   `json:"Code"`
	GuestName string   `json:"GuestName"`
	Rooms     []string `json:"Rooms"`

	Checkin  string `json:"Checkin"`
	Checkout string `json:"Checkout"`
	Nights   string `json:"Nights"`

	// Amount is what is outstanding, and Total what the stay costs. They differ
	// once a part payment has landed, which is why both are here.
	Amount string `json:"Amount"`
	Total  string `json:"Total"`

	// PayURL is where the guest pays. Empty on a deployment with no public
	// address configured, so the template has to check before offering a button
	// that would go nowhere — the same shape as ManageURL on the confirmation.
	PayURL string `json:"PayURL"`
}

// Money renders integer cents the way a guest reads them: "$1,234.56".
//
// The dollars and the remainder are separated with integer division and printed
// as two fields. Cents never become a float on the way, here or anywhere else.
//
// Exported because the packages that build these payloads hold the cents, and
// the rule about how the inn writes an amount to a guest belongs here with the
// templates rather than being reinvented at each call site.
func Money(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}

	digits := strconv.FormatInt(cents/100, 10)

	var grouped strings.Builder
	for i := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteByte(digits[i])
	}

	return fmt.Sprintf("%s$%s.%02d", sign, grouped.String(), cents%100)
}

// Day renders a civil date for a guest: "Monday, January 2, 2026".
//
// Spelled out and carrying its year, unlike the picker's "Thu, Oct 1". An email
// is read weeks after it arrives and often on a phone in a hurry, and "Oct 1"
// is the kind of shorthand that has somebody turn up a year late.
func Day(d time.Time) string { return d.Format("Monday, January 2, 2006") }

// Clock renders a time of day for a guest: "11:00 AM".
//
// Takes an offset from midnight rather than a time.Time, because the values it
// formats are settings.checkin_time and settings.checkout_time — a time of day
// with no date attached, and attaching an arbitrary one on the way here is how
// a formatter starts printing the wrong day.
//
// Beside Money and Day for the same reason those are here: how the inn writes
// something to a guest is one rule, not one per call site.
func Clock(sinceMidnight time.Duration) string {
	midnight := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
	return midnight.Add(sinceMidnight).Format("3:04 PM")
}
