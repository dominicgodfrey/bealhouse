import { useState } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'

import { ErrorNote, Layout, Loading } from '../components/Layout'
import { PriceBreakdown } from '../components/PriceBreakdown'
import { createBooking, fetchRoom, type RoomDetail } from '../lib/api'
import { formatLong } from '../lib/dates'
import { parseStay, staySearch } from '../lib/stay'
import type { Stay } from '../lib/api'
import { useAsync } from '../lib/useAsync'

/**
 * The confirm screen: what you are booking, what it costs, and who you are.
 *
 * No payment. The submit button creates a booking and a hold on the room
 * (build-order step 3); collecting money is step 4, and none of this needs a
 * Stripe account to work.
 */
export function Confirm() {
  const { slug = '' } = useParams()
  const [params] = useSearchParams()
  const stay = parseStay(params)

  const room = useAsync(() => fetchRoom(slug, stay ?? undefined), [
    slug,
    stay?.checkin,
    stay?.checkout,
    stay?.guests,
    stay?.withPet,
  ])

  return (
    <Layout>
      {!stay && <p className="text-neutral-600">Those dates are incomplete. Start from the search.</p>}
      {stay && room.loading && <Loading what="your stay" />}
      {stay && room.error && <ErrorNote error={room.error} />}
      {stay && room.data && <ConfirmForm room={room.data} stay={stay} />}
    </Layout>
  )
}

function ConfirmForm({ room, stay }: { room: RoomDetail; stay: Stay }) {
  const navigate = useNavigate()

  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [phone, setPhone] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)

  if (!room.available) {
    return (
      <div className="flex flex-col gap-3">
        <h1 className="text-2xl font-semibold tracking-tight">That room is no longer available</h1>
        <p className="text-neutral-600">
          {room.name} cannot be booked for {formatLong(stay.checkin)} to{' '}
          {formatLong(stay.checkout)}.
        </p>
        <Link
          to={`/search?${staySearch(stay)}`}
          className="text-sm text-neutral-700 underline underline-offset-4"
        >
          See what else is free
        </Link>
      </div>
    )
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault()
    setError(null)
    setSubmitting(true)

    try {
      const booking = await createBooking({
        roomSlug: room.slug,
        checkin: stay.checkin,
        checkout: stay.checkout,
        guests: stay.guests,
        withPet: stay.withPet,
        // The total the guest is looking at. The server prices the stay itself
        // and rejects the booking if the two disagree, so nobody is charged a
        // number they were never shown.
        expectedTotalCents: room.quote.totalCents,
        guest: { name, email, phone },
      })
      navigate(`/bookings/${booking.code}`)
    } catch (e) {
      setError(e instanceof Error ? e.message : 'The booking could not be completed.')
      setSubmitting(false)
    }
  }

  return (
    <div className="flex flex-col gap-8">
      <h1 className="text-2xl font-semibold tracking-tight">Confirm your stay</h1>

      <div className="grid gap-8 sm:grid-cols-[1fr_1fr]">
        <section className="flex flex-col gap-4 rounded-lg border border-neutral-200 p-4">
          <div>
            <h2 className="font-medium">{room.name}</h2>
            <p className="text-sm text-neutral-600">
              {formatLong(stay.checkin)} → {formatLong(stay.checkout)}
            </p>
            <p className="text-sm text-neutral-600">
              {stay.guests} {stay.guests === 1 ? 'guest' : 'guests'}
              {stay.withPet && ' · with a pet'}
            </p>
          </div>

          <PriceBreakdown
            quote={room.quote}
            nightlyCents={room.nightlyCents}
            checkin={stay.checkin}
          />
        </section>

        <form className="flex flex-col gap-4" onSubmit={submit}>
          <Field label="Name" value={name} onChange={setName} autoComplete="name" required />
          <Field
            label="Email"
            value={email}
            onChange={setEmail}
            type="email"
            autoComplete="email"
            required
            hint="Your confirmation goes here."
          />
          <Field
            label="Phone"
            value={phone}
            onChange={setPhone}
            type="tel"
            autoComplete="tel"
            hint="Optional, in case we need to reach you about your arrival."
          />

          {error && <ErrorNote error={new Error(error)} />}

          <button
            type="submit"
            disabled={submitting}
            className="rounded-lg bg-neutral-900 px-4 py-3 text-sm font-medium text-white hover:bg-neutral-700 disabled:bg-neutral-300"
          >
            {submitting ? 'Holding the room…' : 'Hold this room'}
          </button>

          <p className="text-xs text-neutral-500">
            Nothing is charged yet. We hold the room for you while you finish.
          </p>
        </form>
      </div>
    </div>
  )
}

type FieldProps = {
  label: string
  value: string
  onChange: (value: string) => void
  type?: string
  autoComplete?: string
  required?: boolean
  hint?: string
}

function Field({ label, value, onChange, type = 'text', autoComplete, required, hint }: FieldProps) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-sm font-medium">
        {label}
        {!required && <span className="font-normal text-neutral-500"> (optional)</span>}
      </span>
      <input
        className="rounded-lg border border-neutral-300 px-3 py-2 text-sm outline-none focus:border-neutral-900"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        type={type}
        autoComplete={autoComplete}
        required={required}
      />
      {hint && <span className="text-xs text-neutral-500">{hint}</span>}
    </label>
  )
}
