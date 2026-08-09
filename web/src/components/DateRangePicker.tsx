import { useMemo, useState } from 'react'

import { fetchCalendar } from '../lib/api'
import { addMonths, dayOfMonth, formatMonth, monthGrid, startOfMonth, today } from '../lib/dates'
import { indexSpans } from '../lib/spans'
import { useAsync } from '../lib/useAsync'

const WEEKDAYS = ['S', 'M', 'T', 'W', 'T', 'F', 'S']

/** How far ahead the picker will let a guest browse. */
const MONTHS_AHEAD = 12

type Props = {
  checkin: string | null
  checkout: string | null
  guests: number
  withPet: boolean
  onChange: (checkin: string | null, checkout: string | null) => void
}

/**
 * The date picker, greyed from whole sellable spans rather than free nights.
 *
 * Two rules, both of which have to hold on the server as well and do:
 *
 *   - A date can be arrived on only if some ONE room can house the whole
 *     minimum stay starting there.
 *   - Once a check-in is chosen, the departures offered are the ones a room
 *     covering that check-in actually allows.
 *
 * Which is why the API hands over spans per room. Greying from the union of
 * free nights across seven rooms would offer ranges that no single room covers.
 */
export function DateRangePicker({ checkin, checkout, guests, withPet, onChange }: Props) {
  const start = today()
  const lastMonth = startOfMonth(addMonths(start, MONTHS_AHEAD - 1))

  const [month, setMonth] = useState(() => startOfMonth(checkin ?? start))

  const calendar = useAsync(
    () =>
      fetchCalendar({
        from: start,
        to: addMonths(start, MONTHS_AHEAD),
        guests,
        withPet,
      }),
    [guests, withPet],
  )

  const index = useMemo(() => indexSpans(calendar.data), [calendar.data])

  // Choosing a check-in clears the check-out, because the departures a room
  // allows depend on where the stay begins. Clicking a second date completes
  // the range only if it is one of them.
  const choosing: 'checkin' | 'checkout' = checkin && !checkout ? 'checkout' : 'checkin'
  const departures = useMemo(
    () => (choosing === 'checkout' && checkin ? index.departures(checkin) : new Set<string>()),
    [choosing, checkin, index],
  )

  function select(date: string) {
    if (choosing === 'checkout') {
      onChange(checkin, date)
    } else {
      onChange(date, null)
    }
  }

  function selectable(date: string): boolean {
    return choosing === 'checkout' ? departures.has(date) : index.arrivals.has(date)
  }

  const months = [month, addMonths(month, 1)]

  return (
    // Same tint as the card it opens out of; a white panel hanging off a
    // coloured one read as a different control.
    <div className="rounded-lg border border-neutral-300 bg-sienna/90 p-4">
      {/* size-11 is 44px: the smallest thing a thumb reliably hits. These were
          28px, and a miss lands on a date. */}
      <div className="mb-3 flex items-center justify-between gap-2">
        <button
          type="button"
          className="flex size-11 shrink-0 items-center justify-center rounded text-base text-neutral-600 hover:bg-black/5 disabled:invisible"
          disabled={month <= startOfMonth(start)}
          onClick={() => setMonth(addMonths(month, -1))}
          aria-label="Previous month"
        >
          ←
        </button>
        <p className="text-sm font-medium">
          {choosing === 'checkout' ? 'Choose your check-out' : 'Choose your check-in'}
        </p>
        <button
          type="button"
          className="flex size-11 shrink-0 items-center justify-center rounded text-base text-neutral-600 hover:bg-black/5 disabled:invisible"
          disabled={addMonths(month, 1) >= lastMonth}
          onClick={() => setMonth(addMonths(month, 1))}
          aria-label="Next month"
        >
          →
        </button>
      </div>

      {calendar.loading && <p className="py-8 text-center text-sm text-neutral-500">Loading…</p>}

      {calendar.error && (
        <p className="py-8 text-center text-sm text-red-700">
          The calendar could not be loaded: {calendar.error.message}
        </p>
      )}

      {calendar.data && (
        <>
          <div className="grid gap-4 sm:grid-cols-2">
            {months.map((m) => (
              <Month
                key={m}
                month={m}
                checkin={checkin}
                checkout={checkout}
                selectable={selectable}
                onSelect={select}
              />
            ))}
          </div>

          {index.empty && (
            <p className="mt-3 text-center text-sm text-neutral-600">
              Nothing is available for {guests} {guests === 1 ? 'guest' : 'guests'}
              {withPet ? ' with a pet' : ''} in the next year.
            </p>
          )}

          {/* The picker stops offering departures past the maximum stay, which
              without a word of explanation just looks like the calendar has
              run out. Longer stays are genuinely available — they are arranged
              with the owner rather than sold here (decision #27).

              Deliberately still not a link. There is a contact form on the home
              page now, but sending somebody out of a half-chosen date range to
              find it loses the dates they had picked — and the sentence is a
              note about a limit rather than a call to action. */}
          {index.maxStayNights > 0 && (
            <p className="mt-3 text-sm text-neutral-600">
              Stays of up to {index.maxStayNights} nights can be booked here. For anything
              longer, please contact the inn and we will do our best to accommodate your needs.
            </p>
          )}

          {choosing === 'checkout' && (
            <button
              type="button"
              className="mt-3 text-sm text-neutral-600 underline underline-offset-4"
              onClick={() => onChange(null, null)}
            >
              Start over
            </button>
          )}
        </>
      )}
    </div>
  )
}

type MonthProps = {
  month: string
  checkin: string | null
  checkout: string | null
  selectable: (date: string) => boolean
  onSelect: (date: string) => void
}

function Month({ month, checkin, checkout, selectable, onSelect }: MonthProps) {
  return (
    // Each month is boxed, which is what makes the grid readable while the
    // panel stays translucent: the border gives the dates an edge to sit
    // against, and the second layer of tint over the first puts the numbers on
    // something nearer opaque without the whole sheet going solid.
    <div className="rounded-lg border border-neutral-300 bg-sienna/60 p-3">
      <p className="mb-2 text-center text-sm font-medium">{formatMonth(month)}</p>

      <div className="grid grid-cols-7 gap-px">
        {WEEKDAYS.map((day, i) => (
          <div key={i} className="pb-1 text-center text-xs text-neutral-400">
            {day}
          </div>
        ))}

        {monthGrid(month).map((date, i) =>
          date === null ? (
            <div key={i} />
          ) : (
            <Day
              key={date}
              date={date}
              checkin={checkin}
              checkout={checkout}
              selectable={selectable(date)}
              onSelect={onSelect}
            />
          ),
        )}
      </div>
    </div>
  )
}

type DayProps = {
  date: string
  checkin: string | null
  checkout: string | null
  selectable: boolean
  onSelect: (date: string) => void
}

function Day({ date, checkin, checkout, selectable, onSelect }: DayProps) {
  const isEndpoint = date === checkin || date === checkout
  // The checkout date is not a night, so it caps the shaded range rather than
  // being inside it — the same half-open convention the database stores.
  const inRange = Boolean(checkin && checkout && date > checkin && date < checkout)

  let tone = 'text-neutral-300'
  if (isEndpoint) {
    tone = 'bg-neutral-900 font-medium text-white'
  } else if (inRange) {
    tone = 'bg-black/5 text-neutral-900'
  } else if (selectable) {
    tone = 'text-neutral-900 hover:bg-black/5'
  }

  return (
    <button
      type="button"
      disabled={!selectable && !isEndpoint}
      onClick={() => onSelect(date)}
      aria-label={date}
      aria-disabled={!selectable}
      className={`aspect-square rounded text-sm disabled:cursor-not-allowed ${tone}`}
    >
      {dayOfMonth(date)}
    </button>
  )
}
