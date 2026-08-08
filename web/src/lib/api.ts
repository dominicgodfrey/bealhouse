// The shapes the Go API returns.
//
// Hand-written for now. Architecture decision #3 has these generated from an
// OpenAPI spec so the two sides cannot drift; until the annotations exist,
// these live here and are the one place to fix when they do.

import type { PhotoSources } from '../components/Photo'

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
export type Photo = { url: string; alt: string } & PhotoSources

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

export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...init?.headers },
  })

  if (!response.ok) {
    const body = await response.json().catch(() => null)
    throw new ApiError(response.status, body?.error ?? `Request failed (${response.status})`)
  }

  // Signing out and revoking a passkey answer 204, and parsing an empty body as
  // JSON throws — which would turn a request that did exactly what was asked
  // into an error on screen.
  if (response.status === 204) return undefined as T

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

export type PaymentIntent = {
  /**
   * Authorises the browser to complete this one payment, and nothing else. It
   * is a credential: it belongs in Stripe's card form and in no log, URL or
   * error message.
   */
  clientSecret: string

  /** Stripe's public account identifier. Safe to hold; useless on its own. */
  publishableKey: string

  /** What the card will be charged, decided by the server from the booking. */
  amountCents: number

  /**
   * The server is running against the fake processor, so there is no card form
   * to mount and the stand-in button applies. Never true on a deploy that can
   * take real money.
   */
  devPayment: boolean
}

/**
 * Opens a payment for a booking.
 *
 * Sends no body on purpose. The amount is the server's to decide from the
 * booking it already has; anything this file could add would be a guest naming
 * their own price.
 */
export function openPayment(code: string): Promise<PaymentIntent> {
  return request<PaymentIntent>(`/api/bookings/${encodeURIComponent(code)}/payment-intent`, {
    method: 'POST',
  })
}

/**
 * Stands in for the card form against the fake processor.
 *
 * The route exists only when the server has no Stripe configuration at all and
 * is running in dev, so this 404s everywhere it should.
 */
export function devPay(code: string): Promise<{ status: string }> {
  return request<{ status: string }>(`/api/dev/pay/${encodeURIComponent(code)}`, {
    method: 'POST',
  })
}

/** What cancelling a stay today would return (decisions #9 and #26). */
export type RefundPreview = {
  paidCents: number
  retainedCents: number
  refundCents: number
  /** Inside seven days, where the deposit is forfeit. */
  late: boolean
}

export type ManagedBooking = {
  booking: Booking
  refund: RefundPreview
  cancellable: boolean
  /** Why not, in words for the guest. Empty when it is. */
  reason?: string
}

/**
 * The guest's own view of their booking, behind the signed link.
 *
 * The token comes from the URL and goes straight back to the server. It is a
 * capability: it belongs in this request and in no log or analytics call.
 */
export function fetchManagedBooking(code: string, token: string): Promise<ManagedBooking> {
  const query = new URLSearchParams({ t: token })
  return request<ManagedBooking>(`/api/bookings/${encodeURIComponent(code)}/manage?${query}`)
}

/**
 * Where the confirmation PDF lives.
 *
 * A URL rather than a fetch: it is handed to an <a href> so the browser
 * downloads it the way it downloads anything, filename and all.
 */
export function confirmationPdfUrl(code: string, token: string): string {
  const query = new URLSearchParams({ t: token })
  return `/api/bookings/${encodeURIComponent(code)}/confirmation.pdf?${query}`
}

export function cancelBooking(code: string, token: string): Promise<RefundPreview> {
  const query = new URLSearchParams({ t: token })
  return request<RefundPreview>(`/api/bookings/${encodeURIComponent(code)}/cancel?${query}`, {
    method: 'POST',
  })
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
