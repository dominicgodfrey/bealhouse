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

export type PageCopy = {
  slug: string
  heading: string
  body: string
  written: boolean
}

export function fetchPageCopy(slug: string): Promise<PageCopy> {
  return request<PageCopy>(`/api/copy/${encodeURIComponent(slug)}`)
}

export type NewInquiry = {
  name: string
  email: string
  phone: string
  eventDate: string
  partySize: number
  message: string
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
