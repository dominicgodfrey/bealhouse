import type { Stay } from './api'
import { isValidDate, nights } from './dates'

// The stay travels in the URL from search to results to room to confirm.
//
// Keeping it there rather than in React state is what makes every one of those
// screens linkable, refreshable, and shareable — a guest sending "look at this
// room for our anniversary" sends the dates with it.

export function staySearch(stay: Stay): string {
  return new URLSearchParams({
    checkin: stay.checkin,
    checkout: stay.checkout,
    guests: String(stay.guests),
    ...(stay.withPet ? { pet: 'true' } : {}),
  }).toString()
}

/**
 * Reads a stay out of the query string, or null if it is not a whole one.
 *
 * Only sanity checks — a well-formed pair of dates in the right order. Whether
 * the stay is actually sellable is the server's answer, and asking it here as
 * well would be a second opinion to keep in sync.
 */
export function parseStay(params: URLSearchParams): Stay | null {
  const checkin = params.get('checkin') ?? ''
  const checkout = params.get('checkout') ?? ''

  if (!isValidDate(checkin) || !isValidDate(checkout) || nights(checkin, checkout) < 1) {
    return null
  }

  const guests = Number(params.get('guests') ?? '1')

  return {
    checkin,
    checkout,
    guests: Number.isFinite(guests) && guests > 0 ? Math.floor(guests) : 1,
    withPet: params.get('pet') === 'true',
  }
}
