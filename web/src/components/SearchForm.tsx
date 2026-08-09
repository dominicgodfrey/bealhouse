import { useState } from 'react'
import { useNavigate } from 'react-router'

import type { Stay } from '../lib/api'
import { formatLong, nights } from '../lib/dates'
import { DateRangePicker } from './DateRangePicker'

/** The largest party any room sleeps. Beyond this nothing can be offered. */
const MAX_GUESTS = 4

type Props = {
  initial?: Partial<Stay>
  /** Rendered inline on the home page; collapsed to a summary elsewhere. */
  alwaysOpen?: boolean
}

/**
 * Dates, party size, and whether a pet is coming.
 *
 * The pet box does double duty, as decision #23 describes: it narrows results
 * to the one room that takes pets AND adds the $50. Leaving it unchecked does
 * not hide that room, because it is an ordinary room that happens to allow
 * them.
 */
export function SearchForm({ initial, alwaysOpen = false }: Props) {
  const navigate = useNavigate()

  const [checkin, setCheckin] = useState<string | null>(initial?.checkin ?? null)
  const [checkout, setCheckout] = useState<string | null>(initial?.checkout ?? null)
  const [guests, setGuests] = useState(initial?.guests ?? 2)
  const [withPet, setWithPet] = useState(initial?.withPet ?? false)
  const [open, setOpen] = useState(alwaysOpen)

  const complete = Boolean(checkin && checkout)

  function submit() {
    if (!checkin || !checkout) return
    const query = new URLSearchParams({
      checkin,
      checkout,
      guests: String(guests),
      ...(withPet ? { pet: 'true' } : {}),
    })
    navigate(`/search?${query}`)
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="grid gap-3 sm:grid-cols-[1fr_auto_auto]">
        <button
          type="button"
          onClick={() => setOpen((was) => !was)}
          className="rounded-lg border border-neutral-300 px-4 py-3 text-left hover:border-neutral-400"
        >
          <span className="block text-xs uppercase tracking-wide text-neutral-500">Dates</span>
          <span className="block text-sm">
            {complete ? (
              <>
                {formatLong(checkin!)} → {formatLong(checkout!)}{' '}
                <span className="text-neutral-500">
                  ({nights(checkin!, checkout!)} nights)
                </span>
              </>
            ) : (
              <span className="text-neutral-500">Add your dates</span>
            )}
          </span>
        </button>

        <label className="rounded-lg border border-neutral-300 px-4 py-3">
          <span className="block text-xs uppercase tracking-wide text-neutral-500">Guests</span>
          {/*
            text-base rather than text-sm: iOS zooms the whole page in when a
            focused field's text is under 16px, and it does not zoom back out.
            The whole label is the tap target, so the control itself being short
            is fine — but the text has to be readable.
          */}
          <select
            className="w-full bg-transparent text-base outline-none"
            value={guests}
            onChange={(e) => setGuests(Number(e.target.value))}
          >
            {Array.from({ length: MAX_GUESTS }, (_, i) => i + 1).map((n) => (
              <option key={n} value={n}>
                {n} {n === 1 ? 'guest' : 'guests'}
              </option>
            ))}
          </select>
        </label>

        <button
          type="button"
          onClick={submit}
          disabled={!complete}
          className="rounded-lg bg-neutral-900 px-6 py-3 text-sm font-medium text-white hover:bg-neutral-700 disabled:cursor-not-allowed disabled:bg-neutral-300"
        >
          Search
        </button>
      </div>

      {/*
        The whole label is the tap target and -my-2 py-2 makes it 44px tall
        without moving anything: the checkbox itself is 16px, which is a
        difficult thing to hit with a thumb and an easy one to miss into the
        calendar below.
      */}
      <label className="-my-2 flex items-center gap-2 py-2 text-sm text-neutral-700">
        <input
          type="checkbox"
          checked={withPet}
          onChange={(e) => setWithPet(e.target.checked)}
          className="size-4 shrink-0"
        />
        <span>
          I am bringing a pet <span className="text-neutral-500">($50 per stay, one room)</span>
        </span>
      </label>

      {open && (
        <DateRangePicker
          checkin={checkin}
          checkout={checkout}
          guests={guests}
          withPet={withPet}
          onChange={(from, to) => {
            setCheckin(from)
            setCheckout(to)
            if (from && to && !alwaysOpen) setOpen(false)
          }}
        />
      )}
    </div>
  )
}
