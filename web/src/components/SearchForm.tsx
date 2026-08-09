import { useState } from 'react'
import { useNavigate } from 'react-router'

import type { Stay } from '../lib/api'
import { formatLong, nights } from '../lib/dates'
import { DateRangePicker } from './DateRangePicker'

/** The largest party any room sleeps. Beyond this nothing can be offered. */
const MAX_GUESTS = 4

/**
 * Where the calendar goes when it is not allowed to push the page around.
 *
 * Two arrangements, and the second one is not a phone concession. Given room
 * under the field (`roomy`, in index.css) it hangs off it like any dropdown.
 * Otherwise it is pinned above the bottom of the viewport and scrolls inside
 * itself — because on a 568px-tall screen the field's own bottom edge is 300px
 * down, two months of dates are not going to fit in what is left, and the page
 * underneath is the home page, which cannot scroll to reveal the rest.
 *
 * `dvh` rather than `vh`: on a phone `vh` counts the strip the address bar is
 * sitting on.
 *
 * **Nothing between this and the viewport may have a `backdrop-filter` on it.**
 * A blurred ancestor becomes the containing block for `fixed` descendants, and
 * this panel would then be pinned to the bottom of the search card rather than
 * to the bottom of the screen. That is why the card it sits in is plain
 * translucent white — see Home.tsx.
 */
const floating =
  'fixed inset-x-3 bottom-3 z-30 max-h-[85dvh] overflow-y-auto overscroll-contain rounded-lg shadow-xl ' +
  'roomy:absolute roomy:inset-x-0 roomy:bottom-auto roomy:top-full roomy:mt-2 roomy:max-h-[70vh]'

type Props = {
  initial?: Partial<Stay>
  /**
   * Float the calendar over the page instead of pushing what is under it down.
   *
   * The home page asks for this because it does not scroll: an inline picker
   * there would grow the column past the viewport and take the footer with it.
   * Everywhere else the page is a document and the picker is simply part of it.
   */
  overlay?: boolean
}

/**
 * Dates, party size, and whether a pet is coming.
 *
 * **The calendar starts closed, everywhere.** It used to be pinned open on the
 * home page, where it was the largest thing on the screen before a visitor had
 * asked it anything — twelve months of availability answering a question nobody
 * had put yet. Now the field says "Add your dates" and opens on a tap, and so
 * does Search: a search with no dates has nothing to run, so the button that
 * cannot do its job yet opens the thing that is missing rather than sitting
 * greyed out with no explanation.
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

  const complete = Boolean(checkin && checkout)

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
      {/*
        Two columns on a phone with the dates spanning both, rather than three
        stacked rows. That is one field height saved on the screen where it is
        scarcest — the home page has to fit a header, this, two buttons and a
        footer into a viewport nobody can scroll.
      */}
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
          I am bringing a pet <span className="text-neutral-500">($50 per stay, one room)</span>
        </span>
      </label>

      {open && (
        <div className={overlay ? floating : undefined}>
          <DateRangePicker
            checkin={checkin}
            checkout={checkout}
            guests={guests}
            withPet={withPet}
            onChange={(from, to) => {
              setCheckin(from)
              setCheckout(to)
              // A completed range closes it. The dates are now in the field
              // above, and on the home page an open calendar is covering the
              // photograph it was opened over.
              if (from && to) setOpen(false)
            }}
          />
        </div>
      )}
    </div>
  )
}
