package httpx

import (
	"encoding/xml"
	"net/http"
	"strings"
)

// robots.txt and sitemap.xml.
//
// Both are generated rather than shipped in web/public, for the same reason the
// head is: the set of pages worth crawling includes one URL per room, and the
// rooms are rows in a database the owner edits. A static file listing them is a
// file somebody has to remember to change.
//
// Both are registered on the root router ahead of the SPA fallback, exactly
// like /media/*. Left to the fallback they would answer index.html with a 200,
// and a crawler reading a page of HTML as a robots.txt does not behave in any
// way anybody would predict.

// robotsTXT tells crawlers what not to walk.
//
// The disallowed prefixes are the same ones the head marks noindex, and they
// are listed in both places on purpose: noindex keeps a page out of the results
// once it has been fetched, and Disallow stops it being fetched at all. The
// booking flow is the one that matters — /book and /bookings take a real room
// off sale for the hold TTL, so a crawler walking them is a crawler quietly
// emptying the inn's inventory (decision #29 is about the same risk from the
// other direction).
func robotsTXT(siteURL string) http.HandlerFunc {
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	for _, path := range []string{"/admin", "/api", "/book/", "/bookings/", "/booking/", "/search", "/health"} {
		b.WriteString("Disallow: " + path + "\n")
	}
	// A sitemap reference has to be absolute, and with no SITE_URL there is no
	// origin to build one from — so the line is left out rather than guessed.
	if siteURL != "" {
		b.WriteString("\nSitemap: " + strings.TrimSuffix(siteURL, "/") + "/sitemap.xml\n")
	}
	body := []byte(b.String())

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(body)
	}
}

// urlset is the sitemaps.org schema, which is small enough to write out.
type urlset struct {
	XMLName xml.Name  `xml:"urlset"`
	NS      string    `xml:"xmlns,attr"`
	URLs    []sitemap `xml:"url"`
}

type sitemap struct {
	Loc      string `xml:"loc"`
	Priority string `xml:"priority,omitempty"`
}

// sitemapXML lists the pages worth indexing: the five marketing pages and one
// URL per room.
//
// It answers 404 without a SITE_URL rather than emitting relative locations. A
// <loc> is defined as absolute, and a sitemap full of invalid ones is not a
// partial sitemap — it is a file that gets rejected whole, with an error in
// Search Console pointing at a problem in the wrong place.
func sitemapXML(meta *siteMeta) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if meta.siteURL == "" {
			http.NotFound(w, r)
			return
		}

		pages := []sitemap{
			{Loc: meta.absolute("/"), Priority: "1.0"},
			{Loc: meta.absolute("/rooms"), Priority: "0.9"},
			{Loc: meta.absolute("/restaurant"), Priority: "0.8"},
			{Loc: meta.absolute("/events"), Priority: "0.7"},
			{Loc: meta.absolute("/about"), Priority: "0.5"},
		}

		// One entry per room, from the same read model the rooms index and the
		// head both use. A room the owner adds appears here the moment it
		// exists, and one they remove stops being advertised — which is the
		// whole reason this is not a file in web/public.
		//
		// A database that cannot be reached costs the room URLs and not the
		// response: the five pages above are still true, and an empty sitemap
		// would tell a crawler the room pages had been withdrawn.
		if cards, ok := meta.cards(r.Context()); ok {
			for _, card := range cards {
				pages = append(pages, sitemap{
					Loc:      meta.absolute("/rooms/" + card.Slug),
					Priority: "0.8",
				})
			}
		}

		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.Write([]byte(xml.Header))

		enc := xml.NewEncoder(w)
		enc.Indent("", "  ")
		if err := enc.Encode(urlset{
			NS:   "http://www.sitemaps.org/schemas/sitemap/0.9",
			URLs: pages,
		}); err != nil {
			// Too late for a status code; the header and some of the body are
			// already on the wire. Log it and let the crawler see a truncated
			// document, which it will retry.
			logRequestError(r, err)
		}
	}
}
