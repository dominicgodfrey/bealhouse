import { useState } from 'react'
import { Link, useParams } from 'react-router'

import {
  cancelStay,
  fetchStay,
  refundStay,
  requestPayment,
  type BookingDetail as Detail,
} from '../../lib/console'
import { formatInstant } from '../../lib/admin'
import { formatLong, formatShort } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import {
  Aside,
  Button,
  Card,
  Empty,
  Field,
  Input,
  Money,
  Screen,
  Section,
  StatusPill,
  inputToCents,
  report,
  useReload,
} from './ui'

/**
 * One reservation, opened.
 *
 * Two ways money leaves from this page, and they are deliberately different
 * shapes: cancelling runs decision #9's policy and shows the number before
 * asking, while a manual refund is an amount somebody typed. Bending the policy
 * quietly would make the two indistinguishable afterwards, and the ledger is
 * what an owner reaches for when a charge is disputed.
 */
export function BookingDetail() {
  const { code = '' } = useParams()
  const { refresh } = useConsole()
  const [nonce, reload] = useReload()

  const booking = useAsync(() => fetchStay(code), [code, nonce])

  return (
    <Screen
      title={booking.data?.stay.guestName ?? code}
      subtitle={
        <>
          <span className="font-mono">{code}</span>
          {booking.data && (
            <>
              {' · '}
              <StatusPill status={booking.data.stay.status} />
            </>
          )}
        </>
      }
      actions={
        <Link to="/admin/bookings">
          <Button>All reservations</Button>
        </Link>
      }
    >
      {booking.loading && <Loading what="the booking" />}
      {booking.error && <ErrorNote error={booking.error} />}

      {booking.data && (
        <>
          <Stay detail={booking.data} />
          <Quote detail={booking.data} />
          <Collecting detail={booking.data} onSignedOut={refresh} />
          <Ledger detail={booking.data} />
          <Cancel detail={booking.data} onDone={reload} onSignedOut={refresh} />
          <Refund detail={booking.data} onDone={reload} onSignedOut={refresh} />
        </>
      )}
    </Screen>
  )
}

function Stay({ detail }: { detail: Detail }) {
  const { stay } = detail

  return (
    <Section title="The stay">
      <Card tone={stay.chargeFailed ? 'alarm' : 'plain'}>
        <p className="text-sm">
          {formatLong(stay.checkin)} → {formatLong(stay.checkout)}
          <br />
          <span className="text-neutral-600">
            {stay.nights} {stay.nights === 1 ? 'night' : 'nights'} · {stay.guests}{' '}
            {stay.guests === 1 ? 'guest' : 'guests'}
            {stay.withPet && ' · with a pet'}
          </span>
        </p>

        {detail.rooms.map((room) => (
          <p key={room.slug} className="text-sm">
            <span className="font-medium">{room.name}</span>
            {room.view && <span className="text-neutral-600"> · {room.view}</span>}
          </p>
        ))}

        <div className="flex flex-col gap-1 text-sm">
          <a href={`mailto:${stay.guestEmail}`} className="underline">
            {stay.guestEmail}
          </a>
          {stay.guestPhone && (
            <a href={`tel:${stay.guestPhone}`} className="underline">
              {stay.guestPhone}
            </a>
          )}
          {stay.guestId && (
            <Link to={`/admin/guests/${stay.guestId}`} className="underline">
              Their history and notes →
            </Link>
          )}
        </div>

        {detail.holdExpiresAt && (
          <Aside>
            This is a hold, not a booking. It reserves the room until{' '}
            {formatInstant(detail.holdExpiresAt)}, and the sweeper takes the room back after that.
          </Aside>
        )}

        {stay.chargeFailed && (
          <p className="text-sm font-medium text-red-900">
            The balance charge was refused on {formatInstant(stay.chargeFailed)}. The stay is still
            confirmed and the guest has been emailed. Somebody has to ring them.
          </p>
        )}
      </Card>
    </Section>
  )
}

/**
 * The quote as it was snapshotted when the guest booked.
 *
 * Nothing here is recomputed. A season edited since then cannot change what this
 * says, which is the whole reason booking_rooms carries its own nightly prices —
 * so this page, the email and the PDF cannot disagree.
 */
function Quote({ detail }: { detail: Detail }) {
  const { quote, stay } = detail

  return (
    <Section title="What it costs" note="Snapshotted when the guest booked. A rate change since then cannot reach it.">
      <Card>
        <Line label={`Room, ${quote.nights} ${quote.nights === 1 ? 'night' : 'nights'}`} cents={quote.roomSubtotalCents} />
        {quote.petFeeCents > 0 && <Line label="Pet fee" cents={quote.petFeeCents} />}
        <Line label="Tax" cents={quote.taxCents} />
        <Line label="Total" cents={quote.totalCents} strong />

        <hr className="border-neutral-200" />

        <Line label="Deposit" cents={quote.depositCents} />
        <Line label="Balance" cents={quote.balanceCents} />
        <Line label="Collected" cents={stay.paidCents} strong />
        {stay.outstandingCents > 0 && (
          <Line label="Still outstanding" cents={stay.outstandingCents} strong />
        )}

        {stay.balanceChargeOn ? (
          <p className="text-sm text-neutral-600">
            The balance comes off the saved card on {formatShort(stay.balanceChargeOn)}.
          </p>
        ) : (
          <p className="text-sm text-neutral-600">
            No scheduled charge: this stay was either paid in full at booking, or taken by phone and
            settled outside the system.
          </p>
        )}
      </Card>
    </Section>
  )
}

function Line({ label, cents, strong }: { label: string; cents: number; strong?: boolean }) {
  return (
    <div className={`flex justify-between text-sm ${strong ? 'font-medium' : 'text-neutral-600'}`}>
      <span>{label}</span>
      <span>
        <Money cents={cents} />
      </span>
    </div>
  )
}

/**
 * Getting the money in on a stay that still owes some.
 *
 * Only shown when there is something outstanding and nothing already scheduled
 * to collect it — which is exactly a booking taken over the telephone. A website
 * booking's balance comes off the card on file at T-7 and needs no button, and
 * offering one would be how a guest ends up paying twice.
 */
function Collecting({ detail, onSignedOut }: { detail: Detail; onSignedOut: () => void }) {
  const { stay } = detail
  const [working, setWorking] = useState(false)
  const [sent, setSent] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const collectable =
    stay.status === 'confirmed' && stay.outstandingCents > 0 && !stay.balanceChargeOn

  if (!collectable) return null

  async function send() {
    setWorking(true)
    setError(null)
    try {
      await requestPayment(stay.code)
      setSent(true)
    } catch (err) {
      report(err, onSignedOut, setError)
    } finally {
      setWorking(false)
    }
  }

  return (
    <Section
      title="Collecting what is owed"
      note="Nothing is scheduled against a card for this booking, so it is settled by hand."
    >
      <Card>
        <p className="text-sm">
          <Money cents={stay.outstandingCents} /> outstanding.
        </p>

        {error && <ErrorNote error={error} />}
        {sent && (
          <p className="text-sm text-emerald-900">
            Sent. They can pay it whenever — the room is theirs either way, and nothing expires.
          </p>
        )}

        <div className="flex flex-wrap gap-2">
          <Link to={`/admin/bookings/${stay.code}/collect`}>
            <Button kind="primary">Take a card now</Button>
          </Link>
          <Button onClick={send} disabled={working}>
            {working ? 'Sending…' : sent ? 'Send it again' : 'Email them a link'}
          </Button>
        </div>
      </Card>
    </Section>
  )
}

/**
 * The ledger.
 *
 * Refunds are their own rows rather than reductions (decision #25): what was
 * collected only ever grows. A history that quietly netted out could not answer
 * "what did we actually charge this card", which is the question a dispute asks.
 */
function Ledger({ detail }: { detail: Detail }) {
  return (
    <Section title="Payments">
      {detail.payments.length === 0 ? (
        <Empty>Nothing has been charged against this booking.</Empty>
      ) : (
        detail.payments.map((payment, i) => (
          <Card key={`${payment.stripeId}:${payment.status}:${i}`}>
            <div className="flex justify-between text-sm">
              <span className="font-medium">
                {payment.kind} · {payment.status}
              </span>
              <span>
                <Money cents={payment.amountCents} />
              </span>
            </div>
            <p className="text-xs break-all text-neutral-500">
              {formatInstant(payment.at)} · {payment.stripeId}
            </p>
          </Card>
        ))
      )}
    </Section>
  )
}

/**
 * Quote before button, on the owner's side too.
 *
 * The figure shown is the same `payments.RefundFor` the guest's own manage page
 * quotes, against the same civil day — so an owner and a guest looking at the
 * same booking on the same day see the same number, which is the property that
 * matters when one of them is on the phone to the other.
 */
function Cancel({
  detail,
  onDone,
  onSignedOut,
}: {
  detail: Detail
  onDone: () => void
  onSignedOut: () => void
}) {
  const [confirming, setConfirming] = useState(false)
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      await cancelStay(detail.stay.code)
      onDone()
    } catch (err) {
      report(err, onSignedOut, setError)
    } finally {
      setWorking(false)
      setConfirming(false)
    }
  }

  if (!detail.cancellable) {
    return (
      <Section title="Cancelling">
        <Aside>{detail.reason ?? 'This stay cannot be cancelled.'}</Aside>
      </Section>
    )
  }

  const refund = detail.refund

  return (
    <Section title="Cancelling">
      <Card>
        {refund && (
          <div className="flex flex-col gap-1">
            <Line label="Collected" cents={refund.paidCents} />
            <Line label="The inn keeps" cents={refund.retainedCents} />
            <Line label="Back to the guest" cents={refund.refundCents} strong />
            <p className="text-sm text-neutral-600">
              {refund.late
                ? 'Inside seven days, so the deposit is forfeit (decision #9).'
                : 'More than seven days out, so everything comes back less the card processor’s cut.'}
            </p>
          </div>
        )}

        {error && <ErrorNote error={error} />}

        {confirming ? (
          <div className="flex flex-col gap-2 sm:flex-row">
            <Button kind="danger" onClick={submit} disabled={working}>
              {working ? 'Cancelling…' : 'Yes, cancel this stay'}
            </Button>
            <Button onClick={() => setConfirming(false)} disabled={working}>
              Keep the booking
            </Button>
          </div>
        ) : (
          <Button kind="danger" onClick={() => setConfirming(true)}>
            Cancel this stay
          </Button>
        )}

        {confirming && (
          <p className="text-sm text-neutral-600">
            This puts the room back on sale, queues the refund and emails the guest — all together,
            so none of them can happen without the others.
          </p>
        )}
      </Card>
    </Section>
  )
}

/**
 * A refund without a cancellation: the no-show, the cut-short visit, the
 * goodwill gesture. The cases decision #9's arithmetic has no branch for.
 *
 * No step-up authentication (decision #15) — it is the owners' call and the
 * phone is locked. The amount is typed rather than defaulted, because an empty
 * box meaning "everything" is the difference between a gesture and a whole
 * stay's money.
 */
function Refund({
  detail,
  onDone,
  onSignedOut,
}: {
  detail: Detail
  onDone: () => void
  onSignedOut: () => void
}) {
  const [amount, setAmount] = useState('')
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)
  const [sent, setSent] = useState(false)

  if (detail.stay.paidCents === 0) return null

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      await refundStay(detail.stay.code, inputToCents(amount))
      setSent(true)
      setAmount('')
      onDone()
    } catch (err) {
      report(err, onSignedOut, setError)
    } finally {
      setWorking(false)
    }
  }

  return (
    <Section
      title="Refund without cancelling"
      note="For a no-show, a shortened stay, or a gesture — anything the cancellation policy does not describe."
    >
      <Card>
        {error && <ErrorNote error={error} />}
        {sent && (
          <p className="text-sm text-emerald-900">
            Queued. It goes back against the payments that collected it, in ledger order, and the
            job retries if the processor is down.
          </p>
        )}

        <Field
          label="How much?"
          hint={
            <>
              At most <Money cents={detail.stay.paidCents} />, which is what has been collected.
            </>
          }
        >
          <Input
            inputMode="decimal"
            value={amount}
            onChange={(e) => setAmount(e.target.value)}
            placeholder="0.00"
          />
        </Field>

        <Button kind="danger" onClick={submit} disabled={working || inputToCents(amount) <= 0}>
          {working ? 'Queueing…' : 'Send this back'}
        </Button>
      </Card>
    </Section>
  )
}
