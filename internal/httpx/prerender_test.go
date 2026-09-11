package httpx

import (
	"context"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"bealhouse/internal/console"
)

// The served body, and the two files that tell a crawler what to do with it.
//
// What is worth asserting here is not that some HTML appears. It is the three
// ways this does damage if it is wrong: the booking flow gets a body and a
// crawler walks it into a hold, the body claims something the page does not, or
// a room the owner published is missing from the plain-text summary a model
// will answer questions from.
//
// Inside a rolled-back transaction like the rest of meta_test.go.

// A crawler that runs no JavaScript is most of the ones that matter now. Before
// this the document it got was an empty div: no name, no price, no links to
// anywhere else on the site.
func TestAPageServesItsContentWithoutJavaScript(t *testing.T) {
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
	body := afterRoot(t, doc)

	// Unescaped before comparing, for the same reason the head's test does it:
	// a room called "Mrs. Beal's Suite" arrives as `Mrs. Beal&#39;s Suite`,
	// which is the escaping working rather than a bug.
	if !strings.Contains(html.UnescapeString(body), card.Name) {
		t.Errorf("the served body does not name %q", card.Name)
	}
	// The same figure the card shows and the same one the head's Offer
	// publishes. A body quoting its own price is the whole failure this is
	// built to avoid.
	if card.FromCents != nil {
		if want := dollars(*card.FromCents); !strings.Contains(body, want) {
			t.Errorf("the served body does not carry the from price %q", want)
		}
	}

	// A reader with no JavaScript has no navigation unless the document
	// carries some, and without it the only way to any other page is the
	// sitemap.
	for _, path := range []string{"/rooms", "/restaurant", "/about", "/policies"} {
		if !strings.Contains(body, `href="`+path+`"`) {
			t.Errorf("the served body links nowhere to %s", path)
		}
	}
}

// The rooms index and the home page are where a crawler finds the seven room
// URLs at all.
func TestTheRoomsAreLinkedFromTheServedBody(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	cards, err := roomCards(context.Background(), m.q)
	if err != nil {
		t.Fatalf("loading the rooms: %v", err)
	}
	if len(cards) == 0 {
		t.Skip("no rooms seeded")
	}

	for _, path := range []string{"/", "/rooms"} {
		body := afterRoot(t, page(t, m, path))
		for _, card := range cards {
			if !strings.Contains(body, `href="/rooms/`+card.Slug+`"`) {
				t.Errorf("%s does not link to /rooms/%s", path, card.Slug)
			}
		}
	}
}

// Decision #29 from the other side. A GET under /book or /bookings ends in a
// hold, so these are Disallowed and marked noindex — and giving them a body
// full of links would be an invitation to walk them anyway.
func TestTheBookingFlowAndConsoleGetNoServedBody(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	for _, path := range []string{"/book/rose-chamber", "/bookings/BH-ABCDEF", "/admin", "/nowhere"} {
		if body := strings.TrimSpace(afterRoot(t, page(t, m, path))); body != "" {
			t.Errorf("%s served a body a crawler could walk:\n%s", path, body)
		}
	}
}

// The owner's word, not ours, and not markup. Page copy is plain text with no
// markdown parser precisely so the console cannot put a script on the public
// site; the head has always escaped it and now the body has to as well.
func TestTheOwnersWordsCannotEscapeTheServedBody(t *testing.T) {
	m, tx := meta(t, "https://bealhouse.test")

	const attack = `</div><script>alert("xss")</script><p x="`
	if err := m.ops.SaveCopy(context.Background(), console.PageCopy{
		Slug: "about", Heading: "About us", Body: attack,
	}); err != nil {
		t.Fatalf("saving page copy: %v", err)
	}
	_ = tx

	body := afterRoot(t, page(t, m, "/about"))
	if strings.Contains(body, "<script>") {
		t.Errorf("the owner's text reached the document as markup:\n%s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Errorf("the owner's text was not escaped into the body at all:\n%s", body)
	}
}

// One inn, not several businesses at one address. Every block that mentions the
// house points at the same @id rather than describing it again.
func TestEveryPageNamesTheSameInn(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	const id = "https://bealhouse.test/#inn"

	home := jsonLD(t, page(t, m, "/"))
	if len(home) == 0 {
		t.Fatal("the home page publishes no structured data")
	}
	if got := home[0]["@id"]; got != id {
		t.Errorf("the home page's business has @id %v, want %q", got, id)
	}
	if got := home[0]["@type"]; got != "BedAndBreakfast" {
		t.Errorf("the business is a %v; the narrower type is the useful one", got)
	}

	cards, err := roomCards(context.Background(), m.q)
	if err != nil || len(cards) == 0 {
		t.Skip("no rooms seeded")
	}
	for _, block := range jsonLD(t, page(t, m, "/rooms/"+cards[0].Slug)) {
		if block["@type"] != "HotelRoom" {
			continue
		}
		place, _ := block["containedInPlace"].(map[string]any)
		if place["@id"] != id {
			t.Errorf("the room sits in %v, want the inn at %q", place, id)
		}
		return
	}
	t.Error("the room page publishes no HotelRoom")
}

// The plain-text summary. Its whole value is being complete and true, so what
// is asserted is that every room the site sells is in it and that the facts
// come from settings rather than from a sentence somebody typed here.
func TestTheSummaryListsEveryRoomAndTheRealTerms(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	rec := httptest.NewRecorder()
	llmsTXT(m)(rec, httptest.NewRequest(http.MethodGet, "/llms.txt", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/llms.txt answered %d, want 200", rec.Code)
	}
	body := rec.Body.String()

	cards, err := roomCards(context.Background(), m.q)
	if err != nil {
		t.Fatalf("loading the rooms: %v", err)
	}
	for _, card := range cards {
		if !strings.Contains(body, "https://bealhouse.test/rooms/"+card.Slug) {
			t.Errorf("/llms.txt does not list %s", card.Slug)
		}
	}

	terms, ok := m.terms(context.Background())
	if !ok {
		t.Fatal("no settings")
	}
	for _, want := range []string{terms.CheckinTime, terms.CheckoutTime, terms.TaxRatePercent} {
		if !strings.Contains(body, want) {
			t.Errorf("/llms.txt does not carry %q from settings", want)
		}
	}
}

// The welcome is deliberate, and so is the one thing it withholds. A named
// per-crawler group would replace this one wholesale for that crawler, which is
// how a well-meant `Allow` hands somebody the booking flow.
func TestRobotsWelcomesCrawlersButNotIntoTheBookingFlow(t *testing.T) {
	rec := httptest.NewRecorder()
	robotsTXT("https://bealhouse.test")(rec, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "User-agent: *\nAllow: /\n") {
		t.Errorf("robots.txt does not welcome crawlers outright:\n%s", body)
	}
	for _, want := range []string{"Disallow: /admin", "Disallow: /book/", "Disallow: /bookings/", "Disallow: /api"} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt is missing %q", want)
		}
	}
	if !strings.Contains(body, "Sitemap: https://bealhouse.test/sitemap.xml") {
		t.Error("robots.txt does not point at the sitemap")
	}

	// Every rule line must be in the one group. A second `User-agent:` is a
	// second group, and the Disallows above would not apply to it.
	if n := strings.Count(body, "User-agent:"); n != 1 {
		t.Errorf("robots.txt has %d groups; the Disallow lines only bind the one they are in", n)
	}
}

// lastmod is the one hint a crawler acts on, and a generated sitemap's failure
// mode is stamping every page with "now" until the field means nothing.
func TestTheSitemapDatesPagesFromTheirOwnRows(t *testing.T) {
	m, _ := meta(t, "https://bealhouse.test")

	rec := httptest.NewRecorder()
	sitemapXML(m)(rec, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	body := rec.Body.String()

	if strings.Contains(body, "<priority>") || strings.Contains(body, "<changefreq>") {
		t.Error("the sitemap still carries fields Google ignores")
	}

	cards, err := roomCards(context.Background(), m.q)
	if err != nil || len(cards) == 0 {
		t.Skip("no rooms seeded")
	}
	want := w3cDate(cards[0].UpdatedAt)
	if want == "" {
		t.Skip("no room timestamps")
	}
	if !strings.Contains(body, "<lastmod>"+want+"</lastmod>") {
		t.Errorf("the sitemap does not date %s from its own row (%s)", cards[0].Slug, want)
	}
}

// afterRoot is the served body: everything the fallback wrote inside the
// element React mounts into.
func afterRoot(t *testing.T, doc string) string {
	t.Helper()

	at := strings.Index(doc, `<div id="root">`)
	if at < 0 {
		t.Fatal(`the document has no <div id="root">`)
	}
	rest := doc[at+len(`<div id="root">`):]
	if end := strings.LastIndex(rest, "</div>"); end >= 0 {
		rest = rest[:end]
	}
	return rest
}
