import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { fetchGuests, fetchStays, type GuestCard, type Stay } from '../../lib/console'
import { formatShort, today } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import {
  Button,
  Card,
  Empty,
  Field,
  Input,
  Money,
  MoneyLine,
  Screen,
  Section,
  Select,
  StatusPill,
} from './ui'

/**
 * Reservations and the people who made them, behind one search box.
 *
 * They were two screens, and the split was a lie about how an owner looks
 * something up: "Sarah rang" is answered by a name, and whether that lands on a
 * stay or on a person depends on what you happen to want next, not on which tab
 * you opened first. One box, both answers.
 *
 * People appear only once something is typed. With nothing in the box the
 * question is "what is coming up", which is a list of stays — the guest list
 * unfiltered is everybody who has ever booked, and that is a directory rather
 * than an answer.
 *
 * Every filter is in the URL rather than in component state, so the flagged link
 * on the Today screen can land here already narrowed, and so a filtered list is
 * something the owner can leave open, reload, or send to the other phone.
 */
export function Bookings() {
  const [params, setParams] = useSearchParams()

  const filter = {
    from: params.get('from') ?? '',
    to: params.get('to') ?? '',
    status: params.get('status') ?? '',
    q: params.get('q') ?? '',
    flagged: params.get('flagged') === 'true',
  }

  function set(key: string, value: string) {
    const next = new URLSearchParams(params)
    if (value) next.set(key, value)
    else next.delete(key)
    setParams(next, { replace: true })
  }

  const stays = useAsync(
    () =>
      fetchStays({
        from: filter.from || undefined,
        to: filter.to || undefined,
        status: filter.status || undefined,
        q: filter.q || undefined,
        flagged: filter.flagged,
      }),
    [filter.from, filter.to, filter.status, filter.q, filter.flagged],
  )

  // Only when there is something to match. Two requests rather than one
  // combined endpoint, because both read models already exist and answer this
  // exact question — a third query joining them would be a second definition of
  // "matches", and the two would drift.
  const guests = useAsync(
    () => (filter.q ? fetchGuests({ q: filter.q }) : Promise.resolve([])),
    [filter.q],
  )

  const searching = filter.q !== ''
  const nothingAnywhere =
    searching && stays.data?.length === 0 && guests.data?.length === 0

  return (
    <Screen
      title="Reservations"
      subtitle="Paid against total on every row. A refused card is red."
      actions={
        <Link to="/admin/bookings/new">
          <Button kind="primary">Take a booking</Button>
        </Link>
      }
    >
      <Search filter={filter} set={set} />

      {filter.flagged && (
        <p className="rounded-lg border border-red-300 bg-red-50 px-4 py-3 text-sm text-red-900">
          Showing only bookings whose balance charge was refused.{' '}
          <button type="button" onClick={() => set('flagged', '')} className="underline">
            Show everything
          </button>
        </p>
      )}

      {/*
        People first when searching. Somebody looking up a name usually wants
        the person — their history, and whatever the owners wrote down — and the
        stays underneath are the same answer at a different altitude.
      */}
      {searching && (guests.data?.length ?? 0) > 0 && (
        <Section title="People">
          {guests.data?.map((guest) => <GuestRow key={guest.id} guest={guest} />)}
        </Section>
      )}
      {guests.error && <ErrorNote error={guests.error} />}

      {stays.loading && <Loading what="reservations" />}
      {stays.error && <ErrorNote error={stays.error} />}

      {searching && (stays.data?.length ?? 0) > 0 ? (
        <Section title="Reservations">
          {stays.data?.map((stay) => <StayRow key={stay.code} stay={stay} />)}
        </Section>
      ) : (
        !searching && stays.data?.map((stay) => <StayRow key={stay.code} stay={stay} />)
      )}

      {nothingAnywhere && <Empty>Nothing matches that — no bookings and nobody.</Empty>}
      {!searching && stays.data?.length === 0 && <Empty>No bookings match that.</Empty>}
    </Screen>
  )
}

/**
 * One person, as the search returns them. The same card the guest list used to
 * show, so the row an owner learned to read has not changed shape — only where
 * it is found.
 */
function GuestRow({ guest }: { guest: GuestCard }) {
  return (
    <Card>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <Link
          to={`/admin/guests/${guest.id}`}
          className="font-medium underline-offset-2 hover:underline"
        >
          {guest.name}
        </Link>
        <span className="text-sm text-neutral-600">
          {guest.stays === 0
            ? 'no stays yet'
            : `${guest.stays} ${guest.stays === 1 ? 'stay' : 'stays'}`}
        </span>
      </div>

      <p className="text-sm text-neutral-600">
        {guest.email}
        {guest.phone && ` · ${guest.phone}`}
      </p>

      <p className="text-sm text-neutral-600">
        <Money cents={guest.lifetimeCents} /> collected
        {guest.lastCheckout && ` · last here ${formatShort(guest.lastCheckout)}`}
        {guest.notes > 0 && ` · ${guest.notes} ${guest.notes === 1 ? 'note' : 'notes'}`}
      </p>
    </Card>
  )
}

function Search({
  filter,
  set,
}: {
  filter: { from: string; to: string; status: string; q: string }
  set: (key: string, value: string) => void
}) {
  const [open, setOpen] = useState(false)

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-neutral-200 bg-white p-4">
      <Field
        label="Find a booking or a guest"
        hint="A code, a name, an email address, a phone number. Returns both."
      >
        <Input
          value={filter.q}
          onChange={(e) => set('q', e.target.value)}
          placeholder="K3F9QX, or Sarah, or sarah@…"
        />
      </Field>

      <button
        type="button"
        onClick={() => setOpen(!open)}
        className="self-start text-sm text-neutral-600 underline"
      >
        {open ? 'Fewer filters' : 'Dates and status'}
      </button>

      {open && (
        <div className="grid gap-3 sm:grid-cols-3">
          <Field label="From" hint="Stays touching this range.">
            <Input type="date" value={filter.from} onChange={(e) => set('from', e.target.value)} />
          </Field>
          <Field label="To">
            <Input type="date" value={filter.to} onChange={(e) => set('to', e.target.value)} />
          </Field>
          <Field label="Status">
            <Select value={filter.status} onChange={(e) => set('status', e.target.value)}>
              <option value="">Any</option>
              <option value="confirmed">Confirmed</option>
              <option value="pending">Pending</option>
              <option value="cancelled">Cancelled</option>
              <option value="expired">Expired</option>
            </Select>
          </Field>

          <div className="sm:col-span-3">
            <Button onClick={() => set('from', today())}>From today</Button>
          </div>
        </div>
      )}
    </div>
  )
}

function StayRow({ stay }: { stay: Stay }) {
  return (
    <Card tone={stay.chargeFailed ? 'alarm' : 'plain'}>
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <Link
          to={`/admin/bookings/${stay.code}`}
          className="font-medium underline-offset-2 hover:underline"
        >
          {stay.guestName}
        </Link>
        <div className="flex items-center gap-2">
          <StatusPill status={stay.status} />
          <span className="font-mono text-xs text-neutral-500">{stay.code}</span>
        </div>
      </div>

      <div className="text-sm text-neutral-600">
        {formatShort(stay.checkin)} → {formatShort(stay.checkout)} · {stay.rooms || '—'} ·{' '}
        {stay.guests} {stay.guests === 1 ? 'guest' : 'guests'}
      </div>

      <MoneyLine stay={stay} />

      {stay.balanceChargeOn && stay.status === 'confirmed' && !stay.chargeFailed && (
        <p className="text-sm text-neutral-500">
          Balance comes off the card on {formatShort(stay.balanceChargeOn)}
          {stay.warned ? '' : ' · the T-8 warning has not gone out yet'}
        </p>
      )}
    </Card>
  )
}
