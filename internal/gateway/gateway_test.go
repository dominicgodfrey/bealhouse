package gateway

import (
	"context"
	"errors"
	"testing"

	"bealhouse/internal/config"
	"bealhouse/internal/payments"
)

// The fake confirms whatever it is asked to and takes no money, so which
// processor New hands back is the single most expensive decision in this
// package. Every way of arriving at the fake is asserted here, and so is every
// way of being refused one.
func TestNewRefusesToFakeAnythingItShouldNot(t *testing.T) {
	real := config.Config{
		Env:                 "dev",
		StripeSecretKey:     "sk_test_x",
		StripeWebhookSecret: "whsec_x",
	}

	for _, c := range []struct {
		name string
		cfg  config.Config
		want string // "stripe", "fake", "disabled" or "error"
	}{
		{"fully configured", real, "stripe"},
		{"configured in production", func() config.Config {
			c := real
			c.Env = "production"
			return c
		}(), "stripe"},

		// Real settings win outright: a deploy with keys never gets a fake, no
		// matter what else is switched on.
		{"configured and asked to fake", func() config.Config {
			c := real
			c.StripeFake = true
			return c
		}(), "stripe"},

		{"nothing configured", config.Config{Env: "dev"}, "disabled"},
		{"nothing configured in production", config.Config{Env: "production"}, "disabled"},

		{"asked to fake in dev", config.Config{Env: "dev", StripeFake: true}, "fake"},

		// ENV defaults to "dev", so this is what an unconfigured production
		// deploy looks like. It must not be the thing that decides.
		{"asked to fake in production", config.Config{Env: "production", StripeFake: true}, "error"},
		{"asked to fake in staging", config.Config{Env: "staging", StripeFake: true}, "error"},

		// Half-configured is a mistake to stop on, not a licence to substitute
		// a fake for the half that is missing.
		{"asked to fake with a secret key", config.Config{
			Env: "dev", StripeFake: true, StripeSecretKey: "sk_test_x",
		}, "error"},
		{"asked to fake with a webhook secret", config.Config{
			Env: "dev", StripeFake: true, StripeWebhookSecret: "whsec_x",
		}, "error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, secret, err := New(c.cfg)

			if c.want == "error" {
				if err == nil {
					t.Fatalf("got %T, want a refusal", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			// The secret has to match the processor, or every delivery the fake
			// signs is rejected by a route verifying against something else.
			switch c.want {
			case "stripe":
				if secret != c.cfg.StripeWebhookSecret {
					t.Errorf("webhook secret %q, want the configured one", secret)
				}
			case "fake":
				if secret != FakeWebhookSecret {
					t.Errorf("webhook secret %q, want the fake's", secret)
				}
			case "disabled":
				if secret != "" {
					t.Error("a disabled processor handed out a webhook secret")
				}
			}

			var kind string
			switch got.(type) {
			case *Stripe:
				kind = "stripe"
			case *Fake:
				kind = "fake"
			case Disabled:
				kind = "disabled"
			default:
				t.Fatalf("New returned an unknown processor %T", got)
			}
			if kind != c.want {
				t.Errorf("got the %s processor, want %s", kind, c.want)
			}
		})
	}
}

// Without keys the honest answer is that money cannot move. A processor that
// silently succeeded would confirm stays nobody paid for.
func TestDisabledFailsEveryOperation(t *testing.T) {
	ctx := context.Background()
	d := Disabled{}

	if _, err := d.CreateIntent(ctx, payments.IntentRequest{}); !errors.Is(err, payments.ErrGatewayDisabled) {
		t.Errorf("CreateIntent gave %v", err)
	}
	if _, err := d.ChargeOffSession(ctx, payments.OffSessionRequest{}); !errors.Is(err, payments.ErrGatewayDisabled) {
		t.Errorf("ChargeOffSession gave %v", err)
	}
	if _, err := d.Refund(ctx, payments.RefundRequest{}); !errors.Is(err, payments.ErrGatewayDisabled) {
		t.Errorf("Refund gave %v", err)
	}
}

func TestFakeMintsUsableIdentifiers(t *testing.T) {
	ctx := context.Background()
	f := NewFake()

	first, err := f.CreateIntent(ctx, payments.IntentRequest{BookingCode: "BH-1", AmountCents: 100, SaveCard: true})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	second, err := f.CreateIntent(ctx, payments.IntentRequest{BookingCode: "BH-2", AmountCents: 100})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	// The ledger's idempotency key is the intent id, so two payments sharing one
	// would drop the second silently.
	if first.ID == second.ID {
		t.Error("two payments got the same id")
	}

	// Stripe.js splits a client secret on _secret_ to find its intent. A shape
	// it cannot parse fails in the browser, which is the one place this fake
	// exists to exercise.
	if first.ClientSecret != first.ID+"_secret_fake" {
		t.Errorf("client secret %q is not shaped like an intent's", first.ClientSecret)
	}

	// A saved card is what a T-7 charge needs. Without it the fake would confirm
	// a stay that could never take its balance.
	if first.CustomerID == "" || first.PaymentMethodID == "" {
		t.Error("SaveCard produced no customer or payment method")
	}
	if second.CustomerID != "" || second.PaymentMethodID != "" {
		t.Error("a stay paid in full stored a card it will never charge")
	}
}

// A decline has to be distinguishable from a network failure: one is an outcome
// with an email attached, the other is a job to run again.
func TestFakeDeclineIsRecognisableAsOne(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	f.DeclineOffSession(true)

	_, err := f.ChargeOffSession(ctx, payments.OffSessionRequest{
		BookingCode:     "BH-1",
		AmountCents:     5000,
		CustomerID:      "cus_x",
		PaymentMethodID: "pm_x",
	})

	declined, ok := payments.IsDeclined(err)
	if !ok {
		t.Fatalf("a declined charge came back as %v, which a caller would retry", err)
	}
	if declined.IntentID == "" {
		t.Error("the decline names no attempt, so the ledger cannot record it")
	}
	if len(f.Charged()) != 1 {
		t.Errorf("%d charges recorded, want 1", len(f.Charged()))
	}
}
