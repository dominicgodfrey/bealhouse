package payments

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/push"
)

// manageLinkGraceDays is how long after checkout the manage link keeps working.
//
// Long enough to be useful to a guest looking something up on the way home,
// short enough that the capability in a years-old email has lapsed.
const manageLinkGraceDays = 30

// QueueConfirmation queues the confirmation and the owner's copy for a booking
// that was confirmed without a payment going through this package.
//
// That is the console taking a reservation on the phone: the stay is real, the
// guest is owed the same message a guest booking on the website gets, and the
// money is being collected outside this system. Exported rather than copied
// into the console because the payload is the thing worth having one of — a
// second construction of it would drift, and the symptom would be two guests
// with the same booking reading different accounts of what they owe.
//
// `paidNowCents` is what this act collected, which for a phone booking is
// nothing. Call it inside the transaction that created the booking, so the
// message and the stay commit together.
func QueueConfirmation(
	ctx context.Context,
	q *db.Queries,
	code string,
	paidNowCents int64,
	ownerEmail string,
	manageURL func(code string, expires time.Time) string,
) error {
	b, err := q.GetBookingForPayment(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBookingNotFound
	}
	if err != nil {
		return fmt.Errorf("payments: loading booking %q for its confirmation: %w", code, err)
	}

	// AmountPaidCents is read back off the row and confirmationMail adds
	// paidNowCents to it, exactly as it does on the payment path — so a booking
	// that collected nothing reports nothing paid rather than a figure this
	// function had to invent.
	return confirmationMail(ctx, q, b, Charge{
		AmountCents: paidNowCents,
		OwnerEmail:  ownerEmail,
		ManageURL:   manageURL,
	})
}

// confirmationMail queues the guest's confirmation and the owner's copy.
//
// Called from inside RecordCharge's transaction, so the messages and the
// confirmed stay commit together or not at all. It queues; it never sends. A
// provider having a bad afternoon has to delay a confirmation rather than fail
// the payment that earned it (ARCHITECTURE.md).
//
// Only called when this payment is what confirmed the stay. The T-7 balance
// charge lands on a booking confirmed weeks earlier and takes the same success
// path, so RecordCharge compares the status it read at the start of the
// transaction rather than assuming — otherwise every guest gets a second
// "you're booked" a week before they arrive.
func confirmationMail(ctx context.Context, q *db.Queries, b db.GetBookingForPaymentRow, in Charge) error {
	rooms, err := q.ListBookingRooms(ctx, b.ID)
	if err != nil {
		return fmt.Errorf("payments: loading rooms for the confirmation: %w", err)
	}

	names := make([]string, 0, len(rooms))
	for _, r := range rooms {
		names = append(names, r.Name)
	}

	nights := strconv.Itoa(civil.Nights(b.Checkin.Time, b.Checkout.Time))

	// What the guest has now paid in total, read back after AddPaymentToBooking
	// has already run in this transaction — so this charge is included without
	// the caller having to add it on.
	paidSoFar := b.AmountPaidCents + in.AmountCents

	confirmation := email.BookingConfirmationData{
		Code:      b.Code,
		GuestName: b.GuestName,
		Rooms:     names,
		Checkin:   email.Day(b.Checkin.Time),
		Checkout:  email.Day(b.Checkout.Time),
		Nights:    nights,
		PaidNow:   email.Money(in.AmountCents),
		Total:     email.Money(b.TotalCents),
	}

	// What is still owed, and — separately — whether anything is scheduled to
	// collect it. Both empty on a stay paid in full at booking, which is how the
	// template tells the cases apart without being told which it is rendering.
	//
	// Two conditions rather than one, because they came apart when the console
	// started taking bookings by phone: those are confirmed with nothing paid
	// and no card saved, so there is a balance outstanding and no date on which
	// it will be taken. Tying both to balance_charge_at would send that guest a
	// confirmation shaped like "paid in full", which is the one thing it must
	// not say. Nothing changes for a stay booked on the website: a deposit
	// leaves an outstanding balance and a charge date, and a short-notice stay
	// leaves neither.
	if outstanding := b.TotalCents - paidSoFar; outstanding > 0 {
		confirmation.BalanceDue = email.Money(outstanding)
	}
	if b.BalanceChargeAt.Valid {
		confirmation.BalanceChargeOn = email.Day(b.BalanceChargeAt.Time)
	}

	// The link a guest uses to look at the stay and, if they must, cancel it. It
	// stops working a month after they leave: its whole job is done by then, and
	// a confirmation email is forwarded, backed up and searchable forever, so a
	// capability inside one should not be.
	if in.ManageURL != nil {
		confirmation.ManageURL = in.ManageURL(b.Code, civil.AddDays(b.Checkout.Time, manageLinkGraceDays))
	}

	if err := email.Queue(ctx, q, email.Envelope{
		To:       b.GuestEmail,
		Template: email.BookingConfirmation,
		Data:     confirmation,
	}); err != nil {
		return err
	}

	// The same news, to whichever handsets are subscribed.
	//
	// Queued in this transaction like the mail beside it, so the notification
	// and the booking commit together: a crash between them cannot leave a
	// confirmed stay nobody was told about, or a notification about a booking
	// that rolled back.
	//
	// Deliberately above the OwnerEmail check rather than below it. The two are
	// separate ways of hearing the same thing — an inn with notifications on
	// and no owner address configured should still get the tap — and tying push
	// to an email setting is how it would silently never fire.
	if err := push.Queue(ctx, q, push.Notification{
		Title: "New booking",
		Body: fmt.Sprintf("%s · %s → %s · %s",
			b.GuestName, email.Day(b.Checkin.Time), email.Day(b.Checkout.Time),
			email.Money(b.TotalCents)),
		URL: "/admin/bookings/" + b.Code,
		Tag: b.Code,
	}); err != nil {
		return err
	}

	if in.OwnerEmail == "" {
		return nil
	}

	return email.Queue(ctx, q, email.Envelope{
		To:       in.OwnerEmail,
		Template: email.OwnerNotification,
		Data: email.OwnerNotificationData{
			Code:      b.Code,
			GuestName: b.GuestName,
			// The guest's address, which the owner needs and the public
			// booking API deliberately never returns.
			GuestEmail: b.GuestEmail,
			Rooms:      names,
			Checkin:    email.Day(b.Checkin.Time),
			Checkout:   email.Day(b.Checkout.Time),
			Nights:     nights,
			PaidNow:    email.Money(in.AmountCents),
			Total:      email.Money(b.TotalCents),
		},
	})
}

// balanceReceipt tells a guest the T-7 charge went through.
//
// Queued in the transaction that recorded the money, like every other message
// here. The guest was warned a day earlier that this was coming (decision #6);
// this is what closes that loop, and a charge with no receipt behind it is what
// a disputed transaction looks like from the guest's side.
func balanceReceipt(ctx context.Context, q *db.Queries, b db.GetBookingForPaymentRow, in Charge) error {
	return email.Queue(ctx, q, email.Envelope{
		To:       b.GuestEmail,
		Template: email.BalanceReceipt,
		Data: email.BalanceReceiptData{
			Code:      b.Code,
			GuestName: b.GuestName,
			Amount:    email.Money(in.AmountCents),
			Total:     email.Money(b.TotalCents),
			Checkin:   email.Day(b.Checkin.Time),
			Checkout:  email.Day(b.Checkout.Time),
		},
	})
}

// balanceFailed tells a guest their card was refused.
//
// The stay is not cancelled and this message must not read as though it were:
// the guest is still arriving, there is just money outstanding and a
// conversation to have. Queued alongside MarkBalanceChargeFailed, so the flag
// the owner sees and the message the guest gets cannot disagree.
func balanceFailed(ctx context.Context, q *db.Queries, b db.GetBookingForPaymentRow) error {
	return email.Queue(ctx, q, email.Envelope{
		To:       b.GuestEmail,
		Template: email.BalanceFailed,
		Data: email.BalanceFailedData{
			Code:      b.Code,
			GuestName: b.GuestName,
			// What is left, not the snapshotted balance: if a partial payment
			// landed at some point, the remainder is the honest figure to ask
			// a guest to settle.
			Outstanding: email.Money(b.TotalCents - b.AmountPaidCents),
			Checkin:     email.Day(b.Checkin.Time),
			Checkout:    email.Day(b.Checkout.Time),
		},
	})
}
