/**
 * The marketing site's own API: the rooms index, the restaurant menu, the
 * events list, the owner's prose, and the inquiry form.
 *
 * Everything here is content the owner manages in the console, and every one of
 * these endpoints answers with an empty list or an empty page rather than a
 * placeholder when nothing has been written. The pages are built to say "not
 * yet" rather than to invent a sentence about the restaurant — a placeholder is
 * something somebody has to remember to delete, and this one would be on the
 * public internet.
 */

import { request } from './api'
import type { PhotoSources } from '../components/Photo'

export type Photo = { url: string; alt: string } & PhotoSources

export type RoomCard = {
  slug: string
  name: string
  description: string
  view?: string
  maxOccupancy: number
  amenities: string[]
  photos: Photo[]
  /** The bundled drawing to show while a room has no uploaded photograph. */
  placeholderPhotoUrl: string
  isPetFriendly: boolean
  petFeeCents?: number
  /**
   * The cheapest night currently on the calendar. Absent — not zero — for a
   * room no season prices, because such a room cannot be sold at all and
   * "from $0" is a promise the booking flow then refuses to honour.
   */
  fromCents?: number
}

export function fetchRoomCards(): Promise<RoomCard[]> {
  return request<RoomCard[]>('/api/rooms')
}

export type MenuItem = {
  name: string
  description: string
  priceCents: number
  available: boolean
  /**
   * What the kitchen states the dish suits. False is *unmarked*, never a claim
   * that the dish contains the thing — the menu shows an icon for what was
   * ticked and says nothing about what was not.
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
  return request<MenuSection[]>('/api/menu')
}

export type EventItem = {
  title: string
  happensOn?: string
  description: string
  photos: ({ path: string; alt: string } & PhotoSources)[]
}

export function fetchEvents(): Promise<EventItem[]> {
  return request<EventItem[]>('/api/events')
}

/** A photograph on a page rather than on a room, from `page_photos`. */
export type PagePhoto = { path: string; alt: string } & PhotoSources

export type PageCopy = {
  slug: string
  heading: string
  body: string
  written: boolean
  /**
   * The page's gallery. Independent of `written`: the restaurant page has had
   * photographs and no sentences all year, and either can be empty on its own.
   */
  photos: PagePhoto[]
}

export function fetchPageCopy(slug: string): Promise<PageCopy> {
  return request<PageCopy>(`/api/copy/${encodeURIComponent(slug)}`)
}

/** One entry in the local-area page's nearby list. */
export type Attraction = {
  name: string
  /** Free text: "Walking distance" is the honest answer for half the list. */
  distance: string
  /**
   * A sentence or two about the place. Empty renders the row as a name and a
   * distance alone, which is what it was before — not a placeholder.
   */
  description: string
  /** Empty means no link — the name renders as plain text, not a dead anchor. */
  url: string
}

export function fetchAttractions(): Promise<Attraction[]> {
  return request<Attraction[]>('/api/attractions')
}

/**
 * The booking and refund rules, as the server actually applies them.
 *
 * Read from settings and from the pricing package rather than typed into the
 * policy copy, so the page a guest is asked to agree to cannot drift from what
 * the code will do to their money.
 */
export type PolicyTerms = {
  minStayNights: number
  maxStayNights: number
  checkinTime: string
  checkoutTime: string
  holdMinutes: number
  taxRatePercent: string
  refundProcessingPercent: string
  depositPercent: number
  balanceLeadDays: number
  shortNoticeDays: number
  freeCancellationLeadDays: number
}

export function fetchPolicyTerms(): Promise<PolicyTerms> {
  return request<PolicyTerms>('/api/policies')
}

export type NewInquiry = {
  name: string
  email: string
  phone: string
  eventDate: string
  partySize: number
  message: string
  /**
   * Which form sent it. Both land in the same inbox and the owner answers them
   * the same way; the label is what tells a general question from a wedding
   * enquiry. Omitted reads as 'event' on the server, which is what every row
   * was before the contact form existed.
   */
  kind?: 'event' | 'contact'
}

export function submitInquiry(inquiry: NewInquiry): Promise<void> {
  return request<void>('/api/inquiries', {
    method: 'POST',
    body: JSON.stringify(inquiry),
  })
}

/**
 * Splits the owner's plain-text body into paragraphs.
 *
 * Plain text and not markdown, deliberately: the copy is sentences about an
 * inn, and a rich editor would mean either a parser in this bundle or a way to
 * put a <script> on the public site from a phone. Blank lines separate
 * paragraphs, which is what everybody types anyway.
 */
export function paragraphs(body: string): string[] {
  return body
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean)
}
