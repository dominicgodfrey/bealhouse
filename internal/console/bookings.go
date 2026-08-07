package console

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"bealhouse/internal/booking"
	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/payments"
	"bealhouse/internal/pricing"
)

// Stay is one reservation as every console list shows it.
//
// The money on it is four numbers and not one, because "is this paid" has four
// different answers an owner acts on differently: what the stay costs, what has
// actually arrived, what the guest still owes, and whether the card has already
// been refused. A single "paid: true/false" would collapse the two that matter
// most — a deposit-paid stay awaiting its T-7 charge looks identical to one
// whose T-7 charge bounced.
type Stay struct {
	Code   string `json:"code"`
	Status string `json:"status"`

	Checkin  string `json:"checkin"`
	Checkout string `json:"checkout"`
	Nights   int    `json:"nights"`
	Guests   int    `json:"guests"`
	WithPet  bool   `json:"withPet"`

	// Rooms is the room names joined for display. The console's lists are read,
	// not parsed, and v1 sells one room at a time (decision #10).
	Rooms string `json:"rooms"`

	GuestID    int64  `json:"guestId,omitempty"`
	GuestName  string `json:"guestName"`
	GuestEmail string `json:"guestEmail"`
	GuestPhone string `json:"guestPhone,omitempty"`

	TotalCents int64 `json:"totalCents"`
	PaidCents  int64 `json:"paidCents"`

	// OutstandingCents is what has not arrived: total less gross collected.
	// Derived here rather than in SQL so it cannot disagree with the two numbers
	// beside it, and never negative — an overpayment is a conversation, not a
	// negative balance.
	OutstandingCents int64 `json:"outstandingCents"`

	// BalanceChargeOn is empty when no scheduled charge exists: a short-notice
	// stay paid in full at booking, or one the owner took by phone. That is what
	// the NULL column means (decision #7), so it is reported as absent rather
	// than as a date the console would have to invent.
	BalanceChargeOn string `json:"balanceChargeOn,omitempty"`

	// ChargeFailed is the flag that has to be unmissable. The card was refused
	// at T-7, the stay is still confirmed, the guest has been emailed, and
	// somebody at the inn has to pick up the phone.
	ChargeFailed *time.Time `json:"chargeFailed,omitempty"`

	// Warned is the T-8 "you will be charged tomorrow" having gone out. Shown
	// because its absence next to an imminent charge date is the one thing that
	// says the balance jobs are not running.
	Warned *time.Time `json:"warned,omitempty"`

	CreatedAt *time.Time `json:"createdAt,omitempty"`
}

func stayFromSearch(r db.SearchBookingsRow) Stay {
	created := r.CreatedAt
	return Stay{
		Code:             r.Code,
		Status:           r.Status,
		Checkin:          day(r.Checkin),
		Checkout:         day(r.Checkout),
		Nights:           civil.Nights(r.Checkin.Time, r.Checkout.Time),
		Guests:           int(r.Guests),
		WithPet:          r.WithPet,
		Rooms:            r.RoomNames,
		GuestID:          r.GuestID,
		GuestName:        r.GuestName,
		GuestEmail:       r.GuestEmail,
		GuestPhone:       r.GuestPhone,
		TotalCents:       r.TotalCents,
		PaidCents:        r.AmountPaidCents,
		OutstandingCents: outstanding(r.TotalCents, r.AmountPaidCents),
		BalanceChargeOn:  day(r.BalanceChargeAt),
		ChargeFailed:     instant(r.BalanceChargeFailedAt),
		Warned:           instant(r.BalanceWarnedAt),
		CreatedAt:        &created,
	}
}

func outstanding(total, paid int64) int64 {
	if paid >= total {
		return 0
	}
	return total - paid
}

// Board is the console's first screen: what is happening at the inn today.
type Board struct {
	Date string `json:"date"`

	Arrivals   []Stay `json:"arrivals"`
	Departures []Stay `json:"departures"`
	InHouse    []Stay `json:"inHouse"`

	// The hours the arrivals and departures are expected, read from settings
	// rather than written into the page, so changing them in the settings screen
	// changes them here too.
	CheckinTime  string `json:"checkinTime"`
	CheckoutTime string `json:"checkoutTime"`

	// Flagged is the count of confirmed stays whose balance charge was refused,
	// anywhere in the book and not only today. It rides on this response because
	// this is the screen the console opens on, and a flag the owner has to
	// navigate to is a flag they will find a week late.
	Flagged int64 `json:"flagged"`

	// NewInquiries is the same idea for the events inbox.
	NewInquiries int64 `json:"newInquiries"`
}

// Today assembles the board for one civil day at the inn.
func (o *Ops) Today(ctx context.Context, on time.Time) (Board, error) {
	rows, err := o.q.TodayBoard(ctx, dateOf(on))
	if err != nil {
		return Board{}, fmt.Errorf("console: loading today: %w", err)
	}

	settings, err := o.q.GetSettings(ctx)
	if err != nil {
		return Board{}, fmt.Errorf("console: loading settings: %w", err)
	}

	board := Board{
		Date:         on.Format(time.DateOnly),
		Arrivals:     []Stay{},
		Departures:   []Stay{},
		InHouse:      []Stay{},
		CheckinTime:  clock(settings.CheckinTime),
		CheckoutTime: clock(settings.CheckoutTime),
	}

	for _, r := range rows {
		s := Stay{
			Code:             r.Code,
			Status:           booking.StatusConfirmed,
			Checkin:          day(r.Checkin),
			Checkout:         day(r.Checkout),
			Nights:           civil.Nights(r.Checkin.Time, r.Checkout.Time),
			Guests:           int(r.Guests),
			WithPet:          r.WithPet,
			Rooms:            r.RoomNames,
			GuestName:        r.GuestName,
			GuestEmail:       r.GuestEmail,
			GuestPhone:       r.GuestPhone,
			TotalCents:       r.TotalCents,
			PaidCents:        r.AmountPaidCents,
			OutstandingCents: outstanding(r.TotalCents, r.AmountPaidCents),
			BalanceChargeOn:  day(r.BalanceChargeAt),
			ChargeFailed:     instant(r.BalanceChargeFailedAt),
		}

		switch r.Bucket {
		case "arrival":
			board.Arrivals = append(board.Arrivals, s)
		case "departure":
			board.Departures = append(board.Departures, s)
		default:
			board.InHouse = append(board.InHouse, s)
		}
	}

	if board.Flagged, err = o.q.CountFlaggedBookings(ctx); err != nil {
		return Board{}, fmt.Errorf("console: counting flagged bookings: %w", err)
	}
	if board.NewInquiries, err = o.q.CountNewInquiries(ctx); err != nil {
		return Board{}, fmt.Errorf("console: counting new inquiries: %w", err)
	}

	return board, nil
}

// BookingFilter is the reservations list's query, all of it optional.
type BookingFilter struct {
	From   string
	To     string
	Status string
	RoomID int64
	Query  string

	// OnlyFlagged narrows to stays whose balance charge was refused, which is
	// the one filter reached for in order to act rather than to look.
	OnlyFlagged bool

	Limit int
}

// Bookings is the upcoming-reservations screen (an explicit requirement).
func (o *Ops) Bookings(ctx context.Context, f BookingFilter) ([]Stay, error) {
	from, err := optionalDay(f.From)
	if err != nil {
		return nil, err
	}
	to, err := optionalDay(f.To)
	if err != nil {
		return nil, err
	}

	switch f.Status {
	case "", booking.StatusPending, booking.StatusConfirmed, booking.StatusCancelled, booking.StatusExpired:
	default:
		return nil, badf("%q is not a booking status", f.Status)
	}

	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := o.q.SearchBookings(ctx, db.SearchBookingsParams{
		FromDate:    from,
		ToDate:      to,
		Status:      f.Status,
		RoomID:      f.RoomID,
		Query:       strings.TrimSpace(f.Query),
		OnlyFlagged: f.OnlyFlagged,
		RowLimit:    int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("console: searching bookings: %w", err)
	}

	out := make([]Stay, 0, len(rows))
	for _, r := range rows {
		out = append(out, stayFromSearch(r))
	}
	return out, nil
}

// Payment is one row of the ledger as the booking screen shows it.
//
// Refunds appear as their own rows rather than reducing the amount collected,
// which is decision #25: amount_paid_cents is the gross and only ever grows.
// An owner reading this screen is looking at a history, and a history that
// quietly nets out is one that cannot answer "what did we actually charge this
// card".
type Payment struct {
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	AmountCents int64     `json:"amountCents"`
	StripeID    string    `json:"stripeId"`
	At          time.Time `json:"at"`
}

// BookingDetail is one reservation opened.
type BookingDetail struct {
	Stay Stay `json:"stay"`

	// Rooms carries the per-night prices as they were snapshotted at booking, so
	// this screen and the guest's PDF cannot disagree about what was charged.
	Rooms []booking.Room `json:"rooms"`
	Quote pricing.Quote  `json:"quote"`

	Payments []Payment `json:"payments"`

	// Refund is what cancelling would return today (decisions #9 and #26),
	// computed by the same function the guest's own manage page uses. Quote
	// before button, on the owner's side as well: the console never asks anyone
	// to confirm a refund whose amount it has not already shown.
	Refund *RefundQuote `json:"refund,omitempty"`

	// Cancellable is false with a reason when the stay is not in a state the
	// arithmetic describes — already cancelled, never confirmed, or underway.
	Cancellable bool   `json:"cancellable"`
	Reason      string `json:"reason,omitempty"`

	HoldExpiresAt *time.Time `json:"holdExpiresAt,omitempty"`
}

// RefundQuote mirrors payments.RefundQuote on the wire.
type RefundQuote struct {
	PaidCents     int64 `json:"paidCents"`
	RetainedCents int64 `json:"retainedCents"`
	RefundCents   int64 `json:"refundCents"`
	Late          bool  `json:"late"`
}

// Booking opens one reservation.
//
// `on` is the civil day the refund is quoted against, passed in rather than
// read from a clock in here for the same reason payments.Cancel takes it: which
// side of T-7 today falls on is the whole of decision #9.
func (o *Ops) Booking(ctx context.Context, code string, on time.Time) (BookingDetail, error) {
	code = strings.ToUpper(strings.TrimSpace(code))

	full, err := booking.Get(ctx, o.q, code)
	if errors.Is(err, booking.ErrNotFound) {
		return BookingDetail{}, ErrNotFound
	}
	if err != nil {
		return BookingDetail{}, err
	}

	row, err := o.q.GetBookingByCode(ctx, code)
	if err != nil {
		return BookingDetail{}, notFound(fmt.Errorf("console: loading %q: %w", code, err))
	}

	out := BookingDetail{
		Stay: Stay{
			Code:             full.Code,
			Status:           full.Status,
			Checkin:          full.Checkin,
			Checkout:         full.Checkout,
			Nights:           full.Nights,
			Guests:           full.Guests,
			WithPet:          full.WithPet,
			Rooms:            roomNames(full.Rooms),
			GuestName:        row.GuestName,
			GuestEmail:       row.GuestEmail,
			GuestPhone:       row.GuestPhone,
			TotalCents:       full.Quote.TotalCents,
			PaidCents:        row.AmountPaidCents,
			OutstandingCents: outstanding(full.Quote.TotalCents, row.AmountPaidCents),
			BalanceChargeOn:  full.BalanceChargeOn,
		},
		Rooms:         full.Rooms,
		Quote:         full.Quote,
		Payments:      []Payment{},
		HoldExpiresAt: full.HoldExpiresAt,
	}

	ledger, err := o.q.ListPaymentsForBooking(ctx, row.ID)
	if err != nil {
		return BookingDetail{}, fmt.Errorf("console: loading the ledger for %q: %w", code, err)
	}
	for _, p := range ledger {
		out.Payments = append(out.Payments, Payment{
			Kind:        p.Kind,
			Status:      p.Status,
			AmountCents: p.AmountCents,
			StripeID:    p.StripeID,
			At:          p.CreatedAt,
		})
	}

	// The refund quote only means anything for a stay that could be cancelled,
	// and computing one for a stay already underway would run decision #9's
	// arithmetic over a case it does not describe (payments.ErrStayUnderway).
	switch {
	case full.Status != booking.StatusConfirmed:
		out.Reason = "Only a confirmed stay can be cancelled."
	case !on.Before(row.Checkin.Time):
		out.Reason = "The stay has already begun. A no-show or a cut-short visit is a manual refund, not a cancellation."
	default:
		quote, err := payments.RefundFor(ctx, o.q, code, on)
		if err != nil {
			return BookingDetail{}, fmt.Errorf("console: quoting a refund for %q: %w", code, err)
		}
		out.Cancellable = true
		out.Refund = &RefundQuote{
			PaidCents:     quote.PaidCents,
			RetainedCents: quote.RetainedCents,
			RefundCents:   quote.RefundCents,
			Late:          quote.Late,
		}
	}

	return out, nil
}

func roomNames(rooms []booking.Room) string {
	names := make([]string, 0, len(rooms))
	for _, r := range rooms {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}

// Cancel cancels a stay from the console.
//
// The same payments.Cancel the guest's own link calls, so the room goes back on
// sale, the refund is queued and the guest is told, all in one transaction. The
// owner does not get a different arithmetic from the guest — if they want to
// return more than the policy says, that is Refund below, which is an explicit
// amount somebody typed rather than a policy quietly bent.
func (o *Ops) Cancel(ctx context.Context, code string, on time.Time) (RefundQuote, error) {
	done, err := payments.Cancel(ctx, o.store, code, on)
	switch {
	case errors.Is(err, payments.ErrBookingNotFound):
		return RefundQuote{}, ErrNotFound
	case errors.Is(err, payments.ErrNotCancellable):
		return RefundQuote{}, badf("only a confirmed stay can be cancelled")
	case errors.Is(err, payments.ErrStayUnderway):
		return RefundQuote{}, badf("the stay has already begun; use a manual refund instead")
	case err != nil:
		return RefundQuote{}, err
	}

	return RefundQuote{
		PaidCents:     done.RetainedCents + done.RefundCents,
		RetainedCents: done.RetainedCents,
		RefundCents:   done.RefundCents,
		Late:          done.Late,
	}, nil
}

// Refund sends money back without cancelling anything.
//
// The no-show, the cut-short visit, and the goodwill gesture — the cases
// decision #9's arithmetic has no branch for and that ARCHITECTURE puts in the
// console as a manual refund. There is no step-up authentication on it
// (decision #15): it is the owners' call, and the phone is locked.
//
// Zero is refused rather than treated as "everything". payments.QueueRefund
// does read zero that way, deliberately, because the penalty-free path in
// decision #24 wants the ledger consulted when the job runs — but an owner who
// leaves the amount box empty means nothing, not the entire booking, and the
// difference is the whole stay's money.
func (o *Ops) Refund(ctx context.Context, code string, amountCents int64) error {
	code = strings.ToUpper(strings.TrimSpace(code))

	if amountCents <= 0 {
		return badf("say how much to refund")
	}

	row, err := o.q.GetBookingByCode(ctx, code)
	if err != nil {
		return notFound(fmt.Errorf("console: loading %q: %w", code, err))
	}
	if amountCents > row.AmountPaidCents {
		return badf("only %s has been collected on this booking", email.Money(row.AmountPaidCents))
	}

	if err := payments.QueueRefund(ctx, o.q, code, amountCents); err != nil {
		return fmt.Errorf("console: queueing a refund for %q: %w", code, err)
	}
	return nil
}

// How a manual booking is going to be paid for.
//
// Three, because there are three real answers and the owner knows which one on
// the call. They differ only in what happens after the booking is written — the
// stay itself is identical, and in every case it is confirmed and the room is
// held, because the guest has been told they have it.
const (
	// SettleOffline is cash, a cheque, a bank transfer, or an arrangement. The
	// system records what is owed and takes no part in collecting it.
	SettleOffline = "offline"

	// SettleByLink emails the guest a link to pay it themselves.
	SettleByLink = "link"

	// SettleByCard is the owner keying a card in from what the guest is reading
	// out. Nothing is queued here: the console opens the card form on the next
	// screen, and the money arrives through the same webhook as everybody
	// else's.
	SettleByCard = "card"
)

// CardPayment is a payment waiting for a card to be keyed into it.
type CardPayment struct {
	Code string `json:"code"`

	// ClientSecret authorises this one payment and nothing else. It goes into
	// Stripe's card form in the browser and belongs in no log, URL or error
	// message — the same rule the guest-facing pay page lives under.
	ClientSecret string `json:"clientSecret"`

	// PublishableKey identifies the account to the form. Public by design.
	PublishableKey string `json:"publishableKey"`

	AmountCents int64 `json:"amountCents"`

	// DevPayment says the processor is the fake, so there is no card form to
	// mount and the console offers a stand-in button instead. Never true on a
	// deployment that can move real money.
	DevPayment bool `json:"devPayment"`
}

// CollectByCard opens a payment the owner will key a card into.
//
// **No card number passes through this process, and none ever should.** This
// returns a client secret; the console mounts Stripe's own form against it and
// the details travel from that form to Stripe. Taking a card number here would
// put the inn's server in scope for PCI compliance in a way that a seven-room
// inn should never be, and it is the reason the guest-facing flow is built the
// same way.
//
// The amount is the server's, derived from the booking by payments.OpenKeyedIn,
// so an owner cannot key in a figure either — which matters more here than on
// the guest side, because the person at the keyboard is the one who would
// benefit from a mistake going unnoticed.
func (o *Ops) CollectByCard(ctx context.Context, code string) (CardPayment, error) {
	opened, err := payments.OpenKeyedIn(ctx, o.q, o.gateway, strings.ToUpper(strings.TrimSpace(code)))
	switch {
	case errors.Is(err, payments.ErrBookingNotFound):
		return CardPayment{}, ErrNotFound
	case errors.Is(err, payments.ErrNotPayable):
		return CardPayment{}, badf("this booking cannot take a payment")
	case errors.Is(err, payments.ErrNothingToPay):
		return CardPayment{}, badf("nothing is outstanding on this booking")
	case errors.Is(err, payments.ErrGatewayDisabled):
		return CardPayment{}, badf("no card processor is configured on this deployment")
	case err != nil:
		return CardPayment{}, err
	}

	return CardPayment{
		Code:           opened.Code,
		ClientSecret:   opened.ClientSecret,
		PublishableKey: o.stripeKey,
		AmountCents:    opened.AmountCents,
		DevPayment:     o.fakeGateway,
	}, nil
}

// RequestPayment emails a guest a link to pay what is outstanding.
//
// Available on its own as well as from the booking form, because the case it
// exists for recurs: an owner takes a booking meaning to settle it in cash, the
// guest changes their mind a fortnight later, and the way to ask is a button
// rather than a phone call. It is also the retry when the first message went to
// a typo.
//
// It refuses a booking with nothing outstanding rather than sending an invoice
// for nothing, and one that is not confirmed — a pending booking already has a
// pay page and a hold running out, and this message says the opposite.
func (o *Ops) RequestPayment(ctx context.Context, code string) error {
	return o.tx(ctx, func(q *db.Queries) error {
		return o.queuePaymentRequest(ctx, q, strings.ToUpper(strings.TrimSpace(code)))
	})
}

// queuePaymentRequest builds the message. Called inside a transaction in both
// its cases: with the booking that has just been created, or with one of its
// own.
func (o *Ops) queuePaymentRequest(ctx context.Context, q *db.Queries, code string) error {
	b, err := q.GetBookingForPayment(ctx, code)
	if err != nil {
		return notFound(fmt.Errorf("console: loading %q: %w", code, err))
	}

	if b.Status != booking.StatusConfirmed {
		return badf("only a confirmed booking can be sent a payment link")
	}

	outstanding := b.TotalCents - b.AmountPaidCents
	if outstanding <= 0 {
		return badf("nothing is outstanding on this booking")
	}

	// A stay with a scheduled balance charge is a website booking whose card is
	// on file and whose money is coming on its own. Asking that guest to pay
	// again is how somebody pays twice.
	if b.BalanceChargeAt.Valid {
		return badf("this booking's balance is already scheduled to come off the card on file")
	}

	rooms, err := q.ListBookingRooms(ctx, b.ID)
	if err != nil {
		return fmt.Errorf("console: loading rooms for %q: %w", code, err)
	}
	names := make([]string, 0, len(rooms))
	for _, r := range rooms {
		names = append(names, r.Name)
	}

	return email.Queue(ctx, q, email.Envelope{
		To:       b.GuestEmail,
		Template: email.PaymentRequest,
		Data: email.PaymentRequestData{
			Code:      b.Code,
			GuestName: b.GuestName,
			Rooms:     names,
			Checkin:   email.Day(b.Checkin.Time),
			Checkout:  email.Day(b.Checkout.Time),
			Nights:    strconv.Itoa(civil.Nights(b.Checkin.Time, b.Checkout.Time)),
			Amount:    email.Money(outstanding),
			Total:     email.Money(b.TotalCents),
			PayURL:    o.letterhead.PayURL(b.Code),
		},
	})
}

// ManualBooking is a reservation the owner took on the phone.
type ManualBooking struct {
	RoomSlug string `json:"roomSlug"`
	Checkin  string `json:"checkin"`
	Checkout string `json:"checkout"`
	Guests   int    `json:"guests"`
	WithPet  bool   `json:"withPet"`

	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`

	// Payment is one of the three above. Empty means SettleOffline, which is the
	// one that promises nothing.
	Payment string `json:"payment"`
}

// CreateBooking writes down a reservation taken outside the website.
//
// It goes through booking.Create like everything else, with Manual set: the
// same availability re-check, the same pricing, and the same claim on the room
// through the exclusion constraint. An owner cannot double-book a room that a
// guest could not, which matters precisely because this is the path with a
// person on the other end who believes they know the room is free.
//
// **The guest gets the same messages a guest booking on the website gets.** The
// confirmation and the owner's copy are queued here, inside the transaction
// that created the booking; the departure-morning note follows on its own,
// because that scan matches confirmed stays by checkout date and this is one.
// The two balance messages do not, and should not: they announce and then take
// a payment from a saved card, and there is no card — the money is being
// collected some other way, which is what the confirmation says.
//
// It is the same payload the payment path builds, through payments.
// QueueConfirmation, rather than a second construction of it. Two guests with
// the same booking must not be able to read different accounts of what they owe.
func (o *Ops) CreateBooking(ctx context.Context, in ManualBooking) (booking.Booking, error) {
	checkin, err := parseDay(in.Checkin)
	if err != nil {
		return booking.Booking{}, err
	}
	checkout, err := parseDay(in.Checkout)
	if err != nil {
		return booking.Booking{}, err
	}
	if in.Guests < 1 {
		return booking.Booking{}, badf("how many guests?")
	}

	if in.Payment == "" {
		in.Payment = SettleOffline
	}
	switch in.Payment {
	case SettleOffline, SettleByLink, SettleByCard:
	default:
		return booking.Booking{}, badf("%q is not a way of paying", in.Payment)
	}

	made, err := booking.Create(ctx, o.store, booking.Request{
		RoomSlug: strings.TrimSpace(in.RoomSlug),
		Checkin:  checkin,
		Checkout: checkout,
		Guests:   in.Guests,
		WithPet:  in.WithPet,
		Manual:   true,
		Guest: booking.Guest{
			Name:  strings.TrimSpace(in.Name),
			Email: strings.TrimSpace(in.Email),
			Phone: strings.TrimSpace(in.Phone),
		},

		// Inside the transaction that wrote the booking, so the stay and the
		// messages telling the guest about it commit together. Nothing was
		// collected, which is what the zero says — the confirmation reports the
		// whole total as outstanding rather than implying a payment nobody made.
		AfterCreate: func(ctx context.Context, q *db.Queries, code string) error {
			if err := payments.QueueConfirmation(ctx, q, code, 0,
				o.letterhead.OwnerEmail, o.letterhead.ManageURL); err != nil {
				return err
			}
			if in.Payment != SettleByLink {
				return nil
			}
			return o.queuePaymentRequest(ctx, q, code)
		},
	})
	switch {
	case booking.IsUnavailable(err):
		return booking.Booking{}, badf("that room is not free for those dates")
	case errors.Is(err, booking.ErrGuestNameRequired):
		return booking.Booking{}, badf("the booking needs a name")
	case errors.Is(err, booking.ErrGuestEmailRequired):
		return booking.Booking{}, badf("the booking needs an email address")
	case err != nil:
		return booking.Booking{}, err
	}
	return made, nil
}
