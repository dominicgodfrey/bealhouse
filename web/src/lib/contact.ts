/**
 * Where the inn is and how to reach it.
 *
 * The front end's copy of the block at the top of `internal/httpx/meta.go`, which
 * carries the same details for the structured data. No build step shares them,
 * so **change both**.
 *
 * Deliberately not `page_copy`: an address is site chrome, it belongs in the
 * footer of pages that render no prose at all, and an empty console field must
 * not be able to take the telephone number off the site.
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
 * The map on the About page: OpenStreetMap's embed, an iframe and no
 * JavaScript. No API key, nothing third-party running on the page that also
 * has the contact form, and one `frame-src` entry rather than a `script-src`
 * one too — `internal/httpx/middleware.go` has the matching line.
 *
 * The coordinates are OSM's own record of the building, and the same pair the
 * LodgingBusiness JSON-LD publishes.
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
