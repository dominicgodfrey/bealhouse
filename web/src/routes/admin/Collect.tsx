import { Elements, PaymentElement, useElements, useStripe } from '@stripe/react-stripe-js'
import { loadStripe, type Stripe } from '@stripe/stripe-js'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'

import { collectPayment, type CardPayment } from '../../lib/console'
import { devPay } from '../../lib/api'
import { formatCents } from '../../lib/money'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { Aside, Button, Card, Screen } from './ui'

/**
 * Taking a card the guest is reading out over the telephone.
 *
 * **The number never reaches this application.** What is on screen is Stripe's
 * own form, in their iframe, and the details go from it to Stripe directly —
 * exactly as they do when a guest pays on the website. This bundle cannot read
 * the field, this server never sees the digits, and there is no endpoint
 * anywhere in it that would accept them. That is what keeps the inn inside PCI
 * SAQ-A, and it is not a detail to trade away for a form that looks more like
 * the one at the front desk.
 *
 * The payment is declared to Stripe as a telephone order, which is what stops
 * the bank sending a 3-D Secure challenge — that challenge goes to the guest's
 * phone, and the guest is on the phone, talking to the person at the keyboard.
 */
export function Collect() {
  const { code = '' } = useParams()
  const payment = useAsync(() => collectPayment(code), [code])

  return (
    <Screen
      title="Take a card"
      subtitle={
        <>
          Booking <span className="font-mono">{code}</span>
        </>
      }
      actions={
        <Link to={`/admin/bookings/${code}`}>
          <Button>Back to the booking</Button>
        </Link>
      }
    >
      {payment.loading && <Loading what="the card form" />}
      {payment.error && <ErrorNote error={payment.error} />}
      {payment.data && <Form payment={payment.data} code={code} />}
    </Screen>
  )
}

/**
 * loadStripe fetches a script and should happen once per key, not once per
 * render. Cached here rather than at module scope because the key arrives with
 * the payment rather than being baked into the build.
 */
const stripeByKey = new Map<string, Promise<Stripe | null>>()

function stripeFor(publishableKey: string): Promise<Stripe | null> {
  const existing = stripeByKey.get(publishableKey)
  if (existing) return existing

  const loading = loadStripe(publishableKey)
  stripeByKey.set(publishableKey, loading)
  return loading
}

function Form({ payment, code }: { payment: CardPayment; code: string }) {
  return (
    <>
      <Card>
        <p className="text-lg font-medium">{formatCents(payment.amountCents)}</p>
        <p className="text-sm text-neutral-600">
          The amount is the booking's, not something typed here — the same figure the guest would
          see paying it themselves.
        </p>
      </Card>

      <Aside>
        Read the card details into the form below as the guest gives them. They go straight to
        Stripe from this page; nothing about the card is stored by the inn, and nobody here can see
        the number afterwards.
      </Aside>

      {payment.devPayment ? (
        <StandIn code={code} />
      ) : (
        <Elements
          stripe={stripeFor(payment.publishableKey)}
          options={{ clientSecret: payment.clientSecret }}
        >
          <CardForm code={code} />
        </Elements>
      )}
    </>
  )
}

function CardForm({ code }: { code: string }) {
  const stripe = useStripe()
  const elements = useElements()
  const navigate = useNavigate()

  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!stripe || !elements) return

    setWorking(true)
    setError(null)

    // redirect: 'if_required' rather than a return URL, because the owner is
    // standing at this screen with a guest on the telephone. Nothing here should
    // navigate away to a bank's page — and with a telephone order there is
    // nobody at the far end who could complete one if it did.
    const result = await stripe.confirmPayment({ elements, redirect: 'if_required' })

    if (result.error) {
      setError(new Error(result.error.message ?? 'That card was refused.'))
      setWorking(false)
      return
    }

    // Nothing here confirms anything. The webhook records the money against the
    // booking, exactly as it does for a guest paying on the website, so a
    // browser closed at the wrong moment costs nothing.
    navigate(`/admin/bookings/${code}`)
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-4">
      {error && <ErrorNote error={error} />}

      <div className="rounded-lg border border-neutral-200 bg-white p-4">
        <PaymentElement />
      </div>

      <Button kind="primary" type="submit" disabled={!stripe || working}>
        {working ? 'Taking the payment…' : 'Charge this card'}
      </Button>

      <p className="text-sm text-neutral-600">
        If it is refused, nothing has been taken and the booking is unaffected — the room is still
        theirs and you can try another card or send them a link instead.
      </p>
    </form>
  )
}

/**
 * The stand-in against the development processor.
 *
 * No card form, because there is no processor to mount one against. The button
 * behind it builds a properly signed webhook delivery and sends it through the
 * same handler, signature check and state machine a real payment would use — so
 * everything past this point is exercised for real even though no money exists.
 */
function StandIn({ code }: { code: string }) {
  const navigate = useNavigate()
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      await devPay(code)
      navigate(`/admin/bookings/${code}`)
    } catch (err) {
      setError(err instanceof Error ? err : new Error('That did not work.'))
      setWorking(false)
    }
  }

  return (
    <Card tone="alarm">
      {error && <ErrorNote error={error} />}
      <p className="text-sm font-medium">
        This server has no card processor configured, so there is no card form.
      </p>
      <p className="text-sm">
        The button below records the payment as though a card had been taken. No money moves, and
        it exists only on a development machine.
      </p>
      <Button kind="danger" onClick={submit} disabled={working}>
        {working ? 'Recording…' : 'Pretend the card went through'}
      </Button>
    </Card>
  )
}
