package gateway

import (
	"context"
	"errors"
	"fmt"

	stripe "github.com/stripe/stripe-go/v86"

	"bealhouse/internal/payments"
)

// currency is the only one the inn quotes in. Stripe wants it lowercase.
const currency = "usd"

// Stripe is the real processor.
//
// Everything Stripe-shaped in this system lives here and in the webhook
// decoder beside it. The package it implements — internal/payments — never
// imports the SDK, which is why its state machine can be tested against real
// Postgres with no key and no network.
type Stripe struct {
	client *stripe.Client
}

// NewStripe builds a client from a secret key.
func NewStripe(secretKey string) *Stripe {
	return &Stripe{client: stripe.NewClient(secretKey)}
}

// CreateIntent opens a payment for the guest to complete.
//
// The amount is whatever the caller passed, and the caller is required to have
// derived it from the booking's own snapshot. Nothing here validates it,
// because there is nothing here to validate it against — that check belongs
// where the booking is, and RecordCharge's Underpaid outcome is the backstop
// under it.
func (s *Stripe) CreateIntent(ctx context.Context, in payments.IntentRequest) (payments.Intent, error) {
	params := &stripe.PaymentIntentCreateParams{
		Amount:   stripe.Int64(in.AmountCents),
		Currency: stripe.String(currency),

		// The booking code travels as metadata and comes back on the webhook.
		// It is how a payment finds its stay, and it is set here rather than
		// read from the browser at any point, so a guest cannot attach their
		// payment to somebody else's booking.
		Metadata: map[string]string{
			metadataBookingCode: in.BookingCode,
			metadataKind:        in.Kind,
		},

		AutomaticPaymentMethods: &stripe.PaymentIntentCreateAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
	}
	if in.GuestEmail != "" {
		params.ReceiptEmail = stripe.String(in.GuestEmail)
	}

	// Keyed on the booking, so a guest who reloads the pay page gets the payment
	// they already had rather than a second one holding a second authorisation
	// against their card.
	//
	// The amount is in the key as well, because Stripe rejects a reused key
	// whose parameters have changed. A partial payment moves what is still
	// outstanding, and a stay in that state has to be payable rather than stuck
	// behind a 400 for the next day.
	params.SetIdempotencyKey(fmt.Sprintf("intent:%s:%d", in.BookingCode, in.AmountCents))
	params.Context = ctx

	if in.SaveCard {
		customerID, err := s.customerFor(ctx, in)
		if err != nil {
			return payments.Intent{}, err
		}
		params.Customer = stripe.String(customerID)

		// What makes the T-7 charge possible at all. Without it the card is
		// authorised for this payment only and decision #6 has nothing to
		// charge in a week's time.
		params.SetupFutureUsage = stripe.String("off_session")
	}

	// A card the owner is keying in from what a guest is reading out over the
	// telephone (decision: the console's manual collection).
	//
	// AutomaticPaymentMethods is replaced by cards alone, because the wallets and
	// bank redirects it would otherwise offer need the person paying to be at the
	// browser — and here the browser is at the inn's end of the call. MOTO then
	// tells the bank there is nobody to answer a 3-D Secure challenge, which is
	// what stops the card being declined mid-conversation.
	if in.MOTO {
		params.AutomaticPaymentMethods = nil
		params.PaymentMethodTypes = stripe.StringSlice([]string{"card"})
		params.PaymentMethodOptions = &stripe.PaymentIntentCreatePaymentMethodOptionsParams{
			Card: &stripe.PaymentIntentCreatePaymentMethodOptionsCardParams{
				MOTO: stripe.Bool(true),
			},
		}

		// The idempotency key has to differ from the guest-facing one for the
		// same booking and amount, or an owner reaching for the card after
		// emailing a link gets handed the payment built for the browser — one
		// with wallets enabled and no MOTO on it, which is the decline this
		// branch exists to avoid.
		params.SetIdempotencyKey(fmt.Sprintf("intent:moto:%s:%d", in.BookingCode, in.AmountCents))
	}

	intent, err := s.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return payments.Intent{}, declined(err, fmt.Errorf("gateway: creating a payment for %s: %w", in.BookingCode, err))
	}
	return asIntent(intent), nil
}

// customerFor creates the Stripe customer a saved card has to be attached to.
//
// One per booking rather than one per guest. The schema puts stripe_customer_id
// on bookings, and a returning guest getting a second customer record costs
// nothing, while sharing one across bookings would mean a saved card outliving
// the stay it was given for.
func (s *Stripe) customerFor(ctx context.Context, in payments.IntentRequest) (string, error) {
	params := &stripe.CustomerCreateParams{
		Metadata: map[string]string{metadataBookingCode: in.BookingCode},
	}
	if in.GuestEmail != "" {
		params.Email = stripe.String(in.GuestEmail)
	}
	if in.GuestName != "" {
		params.Name = stripe.String(in.GuestName)
	}
	params.SetIdempotencyKey("customer:" + in.BookingCode)
	params.Context = ctx

	customer, err := s.client.V1Customers.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("gateway: creating a customer for %s: %w", in.BookingCode, err)
	}
	return customer.ID, nil
}

// ChargeOffSession takes the balance from the card saved at booking.
//
// Created and confirmed in one call with off_session set, which is what tells
// the issuer nobody is present to answer a challenge. A card that needs one
// declines instead, and the guest is emailed rather than left with a payment
// waiting on a browser that is not open.
func (s *Stripe) ChargeOffSession(ctx context.Context, in payments.OffSessionRequest) (payments.Intent, error) {
	params := &stripe.PaymentIntentCreateParams{
		Amount:        stripe.Int64(in.AmountCents),
		Currency:      stripe.String(currency),
		Customer:      stripe.String(in.CustomerID),
		PaymentMethod: stripe.String(in.PaymentMethodID),
		Confirm:       stripe.Bool(true),
		OffSession:    stripe.Bool(true),
		Metadata: map[string]string{
			metadataBookingCode: in.BookingCode,
			metadataKind:        payments.KindBalance,
		},
	}

	// The balance is charged once per booking, so the code is the whole key. It
	// is what stops a job that failed on the network — after Stripe took the
	// money but before we heard so — charging the guest twice on its retry.
	params.SetIdempotencyKey("balance:" + in.BookingCode)
	params.Context = ctx

	intent, err := s.client.V1PaymentIntents.Create(ctx, params)
	if err != nil {
		return payments.Intent{}, declined(err, fmt.Errorf("gateway: charging the balance for %s: %w", in.BookingCode, err))
	}
	return asIntent(intent), nil
}

// Refund sends money back against a payment that succeeded.
func (s *Stripe) Refund(ctx context.Context, in payments.RefundRequest) (string, error) {
	params := &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(in.IntentID),
		Amount:        stripe.Int64(in.AmountCents),
		Metadata:      map[string]string{metadataBookingCode: in.BookingCode},
	}

	// Not keyed on the booking alone: a stay can be refunded more than once in
	// principle — a partial now, the rest after the owner talks to the guest —
	// and a key that collapsed those into one would silently return the first
	// refund's id and send no second payment.
	params.SetIdempotencyKey(fmt.Sprintf("refund:%s:%s:%d", in.BookingCode, in.IntentID, in.AmountCents))
	params.Context = ctx

	refund, err := s.client.V1Refunds.Create(ctx, params)
	if err != nil {
		return "", fmt.Errorf("gateway: refunding %s: %w", in.BookingCode, err)
	}
	return refund.ID, nil
}

// declined turns a card error into *payments.Declined and leaves everything
// else alone.
//
// The distinction is the whole reason this function exists: a decline is an
// outcome to record and email about, and an API or network error is a job to
// run again. Stripe reports the first as a card_error carrying the intent that
// failed, which is the id the ledger needs.
func declined(err error, fallback error) error {
	var se *stripe.Error
	if !errors.As(err, &se) || se.Type != stripe.ErrorTypeCard {
		return fallback
	}

	d := &payments.Declined{Reason: se.Msg}
	if se.PaymentIntent != nil {
		d.IntentID = se.PaymentIntent.ID
	}
	return d
}

func asIntent(in *stripe.PaymentIntent) payments.Intent {
	out := payments.Intent{
		ID:           in.ID,
		ClientSecret: in.ClientSecret,
	}
	if in.Customer != nil {
		out.CustomerID = in.Customer.ID
	}
	if in.PaymentMethod != nil {
		out.PaymentMethodID = in.PaymentMethod.ID
	}
	return out
}
