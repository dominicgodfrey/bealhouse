// The shapes the Go API returns.
//
// Hand-written for now. Architecture decision #3 has these generated from an
// OpenAPI spec so the two sides cannot drift; until the annotations exist,
// these live here and are the one place to fix when they do.

export type Quote = {
  nights: number
  roomSubtotalCents: number
  petFeeCents: number
  taxableCents: number
  taxCents: number
  totalCents: number
  depositCents: number
  balanceCents: number
}

export type Bed = { type: string; count: number; location?: string }
export type Photo = { url: string; alt: string }

export type Room = {
  id: number
  slug: string
  name: string
  description: string
  view?: string
  maxOccupancy: number
  amenities: string[]
  beds: Bed[]
  photos: Photo[]
  placeholderPhotoUrl: string
  isPetFriendly: boolean
  nightlyCents: number[]
  quote: Quote
}

export type SearchResult = {
  checkin: string
  checkout: string
  nights: number
  guests: number
  withPet: boolean
  rooms: Room[]
  accessibilityNotice: string
}

export type RoomDetail = Room & {
  isAccessible: boolean
  accessibilityFeatures: string[]
  accessibilityNotice: string
  petFeeCentsPerStay?: number
  hasDates: boolean
  available: boolean
  checkin?: string
  checkout?: string
  nights?: number
  guests?: number
  withPet?: boolean
}

/** An unbroken run of sellable nights in one room. */
export type Span = { start: string; minStays: number[] }

export type Calendar = {
  from: string
  to: string
  guests: number
  withPet: boolean
  rooms: { slug: string; spans: Span[] }[]
  /** The longest stay on sale. Longer ones are arranged with the inn directly. */
  maxStayNights: number
}

export type BookedRoom = {
  slug: string
  name: string
  view?: string
  checkin: string
  checkout: string
  nightlyCents: number[]
}

export type Booking = {
  code: string
  status: 'pending' | 'confirmed' | 'cancelled' | 'expired'
  checkin: string
  checkout: string
  nights: number
  guests: number
  withPet: boolean
  rooms: BookedRoom[]
  quote: Quote
  chargeNowCents: number
  balanceChargeOn?: string
  holdExpiresAt?: string
  guest?: { name: string; email: string; phone?: string }
}

/**
 * ApiError carries the message the server wrote.
 *
 * The API answers a failed booking with a sentence a guest can act on — "that
 * room has just been taken" — so throwing away the body and showing "HTTP 409"
 * would be discarding the only useful part.
 */
export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })

  if (!response.ok) {
    const body = await response.json().catch(() => null)
    throw new ApiError(response.status, body?.error ?? `Request failed (${response.status})`)
  }
  return response.json() as Promise<T>
}

export type Stay = {
  checkin: string
  checkout: string
  guests: number
  withPet: boolean
}

function stayQuery(stay: Stay): string {
  return new URLSearchParams({
    checkin: stay.checkin,
    checkout: stay.checkout,
    guests: String(stay.guests),
    ...(stay.withPet ? { pet: 'true' } : {}),
  }).toString()
}

export function searchRooms(stay: Stay): Promise<SearchResult> {
  return request<SearchResult>(`/api/availability?${stayQuery(stay)}`)
}

export function fetchRoom(slug: string, stay?: Stay): Promise<RoomDetail> {
  const query = stay ? `?${stayQuery(stay)}` : ''
  return request<RoomDetail>(`/api/rooms/${encodeURIComponent(slug)}${query}`)
}

export function fetchCalendar(opts: {
  from: string
  to: string
  guests: number
  withPet: boolean
}): Promise<Calendar> {
  const query = new URLSearchParams({
    from: opts.from,
    to: opts.to,
    guests: String(opts.guests),
    ...(opts.withPet ? { pet: 'true' } : {}),
  })
  return request<Calendar>(`/api/calendar?${query}`)
}

export function fetchBooking(code: string): Promise<Booking> {
  return request<Booking>(`/api/bookings/${encodeURIComponent(code)}`)
}

export function createBooking(body: {
  roomSlug: string
  checkin: string
  checkout: string
  guests: number
  withPet: boolean
  expectedTotalCents: number
  guest: { name: string; email: string; phone: string }
}): Promise<Booking> {
  return request<Booking>('/api/bookings', {
    method: 'POST',
    body: JSON.stringify(body),
  })
}
