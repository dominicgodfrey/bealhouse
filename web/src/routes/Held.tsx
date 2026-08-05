import { useEffect, useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'

import { ErrorNote, Layout, Loading } from '../components/Layout'
import { PriceBreakdown } from '../components/PriceBreakdown'
import { fetchBooking, type Booking } from '../lib/api'
import { formatLong } from '../lib/dates'
import { useAsync } from '../lib/useAsync'

/** How often the page asks whether the webhook has landed yet. */
const POLL_MS = 1500

/**
 * How long to keep asking before saying so.
 *
 * A webhook normally arrives in under a second. Past this the honest thing is
 * to stop spinning and tell the guest what is true: the payment went through,
 * the booking will catch up, and nothing is lost — rather than a spinner that
 * implies something is still happening in this tab.
 */
const POLL_TIMEOUT_MS = 30_000

/**
 * The booking, at every stage it can be in.
 *
 * Held, paid for, or run out. The interesting case is the middle one: a guest
 * arriving here from Stripe has paid, but the stay is confirmed by the webhook
 * rather than by this redirect (ARCHITECTURE.md, step 6), so for a moment the
 * money has moved and the booking has not caught up. That gap is real and this
 * page waits it out rather than pretending either way.
 */
export function Held() {
  const { code = '' } = useParams()
  const [params] = useSearchParams()
  const booking = useAsync(() => fetchBooking(code), [code])

  // Stripe appends this on its way back. Its presence is what tells the page a
  // payment is in flight and worth waiting for; without it a pending booking is
  // just a hold somebody has not paid yet.
  const returning = params.get('redirect_status') === 'succeeded'
  const settled = usePollUntilSettled(code, returning && booking.data?.status === 'pending')

  if (booking.loading) return <Layout><Loading what="your booking" /></Layout>
  if (booking.error) return <Layout><ErrorNote error={booking.error} /></Layout>
  if (!booking.data) return null

  const held = settled.booking ?? booking.data
  const room = held.rooms[0]
  const awaitingWebhook = returning && held.status === 'pending'

  return (
    <Layout>
      <div className="flex max-w-2xl flex-col gap-6">
        <header className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">{heading(held, awaitingWebhook)}</h1>
          <p className="text-sm text-neutral-600">
            Booking reference <span className="font-mono font-medium">{held.code}</span>
          </p>
        </header>

        {awaitingWebhook &&
          (settled.timedOut ? (
            // Deliberately not an error. The payment succeeded — Stripe said so
            // on the way back — and the booking will catch up when the webhook
            // is delivered or retried. Telling a guest who has just been charged
            // that something went wrong would be both alarming and untrue.
            <p className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
              Your payment went through and we are still finishing your booking. Nothing more
              is needed from you — your confirmation email will arrive shortly. Keep the
              reference above if you would like to check with us.
            </p>
          ) : (
            <p className="rounded-lg border border-neutral-200 px-4 py-3 text-sm">
              Payment received — confirming your booking…
            </p>
          ))}

        {held.status === 'confirmed' && (
          <p className="rounded-lg border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-900">
            Your stay is confirmed. A confirmation email is on its way.
          </p>
        )}

        {held.status === 'pending' && !awaitingWebhook && (
          <>
            <Countdown expiresAt={held.holdExpiresAt} />
            <Link
              to={`/bookings/${held.code}/pay`}
              className="rounded-lg bg-neutral-900 px-4 py-3 text-center text-sm font-medium text-white"
            >
              Pay and confirm
            </Link>
          </>
        )}

        {held.status === 'expired' && (
          <p className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
            This hold ran out and the room has gone back on sale. Nothing was charged.
          </p>
        )}

        {held.status === 'cancelled' && (
          <p className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
            This booking was cancelled. Any refund due is settled separately.
          </p>
        )}

        <section className="flex flex-col gap-4 rounded-lg border border-neutral-200 p-4">
          <div>
            <h2 className="font-medium">{room?.name}</h2>
            <p className="text-sm text-neutral-600">
              {formatLong(held.checkin)} → {formatLong(held.checkout)} · {held.nights}{' '}
              {held.nights === 1 ? 'night' : 'nights'}
            </p>
            <p className="text-sm text-neutral-600">
              {held.guests} {held.guests === 1 ? 'guest' : 'guests'}
              {held.withPet && ' · with a pet'}
            </p>
          </div>

          <PriceBreakdown
            quote={held.quote}
            nightlyCents={room?.nightlyCents ?? []}
            checkin={held.checkin}
            payment={{
              chargeNowCents: held.chargeNowCents,
              balanceChargeOn: held.balanceChargeOn,
            }}
          />
        </section>

        <Link to="/" className="text-sm text-neutral-600 underline underline-offset-4">
          Back to the inn
        </Link>
      </div>
    </Layout>
  )
}

function heading(booking: Booking, awaitingWebhook: boolean): string {
  if (awaitingWebhook) return 'Finishing your booking'
  if (booking.status === 'pending') return 'Your room is held'
  return 'Your booking'
}

/**
 * Waits for the webhook to catch up with the payment.
 *
 * The redirect back from Stripe is not what confirms a booking — a guest who
 * closes the tab has still paid, so the webhook has to be the thing that
 * decides. The consequence is this gap, usually well under a second, in which
 * the money has moved and the booking still says pending.
 *
 * Polling rather than anything cleverer: it is a handful of requests over a few
 * seconds on one page, and a websocket to carry them would be a great deal of
 * machinery for a wait most guests never see.
 */
function usePollUntilSettled(code: string, active: boolean) {
  const [booking, setBooking] = useState<Booking | null>(null)
  const [timedOut, setTimedOut] = useState(false)

  useEffect(() => {
    if (!active) return

    let running = true
    const startedAt = Date.now()

    const timer = setInterval(async () => {
      if (!running) return

      if (Date.now() - startedAt > POLL_TIMEOUT_MS) {
        setTimedOut(true)
        clearInterval(timer)
        return
      }

      try {
        const latest = await fetchBooking(code)
        if (!running) return
        if (latest.status !== 'pending') {
          setBooking(latest)
          clearInterval(timer)
        }
      } catch {
        // Swallowed on purpose. The guest has already been charged, so a
        // transient failure to read the booking back is worth another try
        // rather than an error page; the timeout above is what ends the wait.
      }
    }, POLL_MS)

    return () => {
      running = false
      clearInterval(timer)
    }
  }, [code, active])

  return { booking, timedOut }
}

/**
 * How long the hold has left.
 *
 * Counts down from the server's expiry rather than from a duration, so a guest
 * who leaves the tab open and comes back sees the truth instead of a timer that
 * paused with them.
 */
function Countdown({ expiresAt }: { expiresAt?: string }) {
  const [remaining, setRemaining] = useState(() => secondsUntil(expiresAt))

  useEffect(() => {
    const tick = setInterval(() => setRemaining(secondsUntil(expiresAt)), 1000)
    return () => clearInterval(tick)
  }, [expiresAt])

  if (!expiresAt) return null

  if (remaining <= 0) {
    return (
      <p className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
        This hold has run out. Reload to see whether the room is still free.
      </p>
    )
  }

  const minutes = Math.floor(remaining / 60)
  const seconds = remaining % 60

  return (
    <p className="rounded-lg border border-neutral-200 px-4 py-3 text-sm">
      Held for{' '}
      <span className="font-mono font-medium">
        {minutes}:{String(seconds).padStart(2, '0')}
      </span>{' '}
      <span className="text-neutral-600">— nobody else can book it in the meantime.</span>
    </p>
  )
}

function secondsUntil(iso?: string): number {
  if (!iso) return 0
  return Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 1000))
}
