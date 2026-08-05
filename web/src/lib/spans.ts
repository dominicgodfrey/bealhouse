import type { Calendar, Span } from './api'
import { addDays, nights } from './dates'

// Turning the API's per-room spans into the two questions the picker asks.
//
// The rule that matters (decision #14): a stay is offerable only if ONE room
// covers all of it. Merging every room's free nights into a single set of
// "available days" would let a guest pick a range that room A covers early and
// room B covers late, with nothing for sale in between — a selection that looks
// fine until the results page comes back empty.
//
// The minimum stay is evaluated at the arrival night only, matching the
// availability query. A three-night season should stop a stay that starts
// inside it, not one that merely passes through.

export type SpanIndex = {
  /** Dates a guest can arrive on: some room can house the whole minimum stay. */
  arrivals: Set<string>
  /** Dates a guest can leave on, given where they are arriving. */
  departures(checkin: string): Set<string>
  /** Whether any room covers this exact stay. */
  covers(checkin: string, checkout: string): boolean
  empty: boolean
  /** The longest stay on sale, so the picker can say why it stops offering. */
  maxStayNights: number
}

/** offsetIn returns how far into a span a date falls, or -1 if it is outside. */
function offsetIn(span: Span, date: string): number {
  const offset = nights(span.start, date)
  return offset >= 0 && offset < span.minStays.length ? offset : -1
}

export function indexSpans(calendar: Calendar | null): SpanIndex {
  const spans: Span[] = calendar?.rooms.flatMap((room) => room.spans) ?? []

  // The longest stay on sale (decision #27). Sent by the API rather than
  // hardcoded here, and mirrored by availability.Search on the server, so a
  // guest cannot get a longer stay through by editing the request. Zero or a
  // missing value means no cap, matching how the server reads it.
  const maxStayNights = calendar?.maxStayNights ?? 0

  const arrivals = new Set<string>()
  for (const span of spans) {
    for (let offset = 0; offset < span.minStays.length; offset++) {
      // Room to leave again: the nights the minimum demands have to fit inside
      // what is left of this run.
      if (offset + span.minStays[offset] <= span.minStays.length) {
        arrivals.add(addDays(span.start, offset))
      }
    }
  }

  function departures(checkin: string): Set<string> {
    const out = new Set<string>()
    for (const span of spans) {
      const offset = offsetIn(span, checkin)
      if (offset < 0) continue

      let remaining = span.minStays.length - offset
      if (maxStayNights > 0) {
        remaining = Math.min(remaining, maxStayNights)
      }
      for (let stay = span.minStays[offset]; stay <= remaining; stay++) {
        out.add(addDays(checkin, stay))
      }
    }
    return out
  }

  return {
    arrivals,
    departures,
    covers: (checkin, checkout) => departures(checkin).has(checkout),
    empty: spans.length === 0,
    maxStayNights,
  }
}

/**
 * The shortest stay available from a date, for the hint under the picker.
 * Returns 0 when nothing can start there.
 */
export function shortestStay(index: SpanIndex, checkin: string): number {
  let shortest = 0
  for (const departure of index.departures(checkin)) {
    const stay = nights(checkin, departure)
    if (shortest === 0 || stay < shortest) shortest = stay
  }
  return shortest
}
