import { useState } from 'react'
import { Link, useParams, useSearchParams } from 'react-router'

import { ErrorNote, Layout, Loading } from '../components/Layout'
import { PriceBreakdown } from '../components/PriceBreakdown'
import {
  cancelBooking,
  confirmationPdfUrl,
  fetchManagedBooking,
  type RefundPreview,
} from '../lib/api'
import { formatLong } from '../lib/dates'
import { formatCents } from '../lib/money'
import { useAsync } from '../lib/useAsync'

/**
 * The guest's own page for a booking, reached from the link in their
 * confirmation (decision #19).
 *
 * The token in the URL is what authorises this: a booking code is short enough
 * to read out over the phone and is not a secret. Everything here goes back to
 * the server with it, and nothing is decided in the browser — the refund shown
 * is the server's arithmetic, and cancelling asks the server to do it again
 * rather than sending it the figure from this page.
 */
export function Manage() {
  const { code = '' } = useParams()
  const [params] = useSearchParams()
  const token = params.get('t') ?? ''

  const managed = useAsync(() => fetchManagedBooking(code, token), [code, token])
  const [cancelled, setCancelled] = useState<RefundPreview | null>(null)

  if (managed.loading) return <Layout><Loading what="your booking" /></Layout>
  if (managed.error) return <Layout><ErrorNote error={managed.error} /></Layout>
  if (!managed.data) return null

  const { booking, refund } = managed.data
  const room = booking.rooms[0]

  return (
    <Layout>
      <div className="flex max-w-2xl flex-col gap-6">
        <header className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">Your booking</h1>
          <p className="text-sm text-neutral-600">
            Booking reference <span className="font-mono font-medium">{booking.code}</span>
          </p>
        </header>

        {cancelled ? (
          <Cancelled refund={cancelled} />
        ) : (
          managed.data.reason && (
            <p className="rounded-lg border border-amber-200 bg-amber-50 px-4 py-3 text-sm text-amber-900">
              {managed.data.reason}
            </p>
          )
        )}

        <section className="flex flex-col gap-4 rounded-lg border border-neutral-200 p-4">
          <div>
            <h2 className="font-medium">{room?.name}</h2>
            <p className="text-sm text-neutral-600">
              {formatLong(booking.checkin)} → {formatLong(booking.checkout)} · {booking.nights}{' '}
              {booking.nights === 1 ? 'night' : 'nights'}
            </p>
            <p className="text-sm text-neutral-600">
              {booking.guests} {booking.guests === 1 ? 'guest' : 'guests'}
              {booking.withPet && ' · with a pet'}
            </p>
          </div>

          <PriceBreakdown
            quote={booking.quote}
            nightlyCents={room?.nightlyCents ?? []}
            checkin={booking.checkin}
            payment={{
              chargeNowCents: booking.chargeNowCents,
              balanceChargeOn: booking.balanceChargeOn,
            }}
          />
        </section>

        {/*
          A plain anchor, not a fetch: the browser's own download handling is
          what puts this in a guest's files, and it carries the same token the
          endpoint behind it asks for.
        */}
        <a
          href={confirmationPdfUrl(code, token)}
          className="self-start text-sm text-neutral-600 underline underline-offset-4"
        >
          Download confirmation (PDF)
        </a>

        {managed.data.cancellable && !cancelled && (
          <CancelPanel
            code={code}
            token={token}
            refund={refund}
            onCancelled={setCancelled}
          />
        )}

        <Link to="/" className="text-sm text-neutral-600 underline underline-offset-4">
          Back to the inn
        </Link>
      </div>
    </Layout>
  )
}

/**
 * The refund, then the button.
 *
 * In that order and never the other way round: what a guest gets back depends on
 * which side of seven days they are (decision #9), and a cancel button that did
 * not say so first would be one people press and then telephone about.
 */
function CancelPanel({
  code,
  token,
  refund,
  onCancelled,
}: {
  code: string
  token: string
  refund: RefundPreview
  onCancelled: (r: RefundPreview) => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      onCancelled(await cancelBooking(code, token))
    } catch (err) {
      setError(err instanceof Error ? err : new Error('The booking could not be cancelled'))
    } finally {
      setWorking(false)
    }
  }

  return (
    <section className="flex flex-col gap-3 rounded-lg border border-neutral-200 p-4">
      <h2 className="font-medium">Cancel this booking</h2>

      <dl className="flex flex-col gap-1 text-sm">
        <Row label="Paid so far" value={formatCents(refund.paidCents)} />
        <Row label="We would keep" value={formatCents(refund.retainedCents)} />
        <Row label="Refund to your card" value={formatCents(refund.refundCents)} strong />
      </dl>

      <p className="text-sm text-neutral-600">
        {refund.late
          ? 'Cancelling within seven days of arrival forfeits the deposit.'
          : 'Cancelling this far ahead refunds everything paid, less what the card processor kept.'}
      </p>

      {error && <ErrorNote error={error} />}

      {confirming ? (
        <div className="flex flex-col gap-2 sm:flex-row">
          <button
            type="button"
            onClick={submit}
            disabled={working}
            className="rounded-lg bg-red-700 px-4 py-3 text-sm font-medium text-white disabled:opacity-60"
          >
            {working ? 'Cancelling…' : `Yes, cancel and refund ${formatCents(refund.refundCents)}`}
          </button>
          <button
            type="button"
            onClick={() => setConfirming(false)}
            disabled={working}
            className="rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
          >
            Keep my booking
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setConfirming(true)}
          className="self-start rounded-lg border border-neutral-300 px-4 py-3 text-sm font-medium"
        >
          Cancel this booking
        </button>
      )}
    </section>
  )
}

/**
 * What a guest sees the moment it is done.
 *
 * The money is returned by a queued job rather than by the request that
 * cancelled the stay, so this says the refund is on its way rather than that it
 * has landed — which is also true of the days a card takes to show it.
 */
function Cancelled({ refund }: { refund: RefundPreview }) {
  return (
    <p className="rounded-lg border border-green-200 bg-green-50 px-4 py-3 text-sm text-green-900">
      Your booking is cancelled.{' '}
      {refund.refundCents > 0
        ? `${formatCents(refund.refundCents)} is on its way back to the card you paid with; it usually takes a few days to appear.`
        : 'Nothing is being refunded, in line with the cancellation terms above.'}{' '}
      We have emailed you a copy.
    </p>
  )
}

function Row({ label, value, strong }: { label: string; value: string; strong?: boolean }) {
  return (
    <div className="flex justify-between">
      <dt className={strong ? 'font-medium' : 'text-neutral-600'}>{label}</dt>
      <dd className={strong ? 'font-medium' : ''}>{value}</dd>
    </div>
  )
}
