package console

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
	"bealhouse/internal/email"
	"bealhouse/internal/media"
)

// ---------------------------------------------------------------------------
// Rooms
// ---------------------------------------------------------------------------

// Photo is one image and the alt text without which it is unusable.
//
// Path is where the file sits, and this editor manages the reference rather
// than the bytes: the upload pipeline that generates AVIF and WebP variants
// (decision #16) is not built, so what the owner edits here is which files a
// room shows, in what order, described how. A room with no photos falls back to
// the placeholder SVG on the public side rather than to a broken image.
type Photo struct {
	Path string `json:"path"`
	Alt  string `json:"alt"`

	// The other sizes it is stored at, for srcset. Derived from Path, and here
	// as well as on the guest-side shapes because the events page renders these
	// rows directly.
	media.Ladder
}

// Bed is one bed in a room.
type Bed struct {
	Type     string `json:"type"`
	Count    int    `json:"count"`
	Location string `json:"location,omitempty"`
}

// RoomContent is a room as the content editor works on it.
type RoomContent struct {
	ID          int64  `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	View        string `json:"view,omitempty"`

	MaxOccupancy int      `json:"maxOccupancy"`
	Amenities    []string `json:"amenities"`

	// The accessibility honesty rule (decision #22) is a CHECK constraint: the
	// flag cannot be set without naming at least one specific feature. It is
	// enforced there rather than here on purpose — a promise a guest plans a
	// trip around should not depend on which form wrote the row — so saving a
	// flag with no features comes back as a refusal from the database, and the
	// screen says what the constraint means.
	IsAccessible          bool     `json:"isAccessible"`
	AccessibilityFeatures []string `json:"accessibilityFeatures"`

	IsPetFriendly bool  `json:"isPetFriendly"`
	PetFeeCents   int64 `json:"petFeeCents"`

	SortOrder int     `json:"sortOrder"`
	Photos    []Photo `json:"photos"`
	Beds      []Bed   `json:"beds"`
}

// Rooms loads every room with its photos and beds.
func (o *Ops) Rooms(ctx context.Context) ([]RoomContent, error) {
	rooms, err := o.q.ListRooms(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading rooms: %w", err)
	}

	ids := make([]int64, 0, len(rooms))
	for _, r := range rooms {
		ids = append(ids, r.ID)
	}

	photos, err := o.q.ListPhotosForRooms(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("console: loading photos: %w", err)
	}
	beds, err := o.q.ListBedsForRooms(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("console: loading beds: %w", err)
	}

	byRoom := map[int64][]Photo{}
	for _, p := range photos {
		byRoom[p.RoomID] = append(byRoom[p.RoomID],
			Photo{Path: p.Path, Alt: p.AltText, Ladder: media.Sources(p.Path)})
	}
	bedsByRoom := map[int64][]Bed{}
	for _, b := range beds {
		bedsByRoom[b.RoomID] = append(bedsByRoom[b.RoomID], Bed{
			Type:     b.BedType,
			Count:    int(b.Count),
			Location: b.Location,
		})
	}

	out := make([]RoomContent, 0, len(rooms))
	for _, r := range rooms {
		room := RoomContent{
			ID:                    r.ID,
			Slug:                  r.Slug,
			Name:                  r.Name,
			Description:           r.Description,
			MaxOccupancy:          int(r.MaxOccupancy),
			Amenities:             r.Amenities,
			IsAccessible:          r.IsAccessible,
			AccessibilityFeatures: r.AccessibilityFeatures,
			IsPetFriendly:         r.IsPetFriendly,
			PetFeeCents:           int64(r.PetFeeCents),
			SortOrder:             int(r.SortOrder),
			Photos:                byRoom[r.ID],
			Beds:                  bedsByRoom[r.ID],
		}
		if r.View != nil {
			room.View = *r.View
		}
		if room.Photos == nil {
			room.Photos = []Photo{}
		}
		if room.Beds == nil {
			room.Beds = []Bed{}
		}
		out = append(out, room)
	}
	return out, nil
}

// SaveRoom writes one room's content, its photos and its beds together.
//
// One transaction, because the photos and beds are replaced wholesale: an owner
// reordering a gallery and losing power halfway must not end up with a room
// showing three of its five pictures. Replacing rather than merging is what
// makes the order on screen and the order in the table the same thing —
// sort_order is the array index at save time and never a number anybody types.
func (o *Ops) SaveRoom(ctx context.Context, in RoomContent) error {
	if strings.TrimSpace(in.Name) == "" {
		return badf("the room needs a name")
	}
	if in.MaxOccupancy < 1 {
		return badf("a room sleeps at least one person")
	}
	if in.IsAccessible && len(in.AccessibilityFeatures) == 0 {
		return badf("a room marked accessible has to say what makes it accessible — step-free entry, a roll-in shower, grab bars. The promise is one a guest plans a trip around")
	}
	if !in.IsPetFriendly && in.PetFeeCents != 0 {
		return badf("a pet fee on a room that does not take pets could never be charged")
	}
	for _, p := range in.Photos {
		if strings.TrimSpace(p.Alt) == "" {
			return badf("every photo needs alt text; a picture with none is invisible to a screen reader")
		}
	}

	var view *string
	if v := strings.TrimSpace(in.View); v != "" {
		view = &v
	}

	return o.tx(ctx, func(q *db.Queries) error {
		n, err := q.UpdateRoom(ctx, db.UpdateRoomParams{
			ID:                    in.ID,
			Name:                  strings.TrimSpace(in.Name),
			Description:           in.Description,
			View:                  view,
			MaxOccupancy:          int32(in.MaxOccupancy),
			Amenities:             cleaned(in.Amenities),
			IsAccessible:          in.IsAccessible,
			AccessibilityFeatures: cleaned(in.AccessibilityFeatures),
			IsPetFriendly:         in.IsPetFriendly,
			PetFeeCents:           int32(in.PetFeeCents),
			SortOrder:             int32(in.SortOrder),
		})
		if err != nil {
			return fmt.Errorf("console: saving room %d: %w", in.ID, err)
		}
		if n == 0 {
			return ErrNotFound
		}

		if err := q.DeleteRoomPhotos(ctx, in.ID); err != nil {
			return fmt.Errorf("console: clearing photos: %w", err)
		}
		for i, p := range in.Photos {
			if strings.TrimSpace(p.Path) == "" {
				continue
			}
			if err := q.CreateRoomPhoto(ctx, db.CreateRoomPhotoParams{
				RoomID:    in.ID,
				Path:      strings.TrimSpace(p.Path),
				AltText:   strings.TrimSpace(p.Alt),
				SortOrder: int32(i),
			}); err != nil {
				return fmt.Errorf("console: saving a photo: %w", err)
			}
		}

		if err := q.DeleteRoomBeds(ctx, in.ID); err != nil {
			return fmt.Errorf("console: clearing beds: %w", err)
		}
		for _, b := range in.Beds {
			if b.Count < 1 {
				continue
			}
			if err := q.CreateRoomBed(ctx, db.CreateRoomBedParams{
				RoomID:   in.ID,
				BedType:  strings.TrimSpace(b.Type),
				Count:    int32(b.Count),
				Location: strings.TrimSpace(b.Location),
			}); err != nil {
				return fmt.Errorf("console: saving a bed: %w", err)
			}
		}
		return nil
	})
}

// cleaned drops blank entries from a list of tags the owner typed.
//
// An empty amenity renders as a bullet with nothing after it, and an empty
// accessibility feature would satisfy the honesty constraint's cardinality
// check while naming nothing at all — which is exactly the promise that
// constraint exists to prevent.
func cleaned(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

// Settings is the property-wide configuration screen.
//
// Both rates cross this boundary pre-scaled to hundred-thousandths, matching
// pricing.Rate and GetSettings exactly, so no numeric ever becomes a float64 on
// either side. The screen shows them as percentages and converts; nothing in
// between does.
type Settings struct {
	DefaultMinStay int `json:"defaultMinStay"`
	MaxStayNights  int `json:"maxStayNights"`

	TaxRateScaled              int64 `json:"taxRateScaled"`
	RefundProcessingRateScaled int64 `json:"refundProcessingRateScaled"`
	HoldTTLMinutes             int   `json:"holdTtlMinutes"`
	PaymentGraceMinutes        int   `json:"paymentGraceMinutes"`

	// HH:MM on the 24-hour clock going in, because that is unambiguous to parse;
	// the screen shows a time picker and the emails render it through
	// email.Clock.
	CheckinTime  string `json:"checkinTime"`
	CheckoutTime string `json:"checkoutTime"`

	AccessibilityNotice string `json:"accessibilityNotice"`
}

func (o *Ops) Settings(ctx context.Context) (Settings, error) {
	row, err := o.q.GetSettings(ctx)
	if err != nil {
		return Settings{}, fmt.Errorf("console: loading settings: %w", err)
	}
	return Settings{
		DefaultMinStay:             int(row.DefaultMinStay),
		MaxStayNights:              int(row.MaxStayNights),
		TaxRateScaled:              row.TaxRateScaled,
		RefundProcessingRateScaled: row.RefundProcessingRateScaled,
		HoldTTLMinutes:             int(row.HoldTtlMinutes),
		PaymentGraceMinutes:        int(row.PaymentGraceMinutes),
		CheckinTime:                hhmm(row.CheckinTime.Microseconds),
		CheckoutTime:               hhmm(row.CheckoutTime.Microseconds),
		AccessibilityNotice:        row.AccessibilityNotice,
	}, nil
}

// SaveSettings writes the configuration back.
//
// The CHECK constraints on the table are what refuse a nonsense value — a tax
// rate of 1, a maximum stay below the minimum — so a bad save is a database
// error rather than something quietly accepted. What is checked here is only
// what the database cannot say in a sentence the owner would understand.
func (o *Ops) SaveSettings(ctx context.Context, in Settings) error {
	checkin, err := timeOfDay(in.CheckinTime)
	if err != nil {
		return err
	}
	checkout, err := timeOfDay(in.CheckoutTime)
	if err != nil {
		return err
	}
	if in.DefaultMinStay < 1 {
		return badf("the minimum stay is at least one night")
	}
	if in.MaxStayNights < in.DefaultMinStay {
		return badf("the longest stay cannot be shorter than the shortest one")
	}
	if in.HoldTTLMinutes < 1 {
		return badf("a hold has to last at least a minute or nobody could finish paying")
	}
	if in.TaxRateScaled < 0 || in.TaxRateScaled >= 100000 {
		return badf("the tax rate has to be between 0 and 100 percent")
	}
	if in.RefundProcessingRateScaled < 0 || in.RefundProcessingRateScaled >= 100000 {
		return badf("the processing retention has to be between 0 and 100 percent")
	}

	err = o.q.UpdateSettings(ctx, db.UpdateSettingsParams{
		DefaultMinStay:             int32(in.DefaultMinStay),
		MaxStayNights:              int32(in.MaxStayNights),
		TaxRateScaled:              in.TaxRateScaled,
		RefundProcessingRateScaled: in.RefundProcessingRateScaled,
		HoldTtlMinutes:             int32(in.HoldTTLMinutes),
		PaymentGraceMinutes:        int32(in.PaymentGraceMinutes),
		CheckinTime:                checkin,
		CheckoutTime:               checkout,
		AccessibilityNotice:        in.AccessibilityNotice,
	})
	if err != nil {
		return fmt.Errorf("console: saving settings: %w", err)
	}
	return nil
}

// hhmm renders a time-of-day column for a form field, as distinct from clock(),
// which renders it for a person to read.
func hhmm(micros int64) string {
	d := time.Duration(micros) * time.Microsecond
	return fmt.Sprintf("%02d:%02d", int(d.Hours()), int(d.Minutes())%60)
}

// ---------------------------------------------------------------------------
// The menu (decision #12)
// ---------------------------------------------------------------------------

// MenuItem is one dish.
type MenuItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	// Zero means the item carries no price of its own — a market-price special,
	// or a side listed under a prix fixe — and the page renders nothing rather
	// than "$0.00".
	PriceCents int64 `json:"priceCents"`

	// Off rather than deleted, so tonight's sold-out dish keeps its description
	// and its place in the order for tomorrow.
	Available bool `json:"available"`

	// What the kitchen states the dish suits. False is "unmarked", never
	// "contains gluten" — the menu shows an icon for what was ticked and claims
	// nothing about what was not, because a guest with coeliac disease may act
	// on this and the safe failure is them asking.
	GlutenFree bool `json:"glutenFree"`
	Vegan      bool `json:"vegan"`
	Vegetarian bool `json:"vegetarian"`
}

// MenuSection is one course.
type MenuSection struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Items       []MenuItem `json:"items"`
}

// Menu loads the whole menu, unavailable items included. The public endpoint
// filters; the editor must not, or the owner cannot turn anything back on.
func (o *Ops) Menu(ctx context.Context) ([]MenuSection, error) {
	return o.menu(ctx, true)
}

// PublicMenu is what the restaurant page shows: available items only.
func (o *Ops) PublicMenu(ctx context.Context) ([]MenuSection, error) {
	return o.menu(ctx, false)
}

func (o *Ops) menu(ctx context.Context, includeUnavailable bool) ([]MenuSection, error) {
	sections, err := o.q.ListMenuSections(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading menu sections: %w", err)
	}
	items, err := o.q.ListMenuItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading menu items: %w", err)
	}

	bySection := map[int64][]MenuItem{}
	for _, i := range items {
		if !i.IsAvailable && !includeUnavailable {
			continue
		}
		bySection[i.SectionID] = append(bySection[i.SectionID], MenuItem{
			Name:        i.Name,
			Description: i.Description,
			PriceCents:  int64(i.PriceCents),
			Available:   i.IsAvailable,
			GlutenFree:  i.IsGlutenFree,
			Vegan:       i.IsVegan,
			Vegetarian:  i.IsVegetarian,
		})
	}

	out := make([]MenuSection, 0, len(sections))
	for _, s := range sections {
		section := MenuSection{Name: s.Name, Description: s.Description, Items: bySection[s.ID]}
		if section.Items == nil {
			section.Items = []MenuItem{}
		}
		out = append(out, section)
	}
	return out, nil
}

// SaveMenu replaces the menu wholesale, in one transaction.
//
// One document rather than per-row CRUD, because that is how a menu is edited:
// courses are reordered, dishes move between them and prices change across a
// whole section in a single sitting. Reconciling that as a stream of edits
// would be a diff algorithm on the client whose failure mode is a half-applied
// menu on the public site — and this way the failure mode is the previous menu,
// unchanged.
func (o *Ops) SaveMenu(ctx context.Context, sections []MenuSection) error {
	for _, s := range sections {
		if strings.TrimSpace(s.Name) == "" {
			return badf("every course needs a name")
		}
		for _, i := range s.Items {
			if strings.TrimSpace(i.Name) == "" {
				return badf("every dish in %q needs a name", s.Name)
			}
			if i.PriceCents < 0 {
				return badf("%q cannot cost less than nothing", i.Name)
			}
		}
	}

	return o.tx(ctx, func(q *db.Queries) error {
		// Items go with their section through ON DELETE CASCADE.
		if err := q.DeleteAllMenuSections(ctx); err != nil {
			return fmt.Errorf("console: clearing the menu: %w", err)
		}
		for si, s := range sections {
			id, err := q.CreateMenuSection(ctx, db.CreateMenuSectionParams{
				Name:        strings.TrimSpace(s.Name),
				Description: strings.TrimSpace(s.Description),
				SortOrder:   int32(si),
			})
			if err != nil {
				return fmt.Errorf("console: saving course %q: %w", s.Name, err)
			}
			for ii, i := range s.Items {
				if err := q.CreateMenuItem(ctx, db.CreateMenuItemParams{
					SectionID:    id,
					Name:         strings.TrimSpace(i.Name),
					Description:  strings.TrimSpace(i.Description),
					PriceCents:   int32(i.PriceCents),
					IsAvailable:  i.Available,
					IsGlutenFree: i.GlutenFree,
					IsVegan:      i.Vegan,
					IsVegetarian: i.Vegetarian,
					SortOrder:    int32(ii),
				}); err != nil {
					return fmt.Errorf("console: saving dish %q: %w", i.Name, err)
				}
			}
		}
		return nil
	})
}

// ---------------------------------------------------------------------------
// Events, the gallery, and the inbox
// ---------------------------------------------------------------------------

// Event is one thing happening at the inn.
type Event struct {
	Title       string  `json:"title"`
	HappensOn   string  `json:"happensOn,omitempty"`
	Description string  `json:"description"`
	Published   bool    `json:"published"`
	Photos      []Photo `json:"photos"`
}

// Events loads every event, drafts included.
func (o *Ops) Events(ctx context.Context) ([]Event, error) {
	rows, err := o.q.ListEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading events: %w", err)
	}
	photos, err := o.q.ListEventPhotos(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading event photos: %w", err)
	}

	byEvent := map[int64][]Photo{}
	for _, p := range photos {
		byEvent[p.EventID] = append(byEvent[p.EventID],
			Photo{Path: p.Path, Alt: p.AltText, Ladder: media.Sources(p.Path)})
	}

	out := make([]Event, 0, len(rows))
	for _, e := range rows {
		event := Event{
			Title:       e.Title,
			HappensOn:   day(e.HappensOn),
			Description: e.Description,
			Published:   e.IsPublished,
			Photos:      byEvent[e.ID],
		}
		if event.Photos == nil {
			event.Photos = []Photo{}
		}
		out = append(out, event)
	}
	return out, nil
}

// PublicEvents is what the events page shows: published, and not already past.
// A page listing last spring's wedding is a page that looks abandoned.
func (o *Ops) PublicEvents(ctx context.Context, on time.Time) ([]Event, error) {
	rows, err := o.q.ListPublishedEvents(ctx, dateOf(on))
	if err != nil {
		return nil, fmt.Errorf("console: loading events: %w", err)
	}
	photos, err := o.q.ListEventPhotos(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading event photos: %w", err)
	}

	byEvent := map[int64][]Photo{}
	for _, p := range photos {
		byEvent[p.EventID] = append(byEvent[p.EventID],
			Photo{Path: p.Path, Alt: p.AltText, Ladder: media.Sources(p.Path)})
	}

	out := make([]Event, 0, len(rows))
	for _, e := range rows {
		event := Event{
			Title:       e.Title,
			HappensOn:   day(e.HappensOn),
			Description: e.Description,
			Published:   true,
			Photos:      byEvent[e.ID],
		}
		if event.Photos == nil {
			event.Photos = []Photo{}
		}
		out = append(out, event)
	}
	return out, nil
}

// SaveEvents replaces the events list wholesale, on the same reasoning as the
// menu and with the same failure mode: the previous list, unchanged.
func (o *Ops) SaveEvents(ctx context.Context, events []Event) error {
	for _, e := range events {
		if strings.TrimSpace(e.Title) == "" {
			return badf("every event needs a title")
		}
		for _, p := range e.Photos {
			if strings.TrimSpace(p.Path) != "" && strings.TrimSpace(p.Alt) == "" {
				return badf("every photo needs alt text; a picture with none is invisible to a screen reader")
			}
		}
	}

	return o.tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteAllEvents(ctx); err != nil {
			return fmt.Errorf("console: clearing events: %w", err)
		}
		for ei, e := range events {
			when, err := optionalDay(e.HappensOn)
			if err != nil {
				return err
			}
			id, err := q.CreateEvent(ctx, db.CreateEventParams{
				Title:       strings.TrimSpace(e.Title),
				HappensOn:   when,
				Description: e.Description,
				IsPublished: e.Published,
				SortOrder:   int32(ei),
			})
			if err != nil {
				return fmt.Errorf("console: saving event %q: %w", e.Title, err)
			}
			for pi, p := range e.Photos {
				if strings.TrimSpace(p.Path) == "" {
					continue
				}
				if err := q.CreateEventPhoto(ctx, db.CreateEventPhotoParams{
					EventID:   id,
					Path:      strings.TrimSpace(p.Path),
					AltText:   strings.TrimSpace(p.Alt),
					SortOrder: int32(pi),
				}); err != nil {
					return fmt.Errorf("console: saving an event photo: %w", err)
				}
			}
		}
		return nil
	})
}

// Inquiry is one message from the public site: an events enquiry, or a note
// from the contact form on the home page.
type Inquiry struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Phone     string `json:"phone,omitempty"`
	EventDate string `json:"eventDate,omitempty"`
	PartySize int    `json:"partySize,omitempty"`
	Message   string `json:"message"`
	Status    string `json:"status"`

	// Which form wrote it: "event" or "contact". The console shows one inbox
	// and labels the rows, because the owner answers both the same way and two
	// screens would mean two places to forget to look.
	Kind string `json:"kind"`

	At time.Time `json:"at"`
}

// InquiryKinds are the forms that can write to the inbox.
const (
	KindEvent   = "event"
	KindContact = "contact"
)

// Inquiries lists the inbox. Empty status or kind means "all of them".
func (o *Ops) Inquiries(ctx context.Context, status, kind string, limit int) ([]Inquiry, error) {
	switch status {
	case "", "new", "contacted", "closed":
	default:
		return nil, badf("%q is not an inquiry status", status)
	}
	switch kind {
	case "", KindEvent, KindContact:
	default:
		return nil, badf("%q is not a kind of message", kind)
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	rows, err := o.q.ListEventInquiries(ctx, db.ListEventInquiriesParams{
		Status:   status,
		Kind:     kind,
		RowLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("console: loading inquiries: %w", err)
	}

	out := make([]Inquiry, 0, len(rows))
	for _, r := range rows {
		in := Inquiry{
			ID:        r.ID,
			Name:      r.Name,
			Email:     r.Email,
			Phone:     r.Phone,
			EventDate: day(r.EventDate),
			Message:   r.Message,
			Status:    r.Status,
			Kind:      r.Kind,
			At:        r.CreatedAt,
		}
		if r.PartySize != nil {
			in.PartySize = int(*r.PartySize)
		}
		out = append(out, in)
	}
	return out, nil
}

// NewInquiry is a submission from one of the two public forms — the events
// enquiry, or the contact box on the home page. These are the only writes an
// anonymous visitor performs on this site apart from creating a booking.
type NewInquiry struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	EventDate string `json:"eventDate"`
	PartySize int    `json:"partySize"`
	Message   string `json:"message"`

	// Which form it came from. Anything but "contact" is read as an events
	// enquiry, which is what every row was before the contact form existed —
	// so an old client that sends nothing still lands where it always did.
	Kind string `json:"kind"`
}

// SubmitInquiry records a message from the public site.
//
// It inserts and does nothing else: no email, no job, no side effect. Decision
// #11 puts event booking and deposits out of scope, so this is a message the
// owner answers, and the only thing the system owes it is not to lose it. The
// contact form is the same promise with a shorter form in front of it.
func (o *Ops) SubmitInquiry(ctx context.Context, in NewInquiry) error {
	name := strings.TrimSpace(in.Name)
	address := strings.TrimSpace(in.Email)
	if name == "" {
		return badf("please tell us your name")
	}
	// The same test booking uses, and for the same reason: the only check that
	// means anything is whether a reply arrives, and this catches the empty box
	// and the obvious typo.
	if !strings.Contains(address, "@") || strings.HasPrefix(address, "@") || strings.HasSuffix(address, "@") {
		return badf("please leave an email address we can reply to")
	}

	when, err := optionalDay(in.EventDate)
	if err != nil {
		return err
	}

	var party *int32
	if in.PartySize > 0 {
		n := int32(in.PartySize)
		party = &n
	}

	kind := KindEvent
	if in.Kind == KindContact {
		kind = KindContact
	}

	if _, err := o.q.CreateEventInquiry(ctx, db.CreateEventInquiryParams{
		Name:      name,
		Email:     address,
		Phone:     strings.TrimSpace(in.Phone),
		EventDate: when,
		PartySize: party,
		Message:   strings.TrimSpace(in.Message),
		Kind:      kind,
	}); err != nil {
		return fmt.Errorf("console: recording an inquiry: %w", err)
	}
	return nil
}

func (o *Ops) SetInquiryStatus(ctx context.Context, id int64, status string) error {
	switch status {
	case "new", "contacted", "closed":
	default:
		return badf("%q is not an inquiry status", status)
	}
	n, err := o.q.SetInquiryStatus(ctx, db.SetInquiryStatusParams{ID: id, Status: status})
	if err != nil {
		return fmt.Errorf("console: updating inquiry %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------------------------------------------------------------------------
// The prose on the public pages
// ---------------------------------------------------------------------------

// PageCopy is one public page's words.
//
// Plain text, not markdown or HTML: the owner is writing sentences about an
// inn, and a rich editor here would mean either a parser in the bundle or a way
// to put a <script> on the public site from a phone. Paragraphs are blank
// lines, and the page renders them as such.
type PageCopy struct {
	Slug    string `json:"slug"`
	Heading string `json:"heading"`
	Body    string `json:"body"`

	// Written is false when no row exists and the page is showing its structure
	// with nothing in the slot. The console has to say that out loud rather than
	// presenting an empty box as though it were a finished page.
	Written bool `json:"written"`

	// The page's photographs, from page_photos. A separate table and a separate
	// save, because pictures and prose are independent: the restaurant page has
	// had photographs and no sentences all year. Never nil — the front end maps
	// over it, and a null here is the crash room photos already caused once.
	Photos []Photo `json:"photos"`
}

// PageSlugs is which pages have an editable slot, and it is a property of the
// binary rather than of the table — exactly like email.Names(). A page added in
// a later release turns up in the editor on its own.
//
// "local-area" replaced "about": the owner's story moved onto the home page,
// where a visitor deciding whether to book actually reads it, and the standalone
// page became what the inn's current site calls Local Area — what there is to do
// in Littleton, which is the question a guest is really asking.
//
// "policies" is a slot on a page that mostly writes itself: the booking and
// refund rules there are read from settings and from pricing, so they cannot
// drift from what the code actually enforces. What this adds is the paragraphs
// only the owner can write — smoking, children, parking, quiet hours.
func PageSlugs() []string {
	return []string{"home", "rooms", "restaurant", "events", "local-area", "policies"}
}

// ---------------------------------------------------------------------------
// What is near the inn
// ---------------------------------------------------------------------------

// Attraction is one entry in the local-area page's nearby list.
type Attraction struct {
	Name string `json:"name"`
	// Free text — "walking distance" is the honest answer for half the list and
	// is not a number of minutes.
	Distance string `json:"distance"`
	// Empty means no link, and the page renders the name as plain text rather
	// than as an anchor going nowhere.
	URL string `json:"url"`
}

func (o *Ops) Attractions(ctx context.Context) ([]Attraction, error) {
	rows, err := o.q.ListLocalAttractions(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading local attractions: %w", err)
	}
	out := make([]Attraction, 0, len(rows))
	for _, r := range rows {
		a := Attraction{Name: r.Name, Distance: r.Distance}
		if r.Url != nil {
			a.URL = *r.Url
		}
		out = append(out, a)
	}
	return out, nil
}

// SaveAttractions replaces the list wholesale, in one transaction — the same
// whole-document save the menu and the galleries use.
func (o *Ops) SaveAttractions(ctx context.Context, list []Attraction) error {
	for _, a := range list {
		if strings.TrimSpace(a.Name) == "" {
			return badf("every entry needs a name")
		}
		// The database enforces this too, and that is the one that counts. Here
		// so the owner gets a sentence rather than a constraint violation.
		if url := strings.TrimSpace(a.URL); url != "" && !isHTTPURL(url) {
			return badf("the link for %q must start with http:// or https://", a.Name)
		}
	}

	return o.tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteLocalAttractions(ctx); err != nil {
			return err
		}
		for i, a := range list {
			var url *string
			if trimmed := strings.TrimSpace(a.URL); trimmed != "" {
				url = &trimmed
			}
			if err := q.CreateLocalAttraction(ctx, db.CreateLocalAttractionParams{
				Name:      strings.TrimSpace(a.Name),
				Distance:  strings.TrimSpace(a.Distance),
				Url:       url,
				SortOrder: int32(i),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func isHTTPURL(s string) bool {
	lower := strings.ToLower(s)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func (o *Ops) Copy(ctx context.Context) ([]PageCopy, error) {
	rows, err := o.q.ListPageCopy(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading page copy: %w", err)
	}

	written := make(map[string]db.PageCopy, len(rows))
	for _, r := range rows {
		written[r.Slug] = r
	}

	shots, err := o.q.ListPagePhotos(ctx)
	if err != nil {
		return nil, fmt.Errorf("console: loading page photos: %w", err)
	}
	bySlug := map[string][]Photo{}
	for _, s := range shots {
		bySlug[s.Slug] = append(bySlug[s.Slug],
			Photo{Path: s.Path, Alt: s.AltText, Ladder: media.Sources(s.Path)})
	}

	out := make([]PageCopy, 0, len(PageSlugs()))
	for _, slug := range PageSlugs() {
		page := PageCopy{Slug: slug, Photos: bySlug[slug]}
		if row, ok := written[slug]; ok {
			page.Heading, page.Body, page.Written = row.Heading, row.Body, true
		}
		if page.Photos == nil {
			page.Photos = []Photo{}
		}
		out = append(out, page)
	}
	return out, nil
}

// PageFor reads one page's copy and photographs for the public site. A missing
// row is not an error: it is a page with nothing written on it yet.
func (o *Ops) PageFor(ctx context.Context, slug string) (PageCopy, error) {
	page := PageCopy{Slug: slug, Photos: []Photo{}}

	row, err := o.q.GetPageCopy(ctx, slug)
	switch {
	case errors.Is(notFound(err), ErrNotFound):
		// Nothing written. The photographs below may still exist.
	case err != nil:
		return PageCopy{}, fmt.Errorf("console: loading copy for %q: %w", slug, err)
	default:
		page.Heading, page.Body, page.Written = row.Heading, row.Body, true
	}

	shots, err := o.q.ListPagePhotosFor(ctx, slug)
	if err != nil {
		return PageCopy{}, fmt.Errorf("console: loading photos for %q: %w", slug, err)
	}
	for _, s := range shots {
		page.Photos = append(page.Photos,
			Photo{Path: s.Path, Alt: s.AltText, Ladder: media.Sources(s.Path)})
	}
	return page, nil
}

// SavePagePhotos replaces a page's gallery wholesale, in one transaction.
//
// Separate from SaveCopy because the two are independent — emptying the prose
// is a DELETE of the page_copy row and must not take the photographs with it —
// and a whole-document save for the same reason the menu and a room's photos
// are: this is how a gallery is edited, and a half-applied one is on the public
// site.
func (o *Ops) SavePagePhotos(ctx context.Context, slug string, photos []Photo) error {
	if !known(slug, PageSlugs()) {
		return badf("there is no page called %q", slug)
	}
	for _, p := range photos {
		if strings.TrimSpace(p.Alt) == "" {
			return badf("every photograph needs alt text describing it")
		}
	}

	return o.tx(ctx, func(q *db.Queries) error {
		if err := q.DeletePagePhotos(ctx, slug); err != nil {
			return err
		}
		for i, p := range photos {
			if err := q.CreatePagePhoto(ctx, db.CreatePagePhotoParams{
				Slug:      slug,
				Path:      p.Path,
				AltText:   strings.TrimSpace(p.Alt),
				SortOrder: int32(i),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (o *Ops) SaveCopy(ctx context.Context, in PageCopy) error {
	if !known(in.Slug, PageSlugs()) {
		return badf("there is no page called %q", in.Slug)
	}

	// Emptying a page is the delete, the same way resetting an email template
	// is: no row is the absence of copy, and a row holding two empty strings
	// would be a second way to say the same thing.
	if strings.TrimSpace(in.Heading) == "" && strings.TrimSpace(in.Body) == "" {
		_, err := o.q.DeletePageCopy(ctx, in.Slug)
		if err != nil {
			return fmt.Errorf("console: clearing copy for %q: %w", in.Slug, err)
		}
		return nil
	}

	if err := o.q.UpsertPageCopy(ctx, db.UpsertPageCopyParams{
		Slug:    in.Slug,
		Heading: strings.TrimSpace(in.Heading),
		Body:    in.Body,
	}); err != nil {
		return fmt.Errorf("console: saving copy for %q: %w", in.Slug, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Email copy
// ---------------------------------------------------------------------------

// EmailCopy loads the seven messages, edited or not.
func (o *Ops) EmailCopy(ctx context.Context) ([]email.Copy, error) {
	if o.mail == nil {
		return nil, badf("email is not configured on this deployment")
	}
	return o.mail.Current(ctx)
}

// SaveEmailCopy writes one message's words.
//
// email.Parse first, always. Copy that will not compile fails at send time,
// which is after the guest's card has been charged and with nothing in front of
// the owner to connect it to the sentence they typed — so the refusal has to
// happen here, in the save, while they are still looking at it.
func (o *Ops) SaveEmailCopy(ctx context.Context, name, subject, body string) error {
	if !known(name, email.Names()) {
		return badf("there is no message called %q", name)
	}
	if strings.TrimSpace(subject) == "" {
		return badf("the message needs a subject line")
	}
	if strings.TrimSpace(body) == "" {
		return badf("the message needs a body. To put the shipped words back, reset it")
	}

	if _, err := email.Parse(name, subject, body); err != nil {
		return badf("that copy will not render: %s", err)
	}

	if err := o.q.UpsertEmailTemplate(ctx, db.UpsertEmailTemplateParams{
		Name:    name,
		Subject: subject,
		Body:    body,
	}); err != nil {
		return fmt.Errorf("console: saving copy for %q: %w", name, err)
	}
	return nil
}

// ResetEmailCopy puts a message back to what ships with the binary.
//
// A delete rather than a rewrite: the shipped copy lives in the repository, and
// copying it into the row here would give the owner a stale copy of a file that
// has since moved on.
func (o *Ops) ResetEmailCopy(ctx context.Context, name string) error {
	if !known(name, email.Names()) {
		return badf("there is no message called %q", name)
	}
	if _, err := o.q.DeleteEmailTemplate(ctx, name); err != nil {
		return fmt.Errorf("console: resetting copy for %q: %w", name, err)
	}
	return nil
}

func known(want string, in []string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}

// parseID reads an id that arrived as a JSON object key, where numbers are
// strings whether anybody meant them to be or not.
func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, badf("%q is not an id", s)
	}
	return id, nil
}

// Today is the civil date at the inn, re-exported so the HTTP layer resolves
// "today" the same way every date boundary in this system does rather than
// reaching for time.Now.
func Today() time.Time { return civil.Today() }
