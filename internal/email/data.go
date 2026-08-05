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
