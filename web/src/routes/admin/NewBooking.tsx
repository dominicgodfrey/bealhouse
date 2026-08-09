import { useState } from 'react'
import { useNavigate } from 'react-router'

import { createStay, fetchRates, type ManualBooking, type Settlement } from '../../lib/console'
import { addDays, today } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote } from '../../components/Layout'
import { useConsole } from './Console'
import { Aside, Button, Card, Field, Input, Screen, Select, report } from './ui'

/**
 * A reservation the owner took on the phone.
 *
 * It goes through the same `booking.Create` a guest does, with Manual set: the
 * same availability re-check, the same pricing from the same rate calendar, and
 * the same claim on the room through the exclusion constraint. An owner is
 * allowed to take a booking the website would not offer them — they are not
 * allowed to double-book a room, and the database is what enforces that rather
 * than this form.
 */
export function NewBooking() {
  const navigate = useNavigate()
  const { refresh } = useConsole()

  // The rate board is the cheapest way to get the room list, and this screen
  // needs nothing else from it.
  const rates = useAsync(fetchRates, [])

  const [form, setForm] = useState<ManualBooking>({
    roomSlug: '',
    checkin: addDays(today(), 1),
    checkout: addDays(today(), 3),
    guests: 2,
    withPet: false,
    name: '',
    email: '',
    phone: '',
    // The option that promises nothing, so an owner who does not read this
    // section has not silently invoiced somebody.
    payment: 'offline',
  })

  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  function set<K extends keyof typeof form>(key: K, value: (typeof form)[K]) {
    setForm((f) => ({ ...f, [key]: value }))
  }

  async function submit() {
    setWorking(true)
    setError(null)
    try {
      const made = await createStay(form)
      // Straight to the card form when that is how it is being paid, because
      // the guest is still on the telephone waiting to read out a number.
      navigate(
        form.payment === 'card'
          ? `/admin/bookings/${made.code}/collect`
          : `/admin/bookings/${made.code}`,
      )
    } catch (err) {
      report(err, refresh, setError)
      setWorking(false)
    }
  }

  return (
    <Screen title="Take a booking" subtitle="For a reservation made on the phone or in person.">
      <Aside>
        The guest gets the same confirmation as a booking made on the site, with their link to view
        or cancel it, and you get your usual copy. Nothing is scheduled to charge them later, and
        they will still get the note on the morning they leave.
      </Aside>

      <Card>
        {error && <ErrorNote error={error} />}
        {rates.error && <ErrorNote error={rates.error} />}

        <Field label="Room">
          <Select value={form.roomSlug} onChange={(e) => set('roomSlug', e.target.value)}>
            <option value="">Choose a room…</option>
            {rates.data?.rooms.map((room) => (
              <option key={room.slug} value={room.slug}>
                {room.name}
              </option>
            ))}
          </Select>
        </Field>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Check-in">
            <Input
              type="date"
              value={form.checkin}
              onChange={(e) => set('checkin', e.target.value)}
            />
          </Field>
          <Field label="Check-out" hint="Not a night — the morning they leave.">
            <Input
              type="date"
              value={form.checkout}
              onChange={(e) => set('checkout', e.target.value)}
            />
          </Field>
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="Guests">
            <Input
              type="number"
              min={1}
              value={form.guests}
              onChange={(e) => set('guests', Number(e.target.value))}
            />
          </Field>
          <label className="flex items-center gap-2 self-end pb-3 text-sm">
            <input
              type="checkbox"
              checked={form.withPet}
              onChange={(e) => set('withPet', e.target.checked)}
            />
            Bringing a pet
          </label>
        </div>

        <Field label="Name">
          <Input value={form.name} onChange={(e) => set('name', e.target.value)} />
        </Field>
        <Field label="Email" hint="Used to recognise them next time, and for the manage link if you send one.">
          <Input
            type="email"
            value={form.email}
            onChange={(e) => set('email', e.target.value)}
          />
        </Field>
        {/* The server refuses a booking without one, website or console alike,
            so the hint is here rather than a surprise at "Book it". */}
        <Field label="Phone" hint="Required — every booking needs a number the inn can ring.">
          <Input type="tel" value={form.phone} onChange={(e) => set('phone', e.target.value)} />
        </Field>

        <Payment chosen={form.payment} onChoose={(payment) => set('payment', payment)} />

        <Button kind="primary" onClick={submit} disabled={working || !form.roomSlug}>
          {working ? 'Taking the room…' : 'Book it'}
        </Button>

        <p className="text-sm text-neutral-600">
          If the room is not actually free, or the stay is shorter than the minimum for those
          nights, this is refused — the same check the website runs.
        </p>
      </Card>
    </Screen>
  )
}

/**
 * How the money is going to arrive.
 *
 * Three, because there are three real answers and the owner knows which one
 * while the guest is still on the line. The stay is identical either way —
 * confirmed, room held — so this only decides what happens next, and the
 * descriptions say so rather than making it sound like a commitment.
 */
function Payment({
  chosen,
  onChoose,
}: {
  chosen: Settlement
  onChoose: (payment: Settlement) => void
}) {
  const options: { value: Settlement; label: string; hint: string }[] = [
    {
      value: 'offline',
      label: 'Settling some other way',
      hint: 'Cash, a cheque, a transfer, or an arrangement. The booking shows what is owed and nothing chases it.',
    },
    {
      value: 'link',
      label: 'Email them a link to pay',
      hint: 'They pay it themselves, whenever. The room is theirs regardless — nothing expires.',
    },
    {
      value: 'card',
      label: 'Take their card now',
      hint: 'They read the number out and you key it in on the next screen. It goes straight to Stripe.',
    },
  ]

  return (
    <fieldset className="flex flex-col gap-2 rounded-lg bg-neutral-50 p-3">
      <legend className="text-sm font-medium">How are they paying?</legend>

      {options.map((option) => (
        <label key={option.value} className="flex gap-2 text-sm">
          <input
            type="radio"
            name="payment"
            checked={chosen === option.value}
            onChange={() => onChoose(option.value)}
            className="mt-1"
          />
          <span>
            <span className="font-medium">{option.label}</span>
            <br />
            <span className="text-neutral-600">{option.hint}</span>
          </span>
        </label>
      ))}
    </fieldset>
  )
}
