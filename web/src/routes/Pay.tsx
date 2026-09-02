import { Elements, PaymentElement, useElements, useStripe } from '@stripe/react-stripe-js'
import { loadStripe } from '@stripe/stripe-js/pure'
// The type only: `import type` is erased at compile time, so this is not the
// side-effecting import the line above exists to avoid. /pure exports the
// function and not the types.
import type { Stripe } from '@stripe/stripe-js'
import { useState } from 'react'
import { useNavigate, useParams } from 'react-router'

import { ErrorNote, Layout, Loading } from '../components/Layout'
import { devPay, openPayment, type PaymentIntent } from '../lib/api'
import { formatCents } from '../lib/money'
import { useAsync } from '../lib/useAsync'

/**
 * loadStripe fetches a script and should happen once per key, not once per
 * render. Cached here rather than at module scope because the key arrives with
 * the payment rather than being baked into the build — the same bundle has to
 * work against test keys, live keys, and no keys at all.
 *
 * **The import is `/pure` and that is the load-bearing part.** Importing
 * `@stripe/stripe-js` fetches js.stripe.com as a side effect of the import
 * itself, which its own README says plainly. This app is one bundle with no
 * route splitting, so that side effect ran on every page — the home page, the
 * rooms, the restaurant — and brought Stripe's fraud beacon and its
 * m.stripe.com cookie with it. Lighthouse on the home page is what found it:
 * two Best Practices failures, both this.
 *
 * It was doing that while the inn had no Stripe account at all. Loading it
 * everywhere is Stripe's own recommendation, because browsing signal makes
 * their fraud detection better — but that is a trade to make deliberately for a
 * seven-room inn, not one to inherit from an import, and it sits badly beside
 * self-hosting the webfonts to keep a third party off the critical path.
 *
 * `/pure` moves the fetch to the first `loadStripe` call, so Stripe.js loads on
 * this page and the console's Collect screen and nowhere else. To give up the
 * remaining signal as well, `loadStripe.setLoadParameters({ advancedFraudSignals:
 * false })` — that one moves fraud liability and is not ours to take quietly.
 */
const stripeByKey = new Map<string, Promise<Stripe | null>>()

function stripeFor(publishableKey: string): Promise<Stripe | null> {
  const existing = stripeByKey.get(publishableKey)
  if (existing) return existing

  const loading = loadStripe(publishableKey)
  stripeByKey.set(publishableKey, loading)
  return loading
}

/**
 * The pay page.
 *
 * Opening the payment is a POST with no body: the amount is the server's to
 * derive from the booking, and this page only learns it in order to show the
 * guest the figure their card will see.
 *
 * Nothing here confirms anything. The card form hands off to Stripe, Stripe
 * calls the webhook, and the webhook is what turns a hold into a stay — so a
 * guest who pays and closes the tab is still booked.
 */
export function Pay() {
  const { code = '' } = useParams()
  const payment = useAsync(() => openPayment(code), [code])

  if (payment.loading) return <Layout><Loading what="the payment form" /></Layout>
  if (payment.error) return <Layout><ErrorNote error={payment.error} /></Layout>
  if (!payment.data) return null

  return (
    <Layout>
      <div className="flex max-w-xl flex-col gap-6">
        <header className="flex flex-col gap-1">
          <h1 className="text-2xl font-semibold tracking-tight">Payment</h1>
          <p className="text-sm text-neutral-600">
            Booking reference <span className="font-mono font-medium">{code}</span> ·{' '}
            <span className="font-medium text-neutral-900">
              {formatCents(payment.data.amountCents)}
            </span>{' '}
            due now
          </p>
        </header>

        {payment.data.devPayment ? (
          <StandInPayment code={code} />
        ) : (
          <CardPayment code={code} payment={payment.data} />
        )}
      </div>
    </Layout>
  )
}

function CardPayment({ code, payment }: { code: string; payment: PaymentIntent }) {
  if (!payment.publishableKey) {
    return (
      <p className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
        The card form cannot load: this server has a payment processor configured but no
        publishable key to identify it with. Nothing has been charged.
      </p>
    )
  }

  return (
    <Elements
      stripe={stripeFor(payment.publishableKey)}
      options={{ clientSecret: payment.clientSecret }}
    >
      <CardForm code={code} />
    </Elements>
  )
}

function CardForm({ code }: { code: string }) {
  const stripe = useStripe()
  const elements = useElements()
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    if (!stripe || !elements) return

    setSubmitting(true)
    setError(null)

    // On success the browser leaves for the card's bank and comes back to
    // return_url. Nothing after this line runs in that case, which is exactly
    // why the webhook — not this page — is what confirms the booking.
    const result = await stripe.confirmPayment({
      elements,
      confirmParams: { return_url: `${window.location.origin}/bookings/${code}` },
    })

    // Only reached when the payment failed before leaving: a declined card, a
    // form that did not validate. Stripe's own message is the useful one.
    setError(result.error.message ?? 'That payment could not be completed.')
    setSubmitting(false)
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      <PaymentElement />

      {error && (
        <p className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
          {error}
        </p>
      )}

      <button
        type="submit"
        disabled={!stripe || submitting}
        className="rounded-lg bg-neutral-900 px-4 py-3 text-sm font-medium text-white disabled:opacity-50"
      >
        {submitting ? 'Working…' : 'Pay and confirm'}
      </button>

      <p className="text-xs text-neutral-500">
        Card details go straight to Stripe and never reach this server.
      </p>
    </form>
  )
}

/**
 * The stand-in for the card form when the server is running against the fake
 * processor.
 *
 * Everything past this button is real: it asks the server to deliver a properly
 * signed webhook to its own handler, which verifies the signature and runs the
 * same state machine a live payment would. Only the card is imaginary.
 */
function StandInPayment({ code }: { code: string }) {
  const navigate = useNavigate()
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  async function pretend() {
    setSubmitting(true)
    setError(null)
    try {
      await devPay(code)
      navigate(`/bookings/${code}?redirect_status=succeeded`)
    } catch (thrown) {
      setError((thrown as Error).message)
      setSubmitting(false)
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <p className="rounded-lg border border-amber-300 bg-amber-50 px-4 py-3 text-sm text-amber-900">
        <strong>No payment processor is connected.</strong> This button stands in for the card
        form: it confirms the booking through the real webhook without any money moving. It
        exists only in development.
      </p>

      {error && (
        <p className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-800">
          {error}
        </p>
      )}

      <button
        type="button"
        onClick={pretend}
        disabled={submitting}
        className="rounded-lg bg-neutral-900 px-4 py-3 text-sm font-medium text-white disabled:opacity-50"
      >
        {submitting ? 'Working…' : 'Pretend the card was accepted'}
      </button>
    </div>
  )
}
