package httpx

import (
	"encoding/xml"
	"net/http"
	"strings"
	"time"
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

// robotsTXT welcomes crawlers, and says which four corners of the site are not
// for them.
//
// The welcome is the point and it is deliberate: an inn wants to be found, by a
// search engine and equally by whatever is answering somebody's question about
// where to stay near Franconia Notch. `Allow: /` is stated rather than left
// implied so that the intent survives somebody reading this file in two years
// and wondering whether the omission was on purpose.
//
// **One group, not one per crawler.** A named group replaces the `*` group
// wholesale for that agent rather than adding to it, so `User-agent: GPTBot`
// followed by `Allow: /` would hand that one crawler the booking flow with the
// Disallow lines silently not applying to it. Every crawler worth having obeys
// `*`; the ones that do not would ignore a group addressed to them as well.
// So the agents below are named in a comment and governed by the same rules as
// everybody else.
//
// The disallowed prefixes are the same ones the head marks noindex, and they
// are listed in both places on purpose: noindex keeps a page out of the results
// once it has been fetched, and Disallow stops it being fetched at all. The
// booking flow is the one that matters, because /book and /bookings take a real
// room off sale for the hold TTL: a crawler walking them is a crawler quietly
// emptying the inn's inventory (decision #29 is about the same risk from the
// other direction).
func robotsTXT(siteURL string) http.HandlerFunc {
	var b strings.Builder
	b.WriteString("# " + innName + ", " + innStreet + ", " + innLocality + ", " + innRegion + ".\n")
	b.WriteString("# Crawling and indexing are welcome, search engines and assistants alike:\n")
	b.WriteString("# Googlebot, Bingbot, GPTBot, ClaudeBot, PerplexityBot, Applebot,\n")
	b.WriteString("# Google-Extended, OAI-SearchBot and anything else that reads this file.\n")
	b.WriteString("# The four prefixes below are the console, the API and the booking flow,\n")
	b.WriteString("# which take real rooms off sale when walked. Everything else is yours.\n")
	b.WriteString("# There is a summary of the inn in plain text at /llms.txt.\n\n")

	b.WriteString("User-agent: *\n")
	b.WriteString("Allow: /\n")
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
	Loc string `xml:"loc"`

	// W3C datetime, which is what the schema asks for and what Google reads to
	// decide whether a page it already has is worth fetching again.
	//
	// <priority> and <changefreq> used to be here and are gone: Google has said
	// for years that it ignores both, and a number nobody reads is a number
	// somebody eventually maintains. Omitted per URL rather than guessed — a
	// lastmod of "now" on every page, which is what a generated file drifts
	// into, teaches a crawler to ignore the field.
	LastMod string `xml:"lastmod,omitempty"`
}

// w3cDate formats a timestamp for a <lastmod>, and formats nothing for a zero
// one.
func w3cDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
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

		// When the owner last edited each page's prose, so the five marketing
		// pages carry a true lastmod rather than a shared guess. A page with no
		// row has never been written and gets none.
		edited := meta.pageEdits(r.Context())

		pages := []sitemap{
			{Loc: meta.absolute("/"), LastMod: w3cDate(edited["home"])},
			{Loc: meta.absolute("/rooms"), LastMod: w3cDate(edited["rooms"])},
			{Loc: meta.absolute("/restaurant"), LastMod: w3cDate(edited["restaurant"])},
			{Loc: meta.absolute("/events"), LastMod: w3cDate(edited["events"])},
			{Loc: meta.absolute("/local-area"), LastMod: w3cDate(edited["local-area"])},
			{Loc: meta.absolute("/about"), LastMod: w3cDate(edited["about"])},
			{Loc: meta.absolute("/policies"), LastMod: w3cDate(edited["policies"])},
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
			newest := time.Time{}
			for _, card := range cards {
				pages = append(pages, sitemap{
					Loc:     meta.absolute("/rooms/" + card.Slug),
					LastMod: w3cDate(card.UpdatedAt),
				})
				if card.UpdatedAt.After(newest) {
					newest = card.UpdatedAt
				}
			}
			// The index changes when any room on it does, which is later than
			// the last time anybody wrote prose for the page itself.
			if pages[1].LastMod == "" || w3cDate(newest) > pages[1].LastMod {
				pages[1].LastMod = w3cDate(newest)
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
