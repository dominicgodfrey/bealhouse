package httpx

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"bealhouse/internal/console"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/gateway"
	"bealhouse/internal/testdb"
)

// The server-rendered head (decision #3).
//
// What is worth asserting here is not that a title appears. It is the three
// ways this feature does damage if it is wrong: it publishes a sentence the
// owner never wrote, it puts the booking flow in a search index, or it becomes
// the one place on the public site where text typed into the console reaches
// the document unescaped.
//
// These run inside a rolled-back transaction, so they can write menu rows and
// page copy freely and leave the developer's database alone. Nothing here
// commits, so none of it needs testdb.Exclusive or a calendar window.

func meta(t *testing.T, siteURL string) (*siteMeta, pgx.Tx) {
	t.Helper()
	tx := testdb.Tx(t, testdb.Connect(t))
	return &siteMeta{
		q: db.New(tx),
		ops: console.New(tx, nil, console.Letterhead{},
			console.Processor{Gateway: gateway.Disabled{}}),
		siteURL: siteURL,
	}, tx
}

// page serves one address through the real SPA handler and returns the
// document, so what is asserted below is the bytes a crawler receives rather
// than a struct on the way there.
func page(t *testing.T, m *siteMeta, path string) string {
	t.Helper()

	rec := httptest.NewRecorder()
	serveSPA(testSPA(), m)(rec, httptest.NewRequest(http.MethodGet, path, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d, want 200", path, rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("GET %s served %q, want HTML", path, got)
	}
	return rec.Body.String()
}

var (
	ldBlock   = regexp.MustCompile(`(?s)<script type="application/ld\+json">(.*?)</script>`)
	titleTag  = regexp.MustCompile(`(?is)<title>(.*?)</title>`)
	descrTag  = regexp.MustCompile(`<meta name="description" content="([^"]*)"`)
	canonTag  = regexp.MustCompile(`<link rel="canonical" href="([^"]*)"`)
	robotsTag = regexp.MustCompile(`<meta name="robots" content="([^"]*)"`)
)

// jsonLD pulls the structured data back out and decodes it, which also proves
// it is valid JSON — a block that is not is worse than no block, because a
// search engine that fails to parse one is entitled to ignore the rest.
func jsonLD(t *testing.T, doc string) []map[string]any {
	t.Helper()

	var out []map[string]any
	for _, match := range ldBlock.FindAllStringSubmatch(doc, -1) {
		var block map[string]any
		if err := json.Unmarshal([]byte(match[1]), &block); err != nil {
			t.Fatalf("JSON-LD did not parse: %v\n%s", err, match[1])
		}
		out = append(out, block)
	}
	return out
}

func only(t *testing.T, re *regexp.Regexp, doc, what string) string {
	t.Helper()

	found := re.FindAllStringSubmatch(doc, -1)
	if len(found) == 0 {
		return ""
	}
	if len(found) > 1 {
		t.Fatalf("the document has %d %s tags; a crawler may read either", len(found), what)
	}
	return found[0][1]
}

// Every marketing page has to be distinguishable from the others, which is the
// entire problem a SPA has: one document, one title, seven room pages that a
// search engine cannot tell apart.
func TestEveryMarketingPageGetsItsOwnHead(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	seen := map[string]string{}
	for _, path := range []string{"/", "/rooms", "/restaurant", "/events", "/local-area"} {
		doc := page(t, m, path)

		title := only(t, titleTag, doc, "<title>")
		if title == "" {
			t.Errorf("%s has no title", path)
		}
		if before, ok := seen[title]; ok {
			t.Errorf("%s and %s share the title %q", before, path, title)
		}
		seen[title] = path

		if got, want := only(t, canonTag, doc, "canonical"), "https://bealhouse.test"+path; got != want {
			t.Errorf("%s canonical is %q, want %q", path, got, want)
		}
		if robots := only(t, robotsTag, doc, "robots"); robots != "" {
			t.Errorf("%s is marked %q; it is a page the inn wants found", path, robots)
		}
	}
}

// Vite's index.html ships a static <title>. Left in place every page would have
// two of them — the browser shows the first and a crawler may take either,
// which is the failure that looks fine in a browser and puts "Beal House" on
// all seven room results.
func TestTheShellsOwnTitleIsReplacedRatherThanJoined(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	doc := page(t, m, "/local-area")
	if n := strings.Count(strings.ToLower(doc), "<title>"); n != 1 {
		t.Fatalf("the document has %d <title> tags, want exactly 1", n)
	}
	if title := only(t, titleTag, doc, "<title>"); !strings.HasPrefix(title, "Local area") {
		t.Errorf("title is %q; the shell's static one survived", title)
	}
	// ...and the rest of the shell is untouched, or the SPA does not boot.
	if !strings.Contains(doc, `<div id="root">`) || !strings.Contains(doc, "<!doctype html") {
		t.Error("injecting the head damaged the document")
	}
}

// The console is private and the booking flow is one guest's transaction.
// Neither belongs in an index, and /book in particular is a page whose GET
// leads to a hold — a crawler walking it takes real rooms off sale.
func TestTheBookingFlowAndConsoleAreNotIndexed(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	for _, path := range []string{
		"/admin", "/admin/today", "/admin/bookings/ABC123",
		"/search", "/book/rose", "/bookings/ABC123", "/bookings/ABC123/pay",
		"/booking/ABC123", "/health",
	} {
		doc := page(t, m, path)

		if robots := only(t, robotsTag, doc, "robots"); !strings.Contains(robots, "noindex") {
			t.Errorf("%s is marked %q, want noindex", path, robots)
		}
		// No canonical either. A canonical link is a request to index *this*
		// address, which directly contradicts the line above.
		if canon := only(t, canonTag, doc, "canonical"); canon != "" {
			t.Errorf("%s published a canonical URL %q while asking not to be indexed", path, canon)
		}
		if blocks := jsonLD(t, doc); len(blocks) > 0 {
			t.Errorf("%s carries %d structured-data blocks; it describes nothing public", path, len(blocks))
		}
	}
}

// The rule the whole marketing site is built on, applied to the one place it is
// easiest to break: a page with nothing written renders no paragraph, so its
// description must be absent rather than empty. An empty description tag is not
// a smaller version of the right answer — it is a page telling a search engine
// it has nothing to say.
func TestAPageWithNoCopyPublishesNoDescription(t *testing.T) {
	m, tx := meta(t, "https://bealhouse.test")
	ctx := context.Background()

	// Whatever the developer's database happens to hold, this page has none.
	if _, err := tx.Exec(ctx, "DELETE FROM page_copy WHERE slug = 'local-area'"); err != nil {
		t.Fatalf("clearing the page: %v", err)
	}

	doc := page(t, m, "/local-area")
	if strings.Contains(doc, `name="description"`) {
		t.Error("a page nobody has written published a description")
	}

	// ...and the owner's words appear the moment there are some.
	if err := m.ops.SaveCopy(ctx, console.PageCopy{
		Slug: "local-area", Heading: "Our house", Body: "A short history.\n\nAnd a second paragraph.",
	}); err != nil {
		t.Fatalf("saving the copy: %v", err)
	}

	doc = page(t, m, "/local-area")
	if got := only(t, descrTag, doc, "description"); got != "A short history." {
		t.Errorf("description is %q, want the first paragraph alone", got)
	}
}

// The console is a phone, page copy is deliberately plain text with no markdown
// parser, and this is the one place that text becomes markup. If it can escape
// here, the console is a way to put a <script> on the public site — which is
// exactly what storing prose as plain text was meant to prevent.
func TestTheOwnersWordsCannotEscapeTheDocument(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")
	ctx := context.Background()

	const attack = `"><script>alert('xss')</script><p x="`

	if err := m.ops.SaveCopy(ctx, console.PageCopy{Slug: "restaurant", Body: attack}); err != nil {
		t.Fatalf("saving the copy: %v", err)
	}
	// The same string again as a dish, so it is tested on its way into JSON-LD
	// as well as into an attribute. These are different escapers and only one
	// of them being right is the interesting failure.
	if err := m.ops.SaveMenu(ctx, []console.MenuSection{{
		Name:  attack,
		Items: []console.MenuItem{{Name: attack, PriceCents: 1200, Available: true}},
	}}); err != nil {
		t.Fatalf("saving the menu: %v", err)
	}

	doc := page(t, m, "/restaurant")

	if strings.Contains(doc, "<script>alert") {
		t.Fatal("the owner's text reached the document as markup")
	}
	if strings.Count(strings.ToLower(doc), "</script>") != len(ldBlock.FindAllString(doc, -1)) {
		t.Fatal("a </script> in the owner's text closed a block early")
	}

	// It is still the text they typed once it is decoded — escaped, not eaten.
	blocks := jsonLD(t, doc)
	if len(blocks) != 1 {
		t.Fatalf("got %d structured-data blocks, want 1", len(blocks))
	}
	if !strings.Contains(string(mustJSON(t, blocks[0])), "alert") {
		t.Error("the dish name was dropped rather than escaped")
	}
}

// Absolute URLs need an origin. The email letterhead already follows this rule
// and it holds for the same reason here: a canonical or og:image built on a
// guessed origin points somewhere that is not this site.
func TestWithoutAnOriginThereAreNoAbsoluteURLs(t *testing.T) {
	m, _ := meta(t, "")

	doc := page(t, m, "/")
	for _, tag := range []string{"canonical", "og:url", "og:image"} {
		if strings.Contains(doc, tag) {
			t.Errorf("%s was published with no SITE_URL to build it on", tag)
		}
	}
	// The page still describes itself. Losing the origin costs the URLs, not
	// the title and not the structured data.
	if only(t, titleTag, doc, "<title>") == "" {
		t.Error("no title without an origin")
	}
	if len(jsonLD(t, doc)) == 0 {
		t.Error("no structured data without an origin")
	}
}

// A room page is the one somebody arrives at from a search engine, and the
// figures in its structured data are the figures it will be judged on: a price
// in a search result that the room page then contradicts is a complaint at the
// front desk.
func TestARoomPageDescribesThatRoomAtThePriceTheAPIQuotes(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	cards, err := roomCards(context.Background(), m.q)
	if err != nil {
		t.Fatalf("loading the rooms: %v", err)
	}
	if len(cards) == 0 {
		t.Skip("no rooms seeded")
	}
	card := cards[0]

	doc := page(t, m, "/rooms/"+card.Slug)

	// Unescaped before comparing, because the name reaches the document through
	// html/template: a room called "Mrs. Beal's Suite" arrives as
	// `Mrs. Beal&#39;s Suite`, which is the escaping working rather than a bug.
	if title := html.UnescapeString(only(t, titleTag, doc, "<title>")); !strings.Contains(title, card.Name) {
		t.Errorf("title is %q, want the room's name in it", title)
	}

	blocks := jsonLD(t, doc)
	if len(blocks) != 1 {
		t.Fatalf("got %d structured-data blocks, want 1", len(blocks))
	}
	room := blocks[0]
	if room["@type"] != "HotelRoom" {
		t.Errorf("@type is %v, want HotelRoom", room["@type"])
	}
	if room["name"] != card.Name {
		t.Errorf("name is %v, want %q", room["name"], card.Name)
	}

	// The offer and the card's "from" price agree, in both directions: a room
	// no season prices cannot be sold and must carry no offer at all.
	offer, hasOffer := room["offers"].(map[string]any)
	switch {
	case card.FromCents == nil && hasOffer:
		t.Error("a room with no rate published an offer")
	case card.FromCents != nil && !hasOffer:
		t.Error("a sellable room published no offer")
	case hasOffer:
		if got, want := offer["price"], dollars(*card.FromCents); got != want {
			t.Errorf("offer price is %v, want %q — the same figure the page shows", got, want)
		}
	}
}

// The SPA fallback has already committed to serving a document by the time the
// slug turns out to be nothing, so it cannot 404. Keeping it out of the index
// is the part that is still available.
func TestARoomThatDoesNotExistIsNotIndexed(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	doc := page(t, m, "/rooms/no-such-room")
	if robots := only(t, robotsTag, doc, "robots"); !strings.Contains(robots, "noindex") {
		t.Errorf("an unknown room is marked %q, want noindex", robots)
	}
	if len(jsonLD(t, doc)) != 0 {
		t.Error("an unknown room published structured data")
	}
}

// Decision #12's point: the same rows become the page and the structured menu,
// so the two cannot disagree. And a kitchen that has entered nothing publishes
// no Menu at all — an empty one says the restaurant serves nothing.
func TestTheRestaurantPublishesTheMenuItRenders(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")
	ctx := context.Background()

	if err := m.ops.SaveMenu(ctx, nil); err != nil {
		t.Fatalf("emptying the menu: %v", err)
	}
	if strings.Contains(page(t, m, "/restaurant"), "hasMenu") {
		t.Error("an empty menu was published as a Menu")
	}

	if err := m.ops.SaveMenu(ctx, []console.MenuSection{{
		Name: "Mains",
		Items: []console.MenuItem{
			{Name: "Trout", Description: "From the river", PriceCents: 2450, Available: true},
			{Name: "Market fish", PriceCents: 0, Available: true},
			{Name: "Sold out tonight", PriceCents: 1900, Available: false},
		},
	}}); err != nil {
		t.Fatalf("saving the menu: %v", err)
	}

	blocks := jsonLD(t, page(t, m, "/restaurant"))
	if len(blocks) != 1 {
		t.Fatalf("got %d structured-data blocks, want 1", len(blocks))
	}
	body := string(mustJSON(t, blocks[0]))

	if !strings.Contains(body, `"price":"24.50"`) {
		t.Errorf("the trout's price is not 24.50 in:\n%s", body)
	}
	// Zero is "no price of its own" — market price, or a side inside a set
	// menu — and an Offer of $0.00 is a lie the kitchen has to explain.
	if strings.Contains(body, `"price":"0.00"`) {
		t.Error("a dish with no price of its own was published at $0.00")
	}
	// The public endpoint filters sold-out dishes and this reads the same
	// model, so tonight's structured menu must not offer one either.
	if strings.Contains(body, "Sold out tonight") {
		t.Error("a dish the kitchen turned off was published to search engines")
	}
}

func TestTheSitemapListsEveryRoomAndTheSiteURLsAreAbsolute(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	rec := httptest.NewRecorder()
	sitemapXML(m)(rec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the sitemap answered %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	cards, err := roomCards(context.Background(), m.q)
	if err != nil {
		t.Fatalf("loading the rooms: %v", err)
	}
	for _, card := range cards {
		if want := "https://bealhouse.test/rooms/" + card.Slug; !strings.Contains(body, want) {
			t.Errorf("the sitemap does not list %s", want)
		}
	}
	for _, page := range []string{"/", "/rooms", "/restaurant", "/events", "/local-area"} {
		if !strings.Contains(body, "<loc>https://bealhouse.test"+page+"</loc>") {
			t.Errorf("the sitemap does not list %s", page)
		}
	}
	// Nothing a crawler is asked not to fetch should be advertised in the file
	// that exists to tell it what to fetch.
	for _, private := range []string{"/admin", "/book/", "/booking/"} {
		if strings.Contains(body, private) {
			t.Errorf("the sitemap advertises %s", private)
		}
	}
}

// A <loc> is defined as absolute. With no origin the honest answer is no
// sitemap, not a file full of relative locations — that one is rejected whole
// and reported as a problem somewhere else.
func TestThereIsNoSitemapWithoutAnOrigin(t *testing.T) {
	rec := httptest.NewRecorder()
	sitemapXML(&siteMeta{})(rec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("the sitemap answered %d with no SITE_URL, want 404", rec.Code)
	}
}

// /book and /bookings take a real room off sale for the hold TTL. A crawler
// walking them empties the inn quietly, which is decision #29's risk arriving
// through the front door rather than from an attacker.
func TestRobotsKeepsCrawlersOutOfTheBookingFlow(t *testing.T) {
	rec := httptest.NewRecorder()
	robotsTXT("https://bealhouse.test")(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	body := rec.Body.String()

	for _, want := range []string{
		"Disallow: /admin", "Disallow: /api", "Disallow: /book/", "Disallow: /bookings/",
		"Sitemap: https://bealhouse.test/sitemap.xml",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt is missing %q:\n%s", want, body)
		}
	}

	// And no sitemap line at all rather than a relative one.
	plain := httptest.NewRecorder()
	robotsTXT("")(plain, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if strings.Contains(plain.Body.String(), "Sitemap:") {
		t.Error("robots.txt pointed at a sitemap with no origin to reach it at")
	}
}

// robots.txt and sitemap.xml are on the root router ahead of the SPA fallback,
// for the same reason /media/* is: served by the fallback they would be a page
// of HTML answering 200, and a crawler parsing that as a rule set does
// something nobody predicted.
func TestTheCrawlerFilesAreNotTheSPA(t *testing.T) {
	h := router(t, false)

	for _, path := range []string{"/robots.txt", "/sitemap.xml"} {
		rec := get(t, h, http.MethodGet, path, nil)
		if strings.Contains(rec.Body.String(), "<!doctype html") {
			t.Errorf("%s was answered by the SPA", path)
		}
	}
}

// Money never becomes a float, here least of all: this is the only place in the
// codebase cents are rendered as a decimal, and it is read by machines.
func TestPricesAreRenderedFromIntegersExactly(t *testing.T) {
	for cents, want := range map[int64]string{
		0: "0.00", 1: "0.01", 9: "0.09", 10: "0.10", 99: "0.99",
		100: "1.00", 2450: "24.50", 99999: "999.99", 100000: "1000.00",
	} {
		if got := dollars(cents); got != want {
			t.Errorf("dollars(%d) = %q, want %q", cents, got, want)
		}
	}
}

func TestADescriptionIsCutOnAWordBoundary(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("elderflower ", 40))

	got := summarise(long)
	if len([]rune(got)) > metaDescriptionLimit+1 { // +1 for the ellipsis
		t.Errorf("description is %d runes, want at most %d", len([]rune(got)), metaDescriptionLimit)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated description does not say so: %q", got)
	}
	if strings.Contains(got, "elderflo…") || strings.HasSuffix(got, "elderflowe…") {
		t.Errorf("the cut landed mid-word: %q", got)
	}

	// Short prose is left exactly as it was written.
	if got := summarise("  Two rooms face the river.  "); got != "Two rooms face the river." {
		t.Errorf("summarise mangled a short line: %q", got)
	}
}

// One address, one canonical. A trailing slash and an escaped byte both reach
// the same page, and a canonical that disagrees with itself between two
// spellings splits the page's own ranking between them.
func TestOneAddressHasOneCanonicalSpelling(t *testing.T) {
	for raw, want := range map[string]string{
		"/rooms/": "/rooms", "/rooms": "/rooms", "/": "/", "": "/",
		"/rooms/rose%2Dchamber": "/rooms/rose-chamber",
	} {
		if got := canonicalPath(raw); got != want {
			t.Errorf("canonicalPath(%q) = %q, want %q", raw, got, want)
		}
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	return out
}
