import { useMemo, useState } from 'react'
import { Link } from 'react-router'

import {
  createBlock,
  fetchGrid,
  removeBlock,
  type CalendarRoom,
  type Occupancy,
} from '../../lib/console'
import { addDays, addMonths, dayOfMonth, formatMonth, nights, startOfMonth, today } from '../../lib/dates'
import { useAsync } from '../../lib/useAsync'
import { ErrorNote, Loading } from '../../components/Layout'
import { useConsole } from './Console'
import { Aside, Button, Card, Field, Input, Screen, Select, report, useReload } from './ui'

/**
 * The seven-row grid, and the place rooms are taken off sale.
 *
 * Bookings, holds and blocks are drawn together because they are stored
 * together — that is the architectural bet — and because "why can I not sell
 * this room" is one question with three possible answers. A hold is drawn
 * differently from a booking on purpose: a room shaded by one is probably about
 * to come back.
 *
 * A month at a time. The grid renders a column per night, and a phone laying out
 * a year of them is a phone that stops responding.
 */
export function CalendarScreen() {
  const [month, setMonth] = useState(startOfMonth(today()))
  const [nonce, reload] = useReload()

  const from = month
  const to = addMonths(month, 1)

  const grid = useAsync(() => fetchGrid(from, to), [from, to, nonce])

  const days = useMemo(() => {
    const out: string[] = []
    for (let d = from; d < to; d = addDays(d, 1)) out.push(d)
    return out
  }, [from, to])

  return (
    <Screen
      title={formatMonth(month)}
      subtitle="Bookings, holds and blocks, all from one table."
      actions={
        <div className="flex gap-2">
          <Button onClick={() => setMonth(addMonths(month, -1))}>←</Button>
          <Button onClick={() => setMonth(startOfMonth(today()))}>This month</Button>
          <Button onClick={() => setMonth(addMonths(month, 1))}>→</Button>
        </div>
      }
    >
      {grid.loading && <Loading what="the calendar" />}
      {grid.error && <ErrorNote error={grid.error} />}

      {grid.data && (
        <>
          <Legend />

          {/*
            Scrolls inside itself rather than making the page scroll sideways.
            The room names are the one column that has to stay put, or the owner
            loses track of which row they are reading on a phone.
          */}
          <div className="overflow-x-auto rounded-lg border border-neutral-200 bg-white">
            <table className="border-separate border-spacing-0 text-xs">
              <thead>
                <tr>
                  <th className="sticky left-0 z-10 border-b border-neutral-200 bg-white px-3 py-2 text-left font-medium">
                    Room
                  </th>
                  {days.map((day) => (
                    <th
                      key={day}
                      className="w-7 border-b border-neutral-200 px-0 py-2 text-center font-normal text-neutral-500"
                    >
                      {dayOfMonth(day)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {grid.data.rooms.map((room) => (
                  <Row key={room.id} room={room} days={days} />
                ))}
              </tbody>
            </table>
          </div>

          <Blocks rooms={grid.data.rooms} onChanged={reload} />
        </>
      )}
    </Screen>
  )
}

const tones: Record<string, string> = {
  booking: 'bg-neutral-800',
  hold: 'bg-amber-400',
  block: 'bg-neutral-400',
}

function Legend() {
  return (
    <div className="flex flex-wrap gap-4 text-xs text-neutral-600">
      {[
        ['booking', 'Booked'],
        ['hold', 'Held — somebody is paying'],
        ['block', 'Blocked by you'],
      ].map(([kind, label]) => (
        <span key={kind} className="flex items-center gap-1.5">
          <span className={`h-3 w-3 rounded-sm ${tones[kind]}`} />
          {label}
        </span>
      ))}
    </div>
  )
}

function Row({ room, days }: { room: CalendarRoom; days: string[] }) {
  // The span covering each night, resolved once per row rather than searched
  // per cell. Half-open: a night is covered when it is on or after startsOn and
  // strictly before endsOn, which is what makes a same-day turnover show one
  // room changing hands rather than two rooms overlapping.
  const cover = new Map<string, Occupancy>()
  for (const span of room.occupancy) {
    for (let d = span.startsOn; d < span.endsOn; d = addDays(d, 1)) cover.set(d, span)
  }

  return (
    <tr>
      <th className="sticky left-0 z-10 border-b border-neutral-100 bg-white px-3 py-2 text-left font-normal whitespace-nowrap">
        {room.name}
      </th>
      {days.map((day) => {
        const span = cover.get(day)
        return (
          <td key={day} className="border-b border-neutral-100 p-0">
            {span ? (
              <Cell span={span} />
            ) : (
              <div className="h-8 w-7 border-l border-neutral-100" />
            )}
          </td>
        )
      })}
    </tr>
  )
}

function Cell({ span }: { span: Occupancy }) {
  const label =
    span.kind === 'block'
      ? span.reason || 'Blocked'
      : `${span.guestName || 'Booking'} · ${span.bookingCode ?? ''}`

  const box = <div className={`h-8 w-7 border-l border-white/40 ${tones[span.kind]}`} title={label} />

  return span.bookingCode ? (
    <Link to={`/admin/bookings/${span.bookingCode}`}>{box}</Link>
  ) : (
    box
  )
}

/**
 * Taking a room off sale, and putting it back.
 *
 * The dates are half-open like every other span here — "back on sale" is the
 * morning it frees up, not the last night blocked — and the form says so rather
 * than leaving the owner to discover it by blocking one night too few.
 */
function Blocks({ rooms, onChanged }: { rooms: CalendarRoom[]; onChanged: () => void }) {
  const { refresh } = useConsole()

  const [form, setForm] = useState({
    roomId: 0,
    from: today(),
    to: addDays(today(), 1),
    reason: '',
  })
  const [working, setWorking] = useState(false)
  const [error, setError] = useState<Error | null>(null)

  const blocks = rooms.flatMap((room) =>
    room.occupancy.filter((o) => o.kind === 'block').map((o) => ({ room, block: o })),
  )

  async function add() {
    setWorking(true)
    setError(null)
    try {
      await createBlock(form)
      setForm((f) => ({ ...f, reason: '' }))
      onChanged()
    } catch (err) {
      report(err, refresh, setError)
    } finally {
      setWorking(false)
    }
  }

  async function drop(id: number) {
    setError(null)
    try {
      await removeBlock(id)
      onChanged()
    } catch (err) {
      report(err, refresh, setError)
    }
  }

  const span = nights(form.from, form.to)

  return (
    <div className="flex flex-col gap-3">
      <h2 className="text-sm font-medium tracking-wide text-neutral-500 uppercase">
        Block a room
      </h2>

      <Card>
        {error && <ErrorNote error={error} />}

        <Field label="Room">
          <Select
            value={form.roomId}
            onChange={(e) => setForm((f) => ({ ...f, roomId: Number(e.target.value) }))}
          >
            <option value={0}>Choose a room…</option>
            {rooms.map((room) => (
              <option key={room.id} value={room.id}>
                {room.name}
              </option>
            ))}
          </Select>
        </Field>

        <div className="grid gap-3 sm:grid-cols-2">
          <Field label="First night blocked">
            <Input
              type="date"
              value={form.from}
              onChange={(e) => setForm((f) => ({ ...f, from: e.target.value }))}
            />
          </Field>
          <Field label="Back on sale" hint="The morning it frees up, not the last night.">
            <Input
              type="date"
              value={form.to}
              onChange={(e) => setForm((f) => ({ ...f, to: e.target.value }))}
            />
          </Field>
        </div>

        <Field label="Why?" hint="Shown only here. Maintenance, family, a private let.">
          <Input
            value={form.reason}
            onChange={(e) => setForm((f) => ({ ...f, reason: e.target.value }))}
          />
        </Field>

        <p className="text-sm text-neutral-600">
          {span > 0
            ? `${span} ${span === 1 ? 'night' : 'nights'} off sale.`
            : 'A block has to cover at least one night.'}
        </p>

        <Button kind="primary" onClick={add} disabled={working || !form.roomId || span < 1}>
          {working ? 'Blocking…' : 'Take it off sale'}
        </Button>

        <Aside>
          If a guest is halfway through paying for those nights, this is refused rather than
          overriding them — the same exclusion constraint decides for you as for them.
        </Aside>
      </Card>

      {blocks.map(({ room, block }) => (
        <Card key={block.id}>
          <div className="flex flex-wrap items-baseline justify-between gap-2">
            <span className="font-medium">{room.name}</span>
            <span className="text-sm text-neutral-600">
              {block.startsOn} → {block.endsOn}
            </span>
          </div>
          {block.reason && <p className="text-sm text-neutral-600">{block.reason}</p>}
          <Button onClick={() => drop(block.id)}>Put it back on sale</Button>
        </Card>
      ))}
    </div>
  )
}
