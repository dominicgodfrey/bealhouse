/**
 * Where the inn is and how to reach it.
 *
 * This is the front end's copy of the block at the top of `internal/httpx/meta.go`,
 * which carries the same details for the structured data a search engine reads.
 * Two copies in two languages with no build step between them, so **change
 * both** — a footer and a map pin that disagree about the street is the exact
 * failure a guest discovers in a car.
 *
 * These are not owner-managed content and deliberately not in `page_copy`. An
 * address is site chrome: it belongs in the footer of every page including the
 * ones that render no prose at all, and a page-copy slot that was left empty
 * would take the telephone number off the site.
 */

export const inn = {
  name: 'The Beal House',
  street: '2 West Main Street',
  locality: 'Littleton',
  region: 'New Hampshire',
  postalCode: '03561',

  /** As it is written and read aloud. */
  phone: '(603) 444-2661',
  /** As `tel:` wants it — no spaces, no brackets, country code included. */
  phoneHref: 'tel:+16034442661',

  email: 'info@thebealhouse.com',
} as const

/** "2 West Main Street, Littleton, New Hampshire 03561", on one line. */
export const innAddressLine = `${inn.street}, ${inn.locality}, ${inn.region} ${inn.postalCode}`

/**
 * The map on the About page.
 *
 * OpenStreetMap's own embed, which is an iframe and no JavaScript: no API key
 * to keep secret, nothing third-party running on a page that also has a contact
 * form on it, and one entry in the CSP's `frame-src` rather than one in
 * `script-src` as well. `internal/httpx/middleware.go` has the matching line.
 *
 * The coordinates are OpenStreetMap's own record of the building, so the marker
 * lands on the house rather than on the middle of the street, and they are the
 * same pair the LodgingBusiness JSON-LD publishes.
 */
const latitude = 44.3086662
const longitude = -71.781512

export const mapEmbedUrl =
  'https://www.openstreetmap.org/export/embed.html' +
  '?bbox=-71.7875%2C44.3057%2C-71.7755%2C44.3117' +
  '&layer=mapnik' +
  `&marker=${latitude}%2C${longitude}`

/** The full map, for anyone who wants to zoom out or ask for directions. */
export const mapLinkUrl = `https://www.openstreetmap.org/?mlat=${latitude}&mlon=${longitude}#map=17/${latitude}/${longitude}`
