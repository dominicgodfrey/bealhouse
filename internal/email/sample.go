package email

// Sample is one filled-in payload per message, for the console's preview.
//
// The console renders a template against this rather than against a real
// booking, and that is deliberate on two counts. A preview must work on an inn
// that has taken no bookings yet — which is every inn on its first day, and
// exactly when somebody is writing this copy. And a preview of a real guest's
// stay would put their name and their money on a screen for no reason, in the
// one place on this system where nobody is asking about them.
//
// The figures are invented and look it: a code that is obviously a sample, a
// guest called Sample, round-ish numbers. An owner reading the preview should
// never wonder for a moment whether they are looking at a real reservation.
//
// Every field the payload carries is filled, including the optional ones. A
// preview whose ManageURL happened to be empty would show a template without
// its button and teach the owner that there isn't one — the interesting half of
// these templates is precisely the part that only appears when a field is set.
func Sample(name string) any {
	const (
		code  = "SAMPLE"
		guest = "Sample Guest"
		in    = "Friday, 12 June 2026"
		out   = "Sunday, 14 June 2026"
	)
	rooms := []string{"The Cornice Room"}

	switch name {
	case BookingConfirmation:
		return BookingConfirmationData{
			Code:      code,
			GuestName: guest,
			Rooms:     rooms,
			Checkin:   in,
			Checkout:  out,
			Nights:    "2",
			PaidNow:   "$162.75",
			Total:     "$325.50",

			// Set, so the preview shows the deposit shape. A stay paid in full
			// leaves both empty and the template says so instead — which is the
			// branch worth seeing, since getting it wrong tells a guest with
			// money outstanding that they are paid up.
			BalanceDue:      "$162.75",
			BalanceChargeOn: "Friday, 5 June 2026",

			ManageURL: "https://example.test/booking/SAMPLE#sample-token",
		}

	case OwnerNotification:
		return OwnerNotificationData{
			Code:       code,
			GuestName:  guest,
			GuestEmail: "sample.guest@example.test",
			Rooms:      rooms,
			Checkin:    in,
			Checkout:   out,
			Nights:     "2",
			PaidNow:    "$162.75",
			Total:      "$325.50",
		}

	case BalanceWarning:
		return BalanceWarningData{
			Code:      code,
			GuestName: guest,
			Amount:    "$162.75",
			ChargeOn:  "Friday, 5 June 2026",
			Checkin:   in,
			Checkout:  out,
		}

	case BalanceReceipt:
		return BalanceReceiptData{
			Code:      code,
			GuestName: guest,
			Amount:    "$162.75",
			Total:     "$325.50",
			Checkin:   in,
			Checkout:  out,
		}

	case BalanceFailed:
		return BalanceFailedData{
			Code:        code,
			GuestName:   guest,
			Outstanding: "$162.75",
			Checkin:     in,
			Checkout:    out,
		}

	case CancellationRefund:
		return CancellationRefundData{
			Code:      code,
			GuestName: guest,
			Refunded:  "$157.83",
			Checkin:   in,
			Checkout:  out,
		}

	case CheckoutReminder:
		return CheckoutReminderData{
			Code:         code,
			GuestName:    guest,
			Rooms:        rooms,
			Checkin:      in,
			Checkout:     out,
			Nights:       "2",
			CheckoutTime: "11:00 AM",
		}

	case PaymentRequest:
		return PaymentRequestData{
			Code:      code,
			GuestName: guest,
			Rooms:     rooms,
			Checkin:   in,
			Checkout:  out,
			Nights:    "2",
			Amount:    "$325.50",
			Total:     "$325.50",
			PayURL:    "https://example.test/bookings/SAMPLE/pay",
		}

	default:
		return nil
	}
}

// Preview renders copy that has not been saved yet.
//
// The point is to answer "what will this look like" before committing to it,
// so it takes the subject and body from the editor rather than reading the
// stored row — previewing only what is already saved would answer the question
// after it stopped being interesting.
//
// It goes through Parse for the same reason every save does: copy that will not
// compile has to fail here, in front of the person who typed it, rather than at
// send time, which is after a guest's card has been charged and with nothing on
// screen to connect the failure to the sentence that caused it.
//
// The layout is the shipped one, exactly as in Render — so what the owner sees
// is the letterhead a guest gets, and not a fragment floating on its own.
func (r *Renderer) Preview(name, subject, body string) (Message, error) {
	set, err := Parse(name, subject, body)
	if err != nil {
		return Message{}, err
	}
	return r.assemble(set, name, Sample(name))
}
