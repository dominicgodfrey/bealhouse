package httpx

import (
	"html/template"
	"strconv"
	"strings"
)

// The document's body, written by the server, for the readers that never run
// the bundle.
//
// meta.go solves half of decision #3: a crawler that does not execute
// JavaScript gets this page's title, description and structured data. What it
// still got for the body was `<div id="root"></div>` — nothing to read, nothing
// to quote, and no links to anywhere else on the site. Google renders the SPA
// and never noticed. The crawlers behind the answer boxes and the assistants do
// not: GPTBot, ClaudeBot, PerplexityBot and the rest fetch HTML and read it as
// it arrives. To all of them this inn had metadata and no content.
//
// Three rules, and they are meta.go's rules because this is the same job:
//
//   - **It reimplements no read model, and it issues no query of its own.** Every
//     page below is filled from the rows the head was just built from, in the
//     same pass. A second query assembling the same page slightly differently is
//     how the document a crawler reads ends up quoting a price the page does not
//     show, and it would also double the database work on every document served.
//   - **Nothing is invented.** A page with no copy renders its structure and no
//     paragraph, exactly as the React page does. The owner's `PLACEHOLDER`
//     description is reproduced rather than hidden; the fix for that is the owner
//     writing a description.
//   - **Nothing here may fail a request.** It is built from data the head already
//     had, so there is nothing left to fail, but a page this does not recognise
//     renders no body rather than an error.
//
// This markup goes *inside* `<div id="root">`, so React clears it on its first
// render. That is deliberate and it is the reason this is not a `<noscript>`
// block: text-extraction pipelines strip `<noscript>` along with `<script>`
// often enough that it would have been an unknown fraction of the audience this
// exists for. Inside the root element it is ordinary content to every reader,
// and a visitor on a slow connection sees the page's words before the bundle
// arrives rather than a blank screen.

// bodyDoc is one page as plain HTML: a heading, some prose, and sections of
// listed things. Every page fits it, which is the point — one small template to
// keep correct rather than eight, and no page can grow a layout that has to be
// kept in step with the React one.
type bodyDoc struct {
	Heading  string
	Paras    []string
	Sections []bodySection
}

type bodySection struct {
	Heading string
	Paras   []string
	Items   []bodyItem
}

// bodyItem is a room, a dish, an event, a place nearby, or a policy: a name,
// optionally a link, a line of facts about it, and a sentence.
type bodyItem struct {
	Name string
	URL  string
	Meta string
	Text string
}

func (d *bodyDoc) section(heading string, items []bodyItem) {
	if len(items) == 0 {
		return
	}
	d.Sections = append(d.Sections, bodySection{Heading: heading, Items: items})
}

// paragraphs splits the owner's prose the way the pages do: plain text, blank
// lines between paragraphs, no markdown parser anywhere near it.
func paragraphs(body string) []string {
	var out []string
	for _, para := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n\n") {
		if para = strings.TrimSpace(para); para != "" {
			out = append(out, para)
		}
	}
	return out
}

// facts joins the short true things about a room or an event into one line:
// "Sleeps 3 · Street and mountain view · From $189.00 a night". Empty pieces
// drop out, so a room with no view and no price on the calendar gets neither a
// blank nor a stray separator.
func facts(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " · ")
}

// trimmed is one optional sentence as the template's list of paragraphs, and
// nothing at all when there is none.
func trimmed(s string) []string {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return []string{s}
}

// roomItems is the rooms index as list entries, from the same cards the head's
// ItemList is built from. Used by the rooms page and by the home page, which is
// where a crawler with no JavaScript finds the room URLs at all.
func roomItems(cards []RoomCard) []bodyItem {
	items := make([]bodyItem, 0, len(cards))
	for _, card := range cards {
		items = append(items, bodyItem{
			Name: card.Name,
			URL:  "/rooms/" + card.Slug,
			Meta: roomFacts(card),
			Text: card.Description,
		})
	}
	return items
}

// roomFacts is the line under a room's name. Sleeps, the view, and the cheapest
// night currently on the calendar — which is the same "from" figure the card
// shows, so the two cannot disagree. A room no season prices has none, because
// it cannot be sold at all.
func roomFacts(card RoomCard) string {
	var from string
	if card.FromCents != nil {
		from = "From $" + dollars(*card.FromCents) + " a night"
	}
	var sleeps string
	if card.MaxOccupancy > 0 {
		sleeps = "Sleeps " + strconv.Itoa(card.MaxOccupancy)
	}
	return facts(sleeps, card.View, from)
}

// bodyTemplate is the whole of it.
//
// html/template for the same reason meta.go uses it: every string in here is
// the owner's, typed into the console on a phone, and page copy is deliberately
// plain text with no markdown parser precisely so there is no way to put markup
// on the public site from there. This is that promise on the body as well as
// the head.
//
// The style block is five declarations and is not a second copy of the design
// system. It is here so the seconds before the bundle arrives look like a plain
// page of this inn's rather than Times New Roman on white: the paper and ink
// from index.css, and the same fallback stack that renders under Karla during
// `swap` anyway. Anything more would be a visual identity to keep in step.
var bodyTemplate = template.Must(template.New("body").Parse(
	`<style>` +
		`.bh-pre{max-width:44rem;margin:0 auto;padding:2rem 1.25rem;` +
		`background:#f7f1e8;color:#33201c;` +
		`font-family:Optima,Candara,'Gill Sans','Segoe UI',ui-sans-serif,system-ui,sans-serif;line-height:1.6}` +
		`.bh-pre h1,.bh-pre h2,.bh-pre h3{font-family:Georgia,'Palatino Linotype',serif;font-weight:600}` +
		`.bh-pre a{color:inherit}` +
		`.bh-pre ul{list-style:none;padding:0}` +
		`</style>` +
		`<div class="bh-pre">` +
		`<header><p><a href="/">` + innName + `</a></p><nav>` +
		`<a href="/rooms">Rooms</a> · <a href="/restaurant">Restaurant</a> · ` +
		`<a href="/events">Events</a> · <a href="/local-area">Local area</a> · ` +
		`<a href="/about">About us</a> · <a href="/policies">Policies</a>` +
		`</nav></header>` +
		`<main>` +
		`{{with .Heading}}<h1>{{.}}</h1>{{end}}` +
		`{{range .Paras}}<p>{{.}}</p>{{end}}` +
		`{{range .Sections}}<section>` +
		`{{with .Heading}}<h2>{{.}}</h2>{{end}}` +
		`{{range .Paras}}<p>{{.}}</p>{{end}}` +
		`{{if .Items}}<ul>{{range .Items}}<li>` +
		`<h3>{{if .URL}}<a href="{{.URL}}">{{.Name}}</a>{{else}}{{.Name}}{{end}}</h3>` +
		`{{with .Meta}}<p>{{.}}</p>{{end}}` +
		`{{with .Text}}<p>{{.}}</p>{{end}}` +
		`</li>{{end}}</ul>{{end}}` +
		`</section>{{end}}` +
		`</main>` +
		`<footer><address>` + innName + `, ` + innStreet + `, ` + innLocality + `, ` +
		innRegion + ` ` + innPostal + `<br>` +
		`<a href="tel:` + innPhone + `">` + innPhone + `</a> · ` +
		`<a href="mailto:` + innEmail + `">` + innEmail + `</a>` +
		`</address></footer>` +
		`</div>`,
))

// renderBody is the page's content as HTML, or nothing.
//
// Nothing is the right answer for every route meta.go marks noindex: the
// console, a guest's own booking and the booking flow, none of which a crawler
// should be reading and the last of which takes a real room off sale for the
// hold TTL on the way past (decision #29).
func (m headMeta) renderBody() []byte {
	if m.Doc == nil {
		return nil
	}
	var out strings.Builder
	if err := bodyTemplate.Execute(&out, m.Doc); err != nil {
		// Same rule as the head: the page is what the visitor came for.
		return nil
	}
	return []byte(out.String())
}
