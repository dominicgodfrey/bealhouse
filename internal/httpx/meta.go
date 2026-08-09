package httpx

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/url"
	"strings"
	"unicode"

	"bealhouse/internal/console"
	db "bealhouse/internal/db/gen"
)

// Per-route <head>, written by the server (decision #3).
//
// The SPA is one document for every address, so without this a crawler that
// does not run JavaScript sees the same empty shell for the home page, all
// seven rooms, the restaurant and the events — one title, no description, no
// structured data anywhere. That is the whole of the site's search presence,
// and the pages it costs most are the room pages, which are the ones somebody
// arrives at from a search engine rather than from the front door.
//
// Two rules run through everything below, and they are the same two the pages
// themselves follow:
//
//   - **Nothing is invented.** A description is the owner's own words where
//     they have written some and is *absent* where they have not, exactly as
//     the page renders no paragraph rather than a placeholder. The one
//     exception is the home page's fallback, which says only what is already
//     true elsewhere in this repository — an inn in Littleton, New Hampshire,
//     booked direct — and is the same sentence the footer has carried all
//     along.
//   - **The tags say what the page says.** Every value here comes from the
//     same read models the page's own API calls return, so the document a
//     crawler indexes and the document a visitor reads cannot describe
//     different rooms at different prices.
//
// Absolute URLs need an origin, so `og:url`, `og:image` and the canonical link
// appear only when SITE_URL is set — the same rule the email letterhead
// follows, and for the same reason: a guessed origin is worse than none.
const (
	innName     = "Beal House"
	innLocality = "Littleton"
	innRegion   = "NH"
	innCountry  = "US"
)

// metaDescriptionLimit is where a description gets cut. Search engines show
// roughly this much and truncate the rest mid-word; doing it here means the cut
// lands on a word boundary and ends in an ellipsis that reads as deliberate.
const metaDescriptionLimit = 155

// headMeta is one page's worth of <head>.
//
// Every string field is optional and an empty one emits no tag at all, which is
// what keeps "the owner has not written this yet" from turning into an empty
// meta description on the public internet.
type headMeta struct {
	Title       string
	Description string
	Canonical   string
	Image       string

	// NoIndex covers everything that is not a marketing page: the console, the
	// booking flow, a guest's own manage-booking link. None of it should be in
	// a search index — the console because it is private, and the booking pages
	// because they are one guest's transaction and are meaningless to anybody
	// else who lands on them.
	NoIndex bool

	// LD is the JSON-LD blocks, already marshalled. json.Marshal escapes <, >
	// and & by default, so nothing in here can close the script element it sits
	// in, whatever the owner typed into the console.
	LD []template.JS
}

// siteMeta builds the head for a path. Every field may be absent: with no
// database there is no content to describe, and with no SITE_URL there is no
// origin to make a URL absolute with. Both degrade to a smaller head rather
// than to a failed request.
type siteMeta struct {
	q       *db.Queries
	ops     *console.Ops
	siteURL string
}

// forPath is the routing table, and it mirrors web/src/main.tsx.
//
// A path this does not recognise gets the bare title and no indexing: an
// unknown address under a SPA fallback is either a typo or a route somebody
// added here and not there, and neither is something to advertise to a crawler.
func (s *siteMeta) forPath(ctx context.Context, path string) headMeta {
	meta := headMeta{Title: innName, Canonical: s.absolute(path)}

	switch {
	case path == "/":
		return s.home(ctx, meta)
	case path == "/rooms":
		return s.rooms(ctx, meta)
	case strings.HasPrefix(path, "/rooms/"):
		return s.room(ctx, meta, strings.TrimPrefix(path, "/rooms/"))
	case path == "/restaurant":
		return s.restaurant(ctx, meta)
	case path == "/events":
		return s.events(ctx, meta)
	case path == "/local-area":
		return s.localArea(ctx, meta)
	case path == "/policies":
		return s.policyPage(ctx, meta)
	}

	// The booking flow, the guest's own booking, and the console.
	meta.NoIndex = true
	meta.Canonical = ""
	return meta
}

// title composes "Rooms · Beal House", and leaves the home page as just the
// inn's name — a home page titled "Home · Beal House" wastes the most valuable
// characters in a search result on the word "home".
func title(section string) string { return section + " · " + innName }

func (s *siteMeta) home(ctx context.Context, meta headMeta) headMeta {
	meta.Title = innName + " — inn in " + innLocality + ", New Hampshire"

	// The one page that speaks for itself when the owner has written nothing,
	// on the same terms as the About page's fallback: both sentences below are
	// already in the site footer, so this states nothing new about a house we
	// know nothing about.
	meta.Description = s.copyFor(ctx, "home")
	if meta.Description == "" {
		meta.Description = "An inn in " + innLocality +
			", New Hampshire. Book direct — no booking fees."
	}

	cards, ok := s.cards(ctx)
	if !ok {
		return meta
	}

	meta.Image = s.leadPhoto(cards)

	business := map[string]any{
		"@context": "https://schema.org",
		"@type":    "LodgingBusiness",
		"name":     innName,
		"address": map[string]any{
			"@type":           "PostalAddress",
			"addressLocality": innLocality,
			"addressRegion":   innRegion,
			"addressCountry":  innCountry,
		},
	}
	// No streetAddress and no telephone: neither is anywhere in this
	// repository, and a structured address is precisely the wrong place to
	// guess — it is what a map pin is built from.
	if len(cards) > 0 {
		business["numberOfRooms"] = len(cards)
	}
	if s.siteURL != "" {
		business["url"] = s.siteURL
		business["logo"] = s.absolute("/logo.svg")
	}
	if meta.Image != "" {
		business["image"] = meta.Image
	}

	meta.LD = append(meta.LD, marshalLD(business))
	return meta
}

func (s *siteMeta) rooms(ctx context.Context, meta headMeta) headMeta {
	meta.Title = title("Rooms")
	meta.Description = s.copyFor(ctx, "rooms")

	cards, ok := s.cards(ctx)
	if !ok {
		return meta
	}
	meta.Image = s.leadPhoto(cards)

	items := make([]any, 0, len(cards))
	for i, card := range cards {
		items = append(items, map[string]any{
			"@type":    "ListItem",
			"position": i + 1,
			"url":      s.absolute("/rooms/" + card.Slug),
			"item":     s.roomLD(card, false),
		})
	}

	meta.LD = append(meta.LD, marshalLD(map[string]any{
		"@context":        "https://schema.org",
		"@type":           "ItemList",
		"name":            "Rooms at " + innName,
		"numberOfItems":   len(items),
		"itemListElement": items,
	}))
	return meta
}

func (s *siteMeta) room(ctx context.Context, meta headMeta, slug string) headMeta {
	cards, ok := s.cards(ctx)
	if !ok {
		meta.Title = title("Rooms")
		return meta
	}

	for _, card := range cards {
		if card.Slug != slug {
			continue
		}

		meta.Title = title(card.Name)
		// The room's own description, or nothing. Whatever the room page shows
		// a visitor is what this says, including the seed's PLACEHOLDER text if
		// that is still what is in the database — the fix for that is the owner
		// writing a description, not this file hiding it.
		meta.Description = summarise(card.Description)
		if len(card.Photos) > 0 {
			meta.Image = s.absolute(card.Photos[0].URL)
		}
		meta.LD = append(meta.LD, marshalLD(s.roomLD(card, true)))
		return meta
	}

	// No such room. The API answers 404 for this slug, but the SPA fallback
	// cannot — it has already committed to serving the document. Keeping it out
	// of the index is the part that matters.
	meta.Title = title("Rooms")
	meta.NoIndex = true
	meta.Canonical = ""
	return meta
}

// roomLD is one room as schema.org sees it. The `context` flag adds @context,
// which belongs on a block that stands alone and not on one nested inside a
// list that already has it.
func (s *siteMeta) roomLD(card RoomCard, standalone bool) map[string]any {
	room := map[string]any{
		"@type": "HotelRoom",
		"name":  card.Name,
	}
	if standalone {
		room["@context"] = "https://schema.org"
	}
	if card.Description != "" {
		room["description"] = card.Description
	}
	if card.MaxOccupancy > 0 {
		room["occupancy"] = map[string]any{
			"@type":       "QuantitativeValue",
			"maxValue":    card.MaxOccupancy,
			"unitCode":    "C62",
			"unitText":    "person",
			"description": "Maximum occupancy",
		}
	}
	if len(card.Amenities) > 0 {
		features := make([]any, 0, len(card.Amenities))
		for _, amenity := range card.Amenities {
			features = append(features, map[string]any{
				"@type": "LocationFeatureSpecification",
				"name":  amenity,
				"value": true,
			})
		}
		room["amenityFeature"] = features
	}
	// Only real photographs. The per-room placeholder SVG is a UI fallback
	// standing in for a picture that does not exist, and offering it to a
	// crawler as `photo` would put a line drawing in an image search result.
	if photos := s.absolutePhotos(card.Photos); len(photos) > 0 {
		room["photo"] = photos
	}
	if url := s.absolute("/rooms/" + card.Slug); url != "" {
		room["url"] = url
	}
	// The cheapest night currently on the calendar, which is exactly what the
	// card shows as "from". A room no season prices has no FromCents and gets
	// no offer, because it cannot be sold at all.
	if card.FromCents != nil {
		room["offers"] = map[string]any{
			"@type":         "Offer",
			"price":         dollars(*card.FromCents),
			"priceCurrency": "USD",
			"availability":  "https://schema.org/InStock",
		}
	}
	return room
}

func (s *siteMeta) restaurant(ctx context.Context, meta headMeta) headMeta {
	meta.Title = title("Restaurant")
	meta.Description = s.copyFor(ctx, "restaurant")

	if s.ops == nil {
		return meta
	}
	sections, err := s.ops.PublicMenu(ctx)
	if err != nil {
		s.warn("menu", err)
		return meta
	}

	restaurant := map[string]any{
		"@context": "https://schema.org",
		"@type":    "Restaurant",
		"name":     "The restaurant at " + innName,
		"address": map[string]any{
			"@type":           "PostalAddress",
			"addressLocality": innLocality,
			"addressRegion":   innRegion,
			"addressCountry":  innCountry,
		},
	}
	if url := s.absolute("/restaurant"); url != "" {
		restaurant["url"] = url
	}

	// A menu nobody has entered is no `hasMenu` at all. An empty Menu object
	// tells a search engine the restaurant serves nothing, which is worse than
	// telling it nothing about the menu (decision #12).
	if courses := menuLD(sections); courses != nil {
		restaurant["hasMenu"] = courses
	}

	meta.LD = append(meta.LD, marshalLD(restaurant))
	return meta
}

// menuLD renders the menu the restaurant page just rendered, from the same
// rows, so the structured copy and the printed one cannot disagree — which is
// the whole reason the menu is data and not a PDF.
func menuLD(sections []console.MenuSection) map[string]any {
	courses := make([]any, 0, len(sections))
	for _, section := range sections {
		if len(section.Items) == 0 {
			continue
		}
		items := make([]any, 0, len(section.Items))
		for _, item := range section.Items {
			dish := map[string]any{"@type": "MenuItem", "name": item.Name}
			if item.Description != "" {
				dish["description"] = item.Description
			}
			// Zero is "no price of its own" — a market-price special, or a side
			// inside a prix fixe — and an Offer of $0.00 would be a lie the
			// kitchen then has to explain.
			if item.PriceCents > 0 {
				dish["offers"] = map[string]any{
					"@type":         "Offer",
					"price":         dollars(item.PriceCents),
					"priceCurrency": "USD",
				}
			}
			// The same three claims the printed menu shows an icon for, in the
			// vocabulary a search engine reads. Only what was ticked: an
			// unmarked dish says nothing here either, exactly as it says
			// nothing on the page.
			if diets := suitableForDiet(item); len(diets) > 0 {
				dish["suitableForDiet"] = diets
			}
			items = append(items, dish)
		}
		course := map[string]any{
			"@type":       "MenuSection",
			"name":        section.Name,
			"hasMenuItem": items,
		}
		if section.Description != "" {
			course["description"] = section.Description
		}
		courses = append(courses, course)
	}
	if len(courses) == 0 {
		return nil
	}
	return map[string]any{"@type": "Menu", "hasMenuSection": courses}
}

// suitableForDiet maps the three flags on a dish to schema.org's RestrictedDiet
// vocabulary, which is a fixed set of URLs rather than free text.
//
// Vegan does not imply vegetarian here, for the same reason the column does not:
// this reports what the kitchen ticked and nothing it did not, and a dish marked
// only vegan is a true statement that needs no help from us.
func suitableForDiet(item console.MenuItem) []any {
	var out []any
	if item.GlutenFree {
		out = append(out, "https://schema.org/GlutenFreeDiet")
	}
	if item.Vegan {
		out = append(out, "https://schema.org/VeganDiet")
	}
	if item.Vegetarian {
		out = append(out, "https://schema.org/VegetarianDiet")
	}
	return out
}

func (s *siteMeta) events(ctx context.Context, meta headMeta) headMeta {
	meta.Title = title("Events")
	meta.Description = s.copyFor(ctx, "events")

	if s.ops == nil {
		return meta
	}
	list, err := s.ops.PublicEvents(ctx, console.Today())
	if err != nil {
		s.warn("events", err)
		return meta
	}

	for _, event := range list {
		// An event with no date is one the owner is still drafting the details
		// of. schema.org requires startDate on an Event, and a dated listing
		// with no date is the kind of thing that gets a whole site's structured
		// data ignored.
		if event.HappensOn == "" {
			continue
		}

		block := map[string]any{
			"@context":  "https://schema.org",
			"@type":     "Event",
			"name":      event.Title,
			"startDate": event.HappensOn,
			"location": map[string]any{
				"@type": "Place",
				"name":  innName,
				"address": map[string]any{
					"@type":           "PostalAddress",
					"addressLocality": innLocality,
					"addressRegion":   innRegion,
					"addressCountry":  innCountry,
				},
			},
		}
		if event.Description != "" {
			block["description"] = event.Description
		}
		if photos := s.absoluteEventPhotos(event.Photos); len(photos) > 0 {
			block["image"] = photos
			if meta.Image == "" {
				meta.Image = photos[0].(string)
			}
		}
		meta.LD = append(meta.LD, marshalLD(block))
	}
	return meta
}

func (s *siteMeta) localArea(ctx context.Context, meta headMeta) headMeta {
	meta.Title = title("Local area")
	meta.Description = s.copyFor(ctx, "local-area")
	return meta
}

// policyPage is indexable on purpose, unlike the booking flow it belongs to. A
// guest looking for the cancellation terms after they have booked should be
// able to find them, and being asked to agree to something unfindable is the
// thing this page exists to stop.
func (s *siteMeta) policyPage(ctx context.Context, meta headMeta) headMeta {
	meta.Title = title("Policies")
	meta.Description = s.copyFor(ctx, "policies")
	return meta
}

// copyFor is the owner's prose for a page, cut to a description's length.
//
// Empty when they have not written any, which emits no meta description at
// all. That is the same choice the page makes when it renders no paragraph, and
// it is the right one: an invented sentence about the food would sit in search
// results long after somebody stopped remembering it was invented.
func (s *siteMeta) copyFor(ctx context.Context, slug string) string {
	if s.ops == nil {
		return ""
	}
	page, err := s.ops.PageFor(ctx, slug)
	if err != nil {
		s.warn("page copy for "+slug, err)
		return ""
	}
	if !page.Written {
		return ""
	}
	// The body, not the heading: a heading is two or three words and a
	// description wants a sentence. Falls back to the heading for a page that
	// has one and no body.
	if summary := summarise(page.Body); summary != "" {
		return summary
	}
	return summarise(page.Heading)
}

// cards is the rooms index read model — the same one GET /api/rooms answers
// with, so the head and the page it is attached to describe the same seven
// rooms at the same prices.
func (s *siteMeta) cards(ctx context.Context) ([]RoomCard, bool) {
	if s.q == nil {
		return nil, false
	}
	cards, err := roomCards(ctx, s.q)
	if err != nil {
		s.warn("rooms", err)
		return nil, false
	}
	return cards, true
}

// leadPhoto is the first real photograph on the site, for a page that has no
// single subject of its own. Empty until the owner has uploaded one, which
// means no og:image rather than a placeholder line drawing in a link preview.
func (s *siteMeta) leadPhoto(cards []RoomCard) string {
	for _, card := range cards {
		if len(card.Photos) > 0 {
			return s.absolute(card.Photos[0].URL)
		}
	}
	return ""
}

func (s *siteMeta) absolutePhotos(photos []Photo) []any {
	out := make([]any, 0, len(photos))
	for _, p := range photos {
		if url := s.absolute(p.URL); url != "" {
			out = append(out, url)
		}
	}
	return out
}

func (s *siteMeta) absoluteEventPhotos(photos []console.Photo) []any {
	out := make([]any, 0, len(photos))
	for _, p := range photos {
		if url := s.absolute(p.Path); url != "" {
			out = append(out, url)
		}
	}
	return out
}

// absolute turns a stored path into a URL a crawler can fetch, and returns
// nothing at all when there is no origin to build one from.
//
// Empty rather than relative on purpose: og:image and the canonical link are
// both defined as absolute, and a relative value in either is not a smaller
// version of the right answer — it is a tag that gets ignored, or worse,
// resolved against whatever origin scraped the page.
func (s *siteMeta) absolute(path string) string {
	if s.siteURL == "" || path == "" {
		return ""
	}
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return strings.TrimSuffix(s.siteURL, "/") + path
}

func (s *siteMeta) warn(what string, err error) {
	// A page still has to render. Structured data is the part of the document
	// nobody can see, and failing the request over it would take the visible
	// page down with it.
	slog.Warn("serving a page without its "+what, "err", err)
}

// marshalLD serialises a block, escaping <, > and & into \u sequences on the
// way — which is what makes it safe inside a <script> element no matter what
// the owner typed into the console.
func marshalLD(block map[string]any) template.JS {
	encoded, err := json.Marshal(block)
	if err != nil {
		slog.Warn("dropping a JSON-LD block", "err", err)
		return ""
	}
	return template.JS(encoded)
}

// dollars renders integer cents for schema.org, which wants a decimal string.
// Integer division and a zero-padded remainder, so no float exists here either.
func dollars(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}

// summarise is the owner's prose cut down to a meta description.
//
// The first paragraph only — blank lines separate paragraphs in page copy, and
// a description that runs two of them together reads as one confused sentence.
// The cut lands on a word boundary, because a search engine truncating it again
// mid-word is the look this is meant to avoid.
func summarise(body string) string {
	paragraph := strings.TrimSpace(body)
	if cut := strings.Index(paragraph, "\n\n"); cut >= 0 {
		paragraph = strings.TrimSpace(paragraph[:cut])
	}
	paragraph = strings.Join(strings.Fields(paragraph), " ")

	if len([]rune(paragraph)) <= metaDescriptionLimit {
		return paragraph
	}

	runes := []rune(paragraph)[:metaDescriptionLimit]
	for i := len(runes) - 1; i > 0; i-- {
		if unicode.IsSpace(runes[i]) {
			return strings.TrimRight(string(runes[:i]), " ,;:—-") + "…"
		}
	}
	return string(runes) + "…"
}

// headTemplate is the injected block.
//
// html/template rather than string concatenation because every value in it is
// the owner's: a page heading typed on a phone reaches an HTML attribute here,
// and page copy is deliberately plain text with no markdown parser precisely so
// that there is no way to put markup on the public site from the console. This
// is the other half of that promise.
var headTemplate = template.Must(template.New("head").Parse(
	`<title>{{.Title}}</title>` +
		`{{with .Description}}<meta name="description" content="{{.}}">{{end}}` +
		`{{if .NoIndex}}<meta name="robots" content="noindex, nofollow">{{end}}` +
		`{{with .Canonical}}<link rel="canonical" href="{{.}}">{{end}}` +
		`<meta property="og:site_name" content="` + innName + `">` +
		`<meta property="og:type" content="website">` +
		`<meta property="og:title" content="{{.Title}}">` +
		`{{with .Description}}<meta property="og:description" content="{{.}}">{{end}}` +
		`{{with .Canonical}}<meta property="og:url" content="{{.}}">{{end}}` +
		`{{with .Image}}<meta property="og:image" content="{{.}}">` +
		`<meta name="twitter:card" content="summary_large_image">{{end}}` +
		`{{range .LD}}<script type="application/ld+json">{{.}}</script>{{end}}`,
))

func (m headMeta) render() ([]byte, error) {
	var out strings.Builder
	if err := headTemplate.Execute(&out, m); err != nil {
		return nil, err
	}
	return []byte(out.String()), nil
}

// canonicalPath normalises the request path before it is matched or published.
// A trailing slash and an escaped byte both address the same page, and a
// canonical link that disagrees with itself between two spellings of one
// address is worse than none.
func canonicalPath(raw string) string {
	if unescaped, err := url.PathUnescape(raw); err == nil {
		raw = unescaped
	}
	if raw != "/" {
		raw = strings.TrimSuffix(raw, "/")
	}
	if raw == "" {
		raw = "/"
	}
	return raw
}
