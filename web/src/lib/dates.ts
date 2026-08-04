// Civil dates, the same as everywhere else in this system: 'YYYY-MM-DD'
// strings, never a Date carrying a local time.
//
// A Date is an instant, and the moment one is used for a check-in the guest's
// browser timezone starts deciding what day it is. Someone in Los Angeles
// opening the picker at 9pm would be offered yesterday. Strings and UTC
// arithmetic keep a night at the inn a night at the inn — matching
// internal/civil on the server.

const DAY_MS = 86_400_000

/** The current date at the inn, regardless of where the browser is. */
export function today(): string {
  // en-CA formats as YYYY-MM-DD, which is the shape we want anyway.
  return new Intl.DateTimeFormat('en-CA', { timeZone: 'America/New_York' }).format(new Date())
}

function utc(iso: string): number {
  const [y, m, d] = iso.split('-').map(Number)
  return Date.UTC(y, m - 1, d)
}

function iso(ms: number): string {
  return new Date(ms).toISOString().slice(0, 10)
}

export function addDays(date: string, days: number): string {
  return iso(utc(date) + days * DAY_MS)
}

export function addMonths(date: string, months: number): string {
  const [y, m, d] = date.split('-').map(Number)
  return iso(Date.UTC(y, m - 1 + months, d))
}

/** Nights in a half-open [checkin, checkout) stay. */
export function nights(checkin: string, checkout: string): number {
  return Math.round((utc(checkout) - utc(checkin)) / DAY_MS)
}

export function isValidDate(date: string): boolean {
  return /^\d{4}-\d{2}-\d{2}$/.test(date) && !Number.isNaN(utc(date))
}

/** The first of the month a date falls in. */
export function startOfMonth(date: string): string {
  return date.slice(0, 7) + '-01'
}

/** Every date in the month, plus the leading blanks that align it to Sunday. */
export function monthGrid(month: string): (string | null)[] {
  const first = startOfMonth(month)
  const start = new Date(utc(first))
  const cells: (string | null)[] = Array<null>(start.getUTCDay()).fill(null)

  for (let d = first; d.slice(0, 7) === first.slice(0, 7); d = addDays(d, 1)) {
    cells.push(d)
  }
  return cells
}

const monthFormat = new Intl.DateTimeFormat('en-US', {
  timeZone: 'UTC',
  month: 'long',
  year: 'numeric',
})

const longFormat = new Intl.DateTimeFormat('en-US', {
  timeZone: 'UTC',
  weekday: 'short',
  month: 'short',
  day: 'numeric',
})

const shortFormat = new Intl.DateTimeFormat('en-US', {
  timeZone: 'UTC',
  month: 'short',
  day: 'numeric',
})

/** 'October 2026' */
export function formatMonth(date: string): string {
  return monthFormat.format(new Date(utc(date)))
}

/** 'Thu, Oct 1' */
export function formatLong(date: string): string {
  return longFormat.format(new Date(utc(date)))
}

/** 'Oct 1' */
export function formatShort(date: string): string {
  return shortFormat.format(new Date(utc(date)))
}

export function dayOfMonth(date: string): number {
  return Number(date.slice(8, 10))
}
