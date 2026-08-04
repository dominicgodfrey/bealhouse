import { useEffect, useState } from 'react'
import { Link, useParams } from 'react-router-dom'

import { ErrorNote, Layout, Loading } from '../components/Layout'
import { PriceBreakdown } from '../components/PriceBreakdown'
import { fetchBooking } from '../lib/api'
import { formatLong } from '../lib/dates'
import { useAsync } from '../lib/useAsync'

/**
 * The held booking.
 *
 * This is where step 3 ends and step 4 will pick up: the room is genuinely
 * reserved — a row in room_occupancy with an expiry, which the exclusion
 * constraint enforces against everyone else — and the countdown is how long
 * that lasts. The pay button arrives with Stripe.
 */
export function Held() {
  const { code = '' } = useParams()
  const booking = useAsync(() => fetchBooking(code), [code])

  if (booking.loading) return <Layout><Loading what="your booking" /></Layout>
  if (booking.error) return <Layout><ErrorNote error={booking.error} /></Layout>
  if (!booking.data) return null

  const held = booking.data
  const room = held.rooms[0]

  return (
    <Layout>
      <div className="flex max-w-2xl flex-col gap-6">
        <header className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">
            {held.status === 'pending' ? 'Your room is held' : 'Your booking'}
          </h1>
          <p className="text-sm text-neutral-600">
            Booking reference <span className="font-mono font-medium">{held.code}</span>
          </p>
        </header>

        {held.status === 'pending' && <Countdown expiresAt={held.holdExpiresAt} />}

        {held.status === 'expired' && (
          <p className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
            This hold ran out and the room has gone back on sale. Nothing was charged.
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

        {held.status === 'pending' && (
          <p className="rounded-lg bg-neutral-50 px-4 py-3 text-sm text-neutral-600">
            Payment is not connected yet, so this is as far as booking goes today. The room
            is genuinely reserved for you until the hold runs out.
          </p>
        )}

        <Link to="/" className="text-sm text-neutral-600 underline underline-offset-4">
          Back to the inn
        </Link>
      </div>
    </Layout>
  )
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
