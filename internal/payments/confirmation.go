package payments

import (
	"context"
	"fmt"
	"strconv"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
)

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

	// Left empty on a stay paid in full at booking, which is how the template
	// tells the two cases apart without being told which it is rendering.
	if b.BalanceChargeAt.Valid {
		confirmation.BalanceDue = email.Money(b.TotalCents - paidSoFar)
		confirmation.BalanceChargeOn = email.Day(b.BalanceChargeAt.Time)
	}

	if err := email.Queue(ctx, q, email.Envelope{
		To:       b.GuestEmail,
		Template: email.BookingConfirmation,
		Data:     confirmation,
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
