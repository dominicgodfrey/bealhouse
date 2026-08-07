import { useState } from 'react'
import { Link, useSearchParams } from 'react-router'

import { fetchStays, type Stay } from '../../lib/console'
import { formatShort, today } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { Button, Card, Empty, Field, Input, MoneyLine, Screen, Select, StatusPill } from './ui'

/**
 * Upcoming reservations — an explicit requirement, and the screen the owner
 * lives in.
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

      {stays.loading && <Loading what="reservations" />}
      {stays.error && <ErrorNote error={stays.error} />}

      {stays.data?.length === 0 && <Empty>No bookings match that.</Empty>}
      {stays.data?.map((stay) => <StayRow key={stay.code} stay={stay} />)}
    </Screen>
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
      <Field label="Find a booking" hint="A code, a name, an email address, a phone number.">
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
