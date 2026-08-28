import { useLayoutEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router'

import type { Stay } from '../lib/api'
import { formatLong, nights } from '../lib/dates'
import { DateRangePicker } from './DateRangePicker'

/** The largest party any room sleeps. Beyond this nothing can be offered. */
const MAX_GUESTS = 4

/**
 * Where the calendar goes when it may not push the page around.
 *
 * It hangs off the bottom of the field like any dropdown, at every size. It
 * used to become a sheet pinned to the bottom of the viewport below a `roomy`
 * breakpoint, which put the panel in a different place on a phone than on a
 * monitor for no reason a guest could see — the field it belongs to was up the
 * screen and the panel was against the bottom edge.
 *
 * What the breakpoint was actually protecting against is real, though: the home
 * page cannot scroll, so a panel taller than the space under the field has a
 * bottom nobody can reach. `fit` below is that guard, done by measurement
 * instead — see it for why this is not a `max-h-[…]` class.
 *
 * **Nothing between this and the viewport may carry a `backdrop-filter`**: a
 * blurred ancestor becomes the containing block for fixed and absolute
 * descendants alike, and this would pin itself to the card it is escaping. See
 * Home.tsx.
 */
const floating =
  'absolute inset-x-0 top-full z-30 mt-2 overflow-y-auto overscroll-contain rounded-lg shadow-xl'

/** Breathing room between the bottom of the open calendar and the viewport. */
const gutter = 12

type Props = {
  initial?: Partial<Stay>
  /**
   * Float the calendar over the page instead of pushing what is under it down.
   * The home page needs it because it does not scroll; everywhere else the page
   * is a document and the picker is part of it.
   */
  overlay?: boolean
}

/**
 * Dates, party size, and whether a pet is coming.
 *
 * **The calendar starts closed, everywhere.** It used to be pinned open on the
 * home page, answering a question nobody had asked yet. Search opens it too: a
 * search with no dates has nothing to run, so the button opens the thing that
 * is missing rather than sitting greyed out with no explanation.
 *
 * The pet box does double duty, as decision #23 describes: it narrows results
 * to the one room that takes pets AND adds the $50. Leaving it unchecked does
 * not hide that room, because it is an ordinary room that happens to allow
 * them.
 */
export function SearchForm({ initial, overlay = false }: Props) {
  const navigate = useNavigate()

  const [checkin, setCheckin] = useState<string | null>(initial?.checkin ?? null)
  const [checkout, setCheckout] = useState<string | null>(initial?.checkout ?? null)
  const [guests, setGuests] = useState(initial?.guests ?? 2)
  const [withPet, setWithPet] = useState(initial?.withPet ?? false)
  const [open, setOpen] = useState(false)
  const panel = useRef<HTMLDivElement>(null)

  const complete = Boolean(checkin && checkout)

  /**
   * Give the open calendar exactly the room there is under the field.
   *
   * Only when it is overlaid — everywhere else the page is a document that
   * scrolls and the picker is part of it. On the home page, which does not
   * scroll, a panel that runs past the bottom of the viewport has a "Start
   * over" and a last week of dates that no gesture can bring into view.
   *
   * Measured rather than a `max-h-[calc(100dvh-…)]` class because the constant
   * such a class needs is how far down the field ends, and that is not one
   * number: it moves with the header, with the fields going from stacked to
   * side by side, and with the dates line wrapping once a range is chosen. Each
   * of those was a value somebody would have had to remember to re-measure.
   *
   * The panel's own top does not move when its height changes — it is pinned to
   * `top-full` — so reading the rect after setting the height is stable rather
   * than a feedback loop.
   */
  useLayoutEffect(() => {
    const el = panel.current
    if (!overlay || !el) return

    const fit = () => {
      const room = window.innerHeight - el.getBoundingClientRect().top - gutter
      el.style.maxHeight = `${Math.max(room, 0)}px`
    }

    fit()
    // Rotation, and the mobile address bar collapsing, both arrive as a resize.
    window.addEventListener('resize', fit)
    return () => window.removeEventListener('resize', fit)
  }, [open, overlay, complete])

  function submit() {
    // Nothing to search for yet: show the calendar rather than refusing. This
    // is the "closed until Search is clicked" half of the picker's contract.
    if (!checkin || !checkout) {
      setOpen(true)
      return
    }
    const query = new URLSearchParams({
      checkin,
      checkout,
      guests: String(guests),
      ...(withPet ? { pet: 'true' } : {}),
    })
    navigate(`/search?${query}`)
  }

  return (
    // relative, so an overlaid calendar is positioned against the form rather
    // than against whatever ancestor happens to be positioned.
    <div className="relative flex flex-col gap-3">
      {/* Two columns on a phone with the dates spanning both, rather than three
          stacked rows: one field height saved on the screen with the least. */}
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-[1fr_auto_auto]">
        <button
          type="button"
          onClick={() => setOpen((was) => !was)}
          aria-expanded={open}
          className="col-span-2 rounded-lg border border-neutral-300 bg-white px-4 py-3 text-left hover:border-neutral-400 sm:col-span-1"
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

        <label className="rounded-lg border border-neutral-300 bg-white px-4 py-3">
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

        {/* Never disabled — with no dates it opens the calendar. */}
        <button
          type="button"
          onClick={submit}
          className="rounded-lg bg-neutral-900 px-6 py-3 text-sm font-medium text-white hover:bg-neutral-700"
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
      <label className="-my-1 flex items-center gap-2 py-1 text-sm text-neutral-700">
        <input
          type="checkbox"
          checked={withPet}
          onChange={(e) => setWithPet(e.target.checked)}
          className="size-4 shrink-0"
        />
        <span>
          I am bringing a pet <span className="text-neutral-500">($50 pet charge per stay)</span>
        </span>
      </label>

      {open && (
        <div ref={panel} className={overlay ? floating : undefined}>
          <DateRangePicker
            checkin={checkin}
            checkout={checkout}
            guests={guests}
            withPet={withPet}
            onChange={(from, to) => {
              setCheckin(from)
              setCheckout(to)
              // A completed range closes it: the dates are in the field above.
              if (from && to) setOpen(false)
            }}
          />
        </div>
      )}
    </div>
  )
}
