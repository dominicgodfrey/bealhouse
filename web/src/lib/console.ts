/**
 * The console's operational API — everything behind the session that is not
 * about phones and passkeys, which live in admin.ts.
 *
 * Split from admin.ts because the two have different lifetimes: admin.ts is the
 * gate, and never changes once passkeys work, while this file grows with every
 * screen. Split from api.ts for the reason that file says — a 401 here is not an
 * error to display but a sign-in to ask for.
 *
 * Hand-written, and the same file the OpenAPI generator in decision #3 replaces.
 */

import { ApiError, request } from './api'

// ---------------------------------------------------------------------------
// Bookings
// ---------------------------------------------------------------------------

export type BookingStatus = 'pending' | 'confirmed' | 'cancelled' | 'expired'

/**
 * One reservation as every list shows it.
 *
 * Four money fields rather than a "paid" flag, because "is this paid" has four
 * answers an owner acts on differently — and the two that matter most look
 * identical under a boolean: a deposit-paid stay waiting for its T-7 charge, and
 * one whose T-7 charge bounced.
 */
export type Stay = {
  code: string
  status: BookingStatus
  checkin: string
  checkout: string
  nights: number
  guests: number
  withPet: boolean
  rooms: string

  guestId?: number
  guestName: string
  guestEmail: string
  guestPhone?: string

  totalCents: number
  paidCents: number
  outstandingCents: number

  /** Absent when no scheduled charge exists: short notice, or a phone booking. */
  balanceChargeOn?: string

  /** The flag that has to be unmissable: the card was refused at T-7. */
  chargeFailed?: string
  warned?: string
  createdAt?: string
}

export type Board = {
  date: string
  arrivals: Stay[]
  departures: Stay[]
  inHouse: Stay[]
  checkinTime: string
  checkoutTime: string
  /** Refused balance charges anywhere in the book, not only today. */
  flagged: number
  newInquiries: number
}

export type Payment = {
  kind: string
  status: string
  amountCents: number
  stripeId: string
  at: string
}

export type RefundQuote = {
  paidCents: number
  retainedCents: number
  refundCents: number
  late: boolean
}

export type BookedRoom = {
  slug: string
  name: string
  view?: string
  checkin: string
  checkout: string
  nightlyCents: number[]
}

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

export type BookingDetail = {
  stay: Stay
  rooms: BookedRoom[]
  quote: Quote
  payments: Payment[]
  /** What cancelling would return today. Absent when cancelling is refused. */
  refund?: RefundQuote
  cancellable: boolean
  reason?: string
  holdExpiresAt?: string
}

export type BookingFilter = {
  from?: string
  to?: string
  status?: string
  room?: number
  q?: string
  flagged?: boolean
}

function query(params: Record<string, string | number | boolean | undefined>): string {
  const out = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== '' && value !== false) out.set(key, String(value))
  }
  const s = out.toString()
  return s ? `?${s}` : ''
}

export function fetchBoard(on?: string): Promise<Board> {
  return request<Board>(`/api/admin/today${query({ on })}`)
}

export function fetchStays(filter: BookingFilter): Promise<Stay[]> {
  return request<Stay[]>(`/api/admin/bookings${query(filter)}`)
}

export function fetchStay(code: string): Promise<BookingDetail> {
  return request<BookingDetail>(`/api/admin/bookings/${encodeURIComponent(code)}`)
}

/**
 * Cancels a stay and starts its refund. The browser sends no amount: what comes
 * back is what the same arithmetic the guest's own page uses decided.
 */
export function cancelStay(code: string): Promise<RefundQuote> {
  return request<RefundQuote>(`/api/admin/bookings/${encodeURIComponent(code)}/cancel`, {
    method: 'POST',
  })
}

/** A refund outside the policy: the no-show, the cut-short visit, the gesture. */
export function refundStay(code: string, amountCents: number): Promise<void> {
  return request<void>(`/api/admin/bookings/${encodeURIComponent(code)}/refund`, {
    method: 'POST',
    body: JSON.stringify({ amountCents }),
  })
}

/**
 * How a booking taken on the telephone gets paid for.
 *
 * The stay is identical in all three cases — confirmed, room held — and only
 * what happens afterwards differs.
 */
export type Settlement = 'offline' | 'link' | 'card'

export type ManualBooking = {
  roomSlug: string
  checkin: string
  checkout: string
  guests: number
  withPet: boolean
  name: string
  email: string
  phone: string
  payment: Settlement
}

/** Emails the guest what is outstanding and somewhere to pay it. */
export function requestPayment(code: string): Promise<void> {
  return request<void>(`/api/admin/bookings/${encodeURIComponent(code)}/request-payment`, {
    method: 'POST',
  })
}

/**
 * A payment for somebody at the inn to key a card into.
 *
 * The card itself never comes back through here and never goes out through
 * here: this is a client secret, and Stripe's own form in the browser is what
 * sees the number. Declared to Stripe as a telephone order, so the bank does
 * not send a 3-D Secure challenge to a guest who is on the phone and has no way
 * to answer one.
 */
export type CardPayment = {
  code: string
  clientSecret: string
  publishableKey: string
  amountCents: number
  /** The processor is the development stand-in, so no money can move. */
  devPayment: boolean
}

export function collectPayment(code: string): Promise<CardPayment> {
  return request<CardPayment>(`/api/admin/bookings/${encodeURIComponent(code)}/collect`, {
    method: 'POST',
  })
}

/**
 * Uploads one photograph and returns the path it now lives at.
 *
 * Multipart rather than JSON, so a phone photograph does not grow by a third on
 * the way. `request` is bypassed deliberately: it sets a JSON content type, and
 * fetch has to be left to write its own multipart boundary.
 *
 * The file is stored immediately; attaching it to a room is the separate save
 * that follows. An upload the owner then changes their mind about leaves a file
 * nobody references rather than a half-edited room.
 */
export async function uploadPhoto(file: File): Promise<{ path: string }> {
  const body = new FormData()
  body.append('photo', file)

  const response = await fetch('/api/admin/photos', { method: 'POST', body })
  if (!response.ok) {
    const problem = await response.json().catch(() => null)
    throw new ApiError(response.status, problem?.error ?? `Upload failed (${response.status})`)
  }
  return response.json() as Promise<{ path: string }>
}

export function createStay(booking: ManualBooking): Promise<{ code: string }> {
  return request<{ code: string }>('/api/admin/bookings', {
    method: 'POST',
    body: JSON.stringify(booking),
  })
}

// ---------------------------------------------------------------------------
// The calendar and blocking
// ---------------------------------------------------------------------------

export type OccupancyKind = 'booking' | 'hold' | 'block'

export type Occupancy = {
  id: number
  roomId: number
  /** Half-open: endsOn is the checkout, not a night. */
  startsOn: string
  endsOn: string
  kind: OccupancyKind
  source: string
  reason?: string
  expiresAt?: string
  bookingCode?: string
  bookingStatus?: string
  guestName?: string
}

export type CalendarRoom = {
  id: number
  slug: string
  name: string
  occupancy: Occupancy[]
}

export type AdminCalendar = {
  from: string
  to: string
  rooms: CalendarRoom[]
}

export function fetchGrid(from: string, to: string): Promise<AdminCalendar> {
  return request<AdminCalendar>(`/api/admin/calendar${query({ from, to })}`)
}

export function createBlock(block: {
  roomId: number
  from: string
  to: string
  reason: string
}): Promise<{ id: number }> {
  return request<{ id: number }>('/api/admin/blocks', {
    method: 'POST',
    body: JSON.stringify(block),
  })
}

export function removeBlock(id: number): Promise<void> {
  return request<void>(`/api/admin/blocks/${id}`, { method: 'DELETE' })
}

// ---------------------------------------------------------------------------
// Rates
// ---------------------------------------------------------------------------

export type RoomRef = { id: number; slug: string; name: string }

/**
 * Season dates are INCLUSIVE — endsOn is a night, not a checkout — deliberately
 * the opposite of every occupancy span in this system. The screen labels it
 * "last night" for that reason.
 */
export type Season = {
  id: number
  name: string
  startsOn: string
  endsOn: string
  minStay: number | null
  priority: number
  /** Keyed by room id. A room absent is one this season does not price. */
  prices: Record<string, number>
}

export type RateBoard = {
  seasons: Season[]
  rooms: RoomRef[]
  defaultMinStay: number
  maxStayNights: number
  /** How far forward the calendar is priced. Empty means no calendar at all. */
  horizon?: string
}

export type RateChange = {
  nights: number
  rooms: number
  nightsGainingAPrice: number
  nightsLosingTheirPrice: number
  firstNight?: string
  lastNight?: string
  /** Confirmed stays in range. Reported so the screen can say they are safe. */
  confirmedBookings: number
}

export function fetchRates(): Promise<RateBoard> {
  return request<RateBoard>('/api/admin/rates')
}

/** What saving would change, computed by applying the edit and rolling back. */
export function previewSeason(season: Season): Promise<RateChange> {
  return request<RateChange>('/api/admin/rates/preview', {
    method: 'POST',
    body: JSON.stringify(season),
  })
}

export function saveSeason(season: Season): Promise<RateChange> {
  return request<RateChange>('/api/admin/rates/seasons', {
    method: 'POST',
    body: JSON.stringify(season),
  })
}

export function deleteSeason(id: number): Promise<RateChange> {
  return request<RateChange>(`/api/admin/rates/seasons/${id}`, { method: 'DELETE' })
}

export function rebuildRates(): Promise<{ nights: number }> {
  return request<{ nights: number }>('/api/admin/rates/rebuild', { method: 'POST' })
}

// ---------------------------------------------------------------------------
// Guests
// ---------------------------------------------------------------------------

export type GuestCard = {
  id: number
  name: string
  email: string
  phone?: string
  /** Confirmed stays only: a cancelled booking is not a visit. */
  stays: number
  lifetimeCents: number
  lastCheckout?: string
  notes: number
}

export type Note = {
  id: number
  body: string
  author?: string
  at: string
}

export type GuestFile = {
  guest: GuestCard
  bookings: Stay[]
  notes: Note[]
}

export function fetchGuests(params: {
  q?: string
  room?: number
  from?: string
  to?: string
}): Promise<GuestCard[]> {
  return request<GuestCard[]>(`/api/admin/guests${query(params)}`)
}

export function fetchGuest(id: number): Promise<GuestFile> {
  return request<GuestFile>(`/api/admin/guests/${id}`)
}

export function addNote(guestId: number, body: string): Promise<Note> {
  return request<Note>(`/api/admin/guests/${guestId}/notes`, {
    method: 'POST',
    body: JSON.stringify({ body }),
  })
}

export function deleteNote(guestId: number, noteId: number): Promise<void> {
  return request<void>(`/api/admin/guests/${guestId}/notes/${noteId}`, { method: 'DELETE' })
}

// ---------------------------------------------------------------------------
// Content
// ---------------------------------------------------------------------------

export type Photo = { path: string; alt: string }
export type Bed = { type: string; count: number; location?: string }

export type RoomContent = {
  id: number
  slug: string
  name: string
  description: string
  view?: string
  maxOccupancy: number
  amenities: string[]
  isAccessible: boolean
  accessibilityFeatures: string[]
  isPetFriendly: boolean
  petFeeCents: number
  sortOrder: number
  photos: Photo[]
  beds: Bed[]
}

export function fetchRooms(): Promise<RoomContent[]> {
  return request<RoomContent[]>('/api/admin/rooms')
}

export function saveRoom(room: RoomContent): Promise<void> {
  return request<void>(`/api/admin/rooms/${room.id}`, {
    method: 'PUT',
    body: JSON.stringify(room),
  })
}

/**
 * Both rates cross the wire pre-scaled to hundred-thousandths, matching
 * pricing.Rate on the server, so no percentage ever becomes a float on the way.
 * The screen shows percentages and converts at the edge.
 */
export type Settings = {
  defaultMinStay: number
  maxStayNights: number
  taxRateScaled: number
  refundProcessingRateScaled: number
  holdTtlMinutes: number
  paymentGraceMinutes: number
  checkinTime: string
  checkoutTime: string
  accessibilityNotice: string
}

export function fetchSettings(): Promise<Settings> {
  return request<Settings>('/api/admin/settings')
}

export function saveSettings(settings: Settings): Promise<void> {
  return request<void>('/api/admin/settings', {
    method: 'PUT',
    body: JSON.stringify(settings),
  })
}

export type MenuItem = {
  name: string
  description: string
  /** Zero means no price of its own — market price, or part of a prix fixe. */
  priceCents: number
  available: boolean
  /**
   * What the kitchen states the dish suits. Unticked is *unmarked* — the public
   * menu shows an icon for what was ticked and claims nothing about the rest.
   */
  glutenFree: boolean
  vegan: boolean
  vegetarian: boolean
}

export type MenuSection = {
  name: string
  description: string
  items: MenuItem[]
}

export function fetchMenu(): Promise<MenuSection[]> {
  return request<MenuSection[]>('/api/admin/menu')
}

/** The menu saves as one document; the failure mode is the previous menu. */
export function saveMenu(sections: MenuSection[]): Promise<void> {
  return request<void>('/api/admin/menu', {
    method: 'PUT',
    body: JSON.stringify(sections),
  })
}

export type EventItem = {
  title: string
  happensOn?: string
  description: string
  published: boolean
  photos: Photo[]
}

export function fetchEvents(): Promise<EventItem[]> {
  return request<EventItem[]>('/api/admin/events')
}

export function saveEvents(events: EventItem[]): Promise<void> {
  return request<void>('/api/admin/events', {
    method: 'PUT',
    body: JSON.stringify(events),
  })
}

export type InquiryStatus = 'new' | 'contacted' | 'closed'

export type InquiryKind = 'event' | 'contact'

export type Inquiry = {
  id: number
  name: string
  email: string
  phone?: string
  eventDate?: string
  partySize?: number
  message: string
  status: InquiryStatus
  /** Which form wrote it — the events enquiry, or the home page contact box. */
  kind: InquiryKind
  at: string
}

export function fetchInquiries(status?: string, kind?: string): Promise<Inquiry[]> {
  return request<Inquiry[]>(`/api/admin/inquiries${query({ status, kind })}`)
}

export function setInquiryStatus(id: number, status: InquiryStatus): Promise<void> {
  return request<void>(`/api/admin/inquiries/${id}`, {
    method: 'PUT',
    body: JSON.stringify({ status }),
  })
}

/**
 * One of the seven messages the inn sends.
 *
 * `edited` false means nothing has been saved and guests are receiving the
 * shipped placeholder — which the screen has to say out loud rather than
 * presenting a placeholder as though it were finished copy.
 */
export type EmailCopy = {
  name: string
  subject: string
  body: string
  edited: boolean
  /**
   * What this message can say about the booking, and the whole of it — a name
   * not in here renders as nothing, silently. Derived on the server from the
   * payload struct rather than listed in this bundle, so it cannot drift from
   * what the message actually carries.
   */
  fields: { name: string; list: boolean }[]
}

export function fetchEmailCopy(): Promise<EmailCopy[]> {
  return request<EmailCopy[]>('/api/admin/email-templates')
}

/** Refused server-side if the copy will not compile — before it is stored. */
export function saveEmailCopy(name: string, subject: string, body: string): Promise<void> {
  return request<void>(`/api/admin/email-templates/${encodeURIComponent(name)}`, {
    method: 'PUT',
    body: JSON.stringify({ subject, body }),
  })
}

/** A delete, not a rewrite: the shipped copy lives in the repository. */
export function resetEmailCopy(name: string): Promise<void> {
  return request<void>(`/api/admin/email-templates/${encodeURIComponent(name)}`, {
    method: 'DELETE',
  })
}

export type PageCopy = {
  slug: string
  heading: string
  body: string
  /** False when the page is showing its structure with nothing in the slot. */
  written: boolean
  /** The page's gallery, from `page_photos`. Saved separately — see below. */
  photos: Photo[]
}

export function fetchCopy(): Promise<PageCopy[]> {
  return request<PageCopy[]>('/api/admin/copy')
}

export function savePageCopy(page: PageCopy): Promise<void> {
  return request<void>(`/api/admin/copy/${encodeURIComponent(page.slug)}`, {
    method: 'PUT',
    body: JSON.stringify(page),
  })
}

/**
 * Replaces one page's photographs.
 *
 * A separate request from the copy, because the two are independent on the
 * server: emptying the heading and the body deletes the page_copy row, and a
 * gallery riding along on that request would go with it. A page may have
 * pictures and no prose — the restaurant page has, all year.
 */
export function savePagePhotos(slug: string, photos: Photo[]): Promise<void> {
  return request<void>(`/api/admin/copy/${encodeURIComponent(slug)}/photos`, {
    method: 'PUT',
    body: JSON.stringify(photos),
  })
}

/** One entry in the local-area page's nearby list. */
export type Attraction = {
  name: string
  /** Free text — "Walking distance" is not a number of minutes. */
  distance: string
  /** A sentence or two. Empty renders the entry without one. */
  description: string
  /** Empty means no link. The page renders the name as plain text. */
  url: string
}

export function fetchAttractions(): Promise<Attraction[]> {
  return request<Attraction[]>('/api/admin/attractions')
}

export function saveAttractions(list: Attraction[]): Promise<void> {
  return request<void>('/api/admin/attractions', {
    method: 'PUT',
    body: JSON.stringify(list),
  })
}
