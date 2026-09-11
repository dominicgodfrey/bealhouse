package httpx

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"bealhouse/internal/console"
)

// /llms.txt — the inn, in one file, for a reader that arrived to answer a
// question rather than to browse.
//
// The convention (llmstxt.org) is a markdown document at the root: a title, a
// sentence, and linked sections. It is not honoured by any crawler as a matter
// of contract and may never be; what makes it worth the forty lines below is
// that it costs one route and no maintenance, and that the alternative for an
// assistant asked "what is there in Littleton" is to reconstruct this inn from
// seven HTML pages and hope.
//
// Every line of it is generated from the same read models the pages and the
// head are, so there is nothing here to keep in step and nothing that can say
// what the site does not. It is deliberately the *facts*: rooms and what they
// cost, the times, the tax, what is on, what is nearby. Prose the owner has
// written is included; prose nobody has written is absent, exactly as it is
// everywhere else.
//
// Registered on the root router ahead of the SPA fallback, for the reason
// robots.txt is: answered by the fallback this would be a page of HTML with a
// 200, which is worse than a 404 for anything trying to parse it.
func llmsTXT(meta *siteMeta) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(meta.llms(r.Context()))
	}
}

// llms assembles the document. Nothing in it may fail the request: a query that
// errors costs its section, the same rule the head follows.
func (s *siteMeta) llms(ctx context.Context) []byte {
	var b strings.Builder

	b.WriteString("# " + innName + "\n\n")
	b.WriteString("> A seven-room inn with a restaurant at " + innStreet + ", " +
		innLocality + ", " + innRegion + " " + innPostal + ", United States. " +
		"Rooms are booked directly with the inn.\n")

	if _, paras := s.copyFor(ctx, "home"); len(paras) > 0 {
		b.WriteString("\n" + strings.Join(paras, "\n\n") + "\n")
	}

	// Rooms, with the price each is actually on sale at today.
	if cards, ok := s.cards(ctx); ok && len(cards) > 0 {
		b.WriteString("\n## Rooms\n\n")
		for _, card := range cards {
			b.WriteString("- " + s.link(card.Name, "/rooms/"+card.Slug))
			if line := roomFacts(card); line != "" {
				b.WriteString(": " + line)
			}
			b.WriteString("\n")
			if desc := oneLine(card.Description); desc != "" {
				b.WriteString("  " + desc + "\n")
			}
		}
	}

	// The questions somebody actually asks, answered in numbers rather than in
	// a paragraph they would have to read to the end of.
	if terms, ok := s.terms(ctx); ok {
		b.WriteString("\n## Staying here\n\n")
		for _, fact := range [][2]string{
			{"Check-in", "from " + terms.CheckinTime},
			{"Check-out", "by " + terms.CheckoutTime},
			{"Shortest stay", nights(terms.MinStayNights)},
			{"Longest stay", nights(terms.MaxStayNights)},
			{"Deposit", fmt.Sprintf("%d%% of the total at booking; the balance is taken %d days before arrival",
				terms.DepositPercent, terms.BalanceLeadDays)},
			{"Free cancellation", fmt.Sprintf("up to %d days before arrival", terms.FreeCancellationLeadDays)},
			{"NH Meals & Rooms tax", terms.TaxRatePercent + "%, added to the room total"},
			{"Pets", "not accepted"},
		} {
			b.WriteString("- " + fact[0] + ": " + fact[1] + "\n")
		}
	}

	if s.ops != nil {
		s.llmsMenu(ctx, &b)
		s.llmsEvents(ctx, &b)
		s.llmsNearby(ctx, &b)
	}

	b.WriteString("\n## Pages\n\n")
	for _, page := range [][2]string{
		{"Rooms", "/rooms"},
		{"Restaurant", "/restaurant"},
		{"Events", "/events"},
		{"Local area", "/local-area"},
		{"About us, with the address and a map", "/about"},
		{"Policies, including cancellation and privacy", "/policies"},
	} {
		b.WriteString("- " + s.link(page[0], page[1]) + "\n")
	}

	b.WriteString("\n## Contact\n\n")
	b.WriteString("- Telephone: " + innPhone + "\n")
	b.WriteString("- Email: " + innEmail + "\n")
	b.WriteString("- Address: " + innStreet + ", " + innLocality + ", " + innRegion + " " + innPostal + "\n")

	return []byte(b.String())
}

func (s *siteMeta) llmsMenu(ctx context.Context, b *strings.Builder) {
	sections, err := s.ops.PublicMenu(ctx)
	if err != nil {
		s.warn("menu", err)
		return
	}
	// A menu nobody has entered says nothing, rather than saying the restaurant
	// serves nothing (decision #12).
	var written bool
	for _, course := range sections {
		if len(course.Items) == 0 {
			continue
		}
		if !written {
			b.WriteString("\n## The restaurant\n")
			written = true
		}
		b.WriteString("\n### " + course.Name + "\n\n")
		for _, item := range course.Items {
			b.WriteString("- " + item.Name)
			// Zero is a market-price special or a side inside a set menu.
			if item.PriceCents > 0 {
				b.WriteString(" ($" + dollars(item.PriceCents) + ")")
			}
			if desc := oneLine(item.Description); desc != "" {
				b.WriteString(": " + desc)
			}
			b.WriteString("\n")
		}
	}
}

func (s *siteMeta) llmsEvents(ctx context.Context, b *strings.Builder) {
	list, err := s.ops.PublicEvents(ctx, console.Today())
	if err != nil {
		s.warn("events", err)
		return
	}
	if len(list) == 0 {
		return
	}
	b.WriteString("\n## What is on\n\n")
	for _, event := range list {
		b.WriteString("- " + event.Title)
		if event.HappensOn != "" {
			b.WriteString(" (" + event.HappensOn + ")")
		}
		if desc := oneLine(event.Description); desc != "" {
			b.WriteString(": " + desc)
		}
		b.WriteString("\n")
	}
}

func (s *siteMeta) llmsNearby(ctx context.Context, b *strings.Builder) {
	places, err := s.ops.Attractions(ctx)
	if err != nil {
		s.warn("local attractions", err)
		return
	}
	if len(places) == 0 {
		return
	}
	b.WriteString("\n## Nearby\n\n")
	for _, place := range places {
		b.WriteString("- " + place.Name)
		if place.Distance != "" {
			b.WriteString(" (" + place.Distance + ")")
		}
		if desc := oneLine(place.Description); desc != "" {
			b.WriteString(": " + desc)
		}
		b.WriteString("\n")
	}
}

// link is a markdown link, absolute where there is an origin to make it
// absolute with and a path where there is not. Unlike a sitemap's <loc> a
// relative markdown link is still a usable link, so this degrades rather than
// disappearing.
func (s *siteMeta) link(text, path string) string {
	if abs := s.absolute(path); abs != "" {
		return "[" + text + "](" + abs + ")"
	}
	return "[" + text + "](" + path + ")"
}

// oneLine flattens the owner's prose onto a single line, because a markdown
// list item that contains a blank line is two list items.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
