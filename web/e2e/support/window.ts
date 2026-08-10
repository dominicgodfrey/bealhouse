/**
 * The stretch of calendar this suite books in.
 *
 * CLAUDE.md's rule for the Go suite is that every package books in its own
 * window, because a committed booking inside another package's window silently
 * breaks it. This suite commits bookings too, and against the same development
 * database, so it takes its own: **today+600**.
 *
 * Far out on purpose. The date picker opens on the current month, so clicking
 * through the booking flow by hand lands a hold around today+30 — which is
 * already the availability tests' window and already the documented soft spot.
 * Six hundred days is past everything the Go suite uses and still inside the
 * two years of nightly rates the calendar is generated for, which is what
 * `rebuild_rate_calendar` writes and what a stay needs to be priceable at all.
 */
const WINDOW_START_DAYS = 600

/** How many days of that window this suite may claim. See dayFor. */
export const WINDOW_DAYS = 60

/**
 * A stay this test can have to itself.
 *
 * Each test takes its own three nights inside the window by naming an offset,
 * because two tests booking the same room on the same nights is a race this
 * suite would report as a bug in the exclusion constraint rather than in
 * itself. Offsets are spaced four days apart so no two stays touch — a
 * checkout and a check-in on one date do *not* collide (the range is half-open)
 * and relying on that here would be testing the schema through a UI.
 */
export function stayFor(offset: number, nights = 3) {
  if (offset * 4 + nights > WINDOW_DAYS) {
    throw new Error(`e2e: offset ${offset} falls outside the ${WINDOW_DAYS}-day window`)
  }
  const checkin = dayFrom(WINDOW_START_DAYS + offset * 4)
  return { checkin, checkout: dayFrom(WINDOW_START_DAYS + offset * 4 + nights), nights }
}

/** The first and last day of the window, for the cleanup to bracket. */
export function windowBounds() {
  return { from: dayFrom(WINDOW_START_DAYS), to: dayFrom(WINDOW_START_DAYS + WINDOW_DAYS) }
}

/**
 * A YYYY-MM-DD day, n days from today.
 *
 * Strings and never Date objects, for the reason web/src/lib/dates.ts gives and
 * the server gives with internal/civil: these are calendar days, and the moment
 * one becomes an instant it acquires a timezone that will be wrong for somebody.
 * UTC arithmetic here, since a day count from today is all this needs and the
 * suite only has to agree with itself.
 */
function dayFrom(days: number): string {
  const d = new Date()
  d.setUTCDate(d.getUTCDate() + days)
  return d.toISOString().slice(0, 10)
}

/** The query string every screen in the booking flow reads its stay from. */
export function staySearch(stay: { checkin: string; checkout: string }, guests = 2): string {
  return new URLSearchParams({
    checkin: stay.checkin,
    checkout: stay.checkout,
    guests: String(guests),
  }).toString()
}
