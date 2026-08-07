package console

import (
	"context"
	"fmt"
	"strings"
	"time"

	"bealhouse/internal/civil"
	db "bealhouse/internal/db/gen"
)

// GuestCard is one person in the searchable history.
type GuestCard struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone,omitempty"`

	// Stays counts confirmed stays only. A guest who booked and cancelled has
	// stayed no times, and a "3 stays" that quietly included two abandoned holds
	// is a number worse than none — the owner greeting a first-time visitor as a
	// regular is the mistake it causes.
	Stays int64 `json:"stays"`

	// LifetimeCents is gross collected across those stays, which is decision
	// #25's figure: refunds are rows in the ledger, not subtractions, so this is
	// what the card was charged rather than what the inn kept.
	LifetimeCents int64 `json:"lifetimeCents"`

	LastCheckout string `json:"lastCheckout,omitempty"`
	Notes        int64  `json:"notes"`
}

// GuestSearch is how the owner looks somebody up: by whatever they remember.
type GuestSearch struct {
	Query  string
	RoomID int64
	From   string
	To     string
	Limit  int
}

// Guests searches the guest history.
func (o *Ops) Guests(ctx context.Context, in GuestSearch) ([]GuestCard, error) {
	from, err := optionalDay(in.From)
	if err != nil {
		return nil, err
	}
	to, err := optionalDay(in.To)
	if err != nil {
		return nil, err
	}

	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}

	rows, err := o.q.SearchGuests(ctx, db.SearchGuestsParams{
		Query:    strings.TrimSpace(in.Query),
		RoomID:   in.RoomID,
		FromDate: from,
		ToDate:   to,
		RowLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("console: searching guests: %w", err)
	}

	out := make([]GuestCard, 0, len(rows))
	for _, r := range rows {
		out = append(out, GuestCard{
			ID:            r.ID,
			Name:          r.Name,
			Email:         r.Email,
			Phone:         r.Phone,
			Stays:         r.Stays,
			LifetimeCents: r.LifetimeCents,
			LastCheckout:  day(r.LastCheckout),
			Notes:         r.Notes,
		})
	}
	return out, nil
}

// Note is something one of the owners wrote about a guest.
type Note struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`

	// Author is empty when the user who wrote it has since been removed. The
	// column is ON DELETE SET NULL rather than CASCADE precisely so the note
	// survives that: what somebody remembered about a guest who is still coming
	// back must not be deleted because a phone was struck off.
	Author string `json:"author,omitempty"`

	At time.Time `json:"at"`
}

// GuestFile is everything the console knows about one person.
type GuestFile struct {
	Guest    GuestCard `json:"guest"`
	Bookings []Stay    `json:"bookings"`
	Notes    []Note    `json:"notes"`
}

// Guest opens one person's file: who they are, every stay they have had, and
// what the owners have written down.
func (o *Ops) Guest(ctx context.Context, id int64) (GuestFile, error) {
	row, err := o.q.GetGuest(ctx, id)
	if err != nil {
		return GuestFile{}, notFound(fmt.Errorf("console: loading guest %d: %w", id, err))
	}

	stays, err := o.q.ListGuestBookings(ctx, id)
	if err != nil {
		return GuestFile{}, fmt.Errorf("console: loading guest %d's stays: %w", id, err)
	}

	notes, err := o.q.ListGuestNotes(ctx, id)
	if err != nil {
		return GuestFile{}, fmt.Errorf("console: loading guest %d's notes: %w", id, err)
	}

	out := GuestFile{
		Guest: GuestCard{
			ID:    row.ID,
			Name:  row.Name,
			Email: row.Email,
			Phone: row.Phone,
		},
		Bookings: make([]Stay, 0, len(stays)),
		Notes:    make([]Note, 0, len(notes)),
	}

	for _, s := range stays {
		out.Bookings = append(out.Bookings, Stay{
			Code:             s.Code,
			Status:           s.Status,
			Checkin:          day(s.Checkin),
			Checkout:         day(s.Checkout),
			Nights:           civil.Nights(s.Checkin.Time, s.Checkout.Time),
			Guests:           int(s.Guests),
			Rooms:            s.RoomNames,
			GuestID:          row.ID,
			GuestName:        row.Name,
			GuestEmail:       row.Email,
			TotalCents:       s.TotalCents,
			PaidCents:        s.AmountPaidCents,
			OutstandingCents: outstanding(s.TotalCents, s.AmountPaidCents),
		})
		if s.Status == "confirmed" {
			out.Guest.Stays++
			out.Guest.LifetimeCents += s.AmountPaidCents
		}
	}

	for _, n := range notes {
		out.Notes = append(out.Notes, Note{
			ID:     n.ID,
			Body:   n.Body,
			Author: n.Author,
			At:     n.CreatedAt,
		})
	}
	out.Guest.Notes = int64(len(out.Notes))

	return out, nil
}

// AddNote writes something down about a guest.
//
// The author is the signed-in user resolved by the session middleware, never a
// field in the request body. There is one account today, so it changes nothing
// in practice — and it is worth getting right now rather than after a second
// user exists and every note in the table is attributed to whoever the browser
// claimed to be.
func (o *Ops) AddNote(ctx context.Context, guestID, authorID int64, body string) (Note, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return Note{}, badf("the note is empty")
	}

	// Checked before the insert so an unknown guest is "no such guest" rather
	// than a foreign key violation the owner would see as "something went
	// wrong".
	if _, err := o.q.GetGuest(ctx, guestID); err != nil {
		return Note{}, notFound(fmt.Errorf("console: loading guest %d: %w", guestID, err))
	}

	var author *int64
	if authorID != 0 {
		author = &authorID
	}

	row, err := o.q.CreateGuestNote(ctx, db.CreateGuestNoteParams{
		GuestID:      guestID,
		AuthorUserID: author,
		Body:         body,
	})
	if err != nil {
		return Note{}, fmt.Errorf("console: saving a note on guest %d: %w", guestID, err)
	}
	return Note{ID: row.ID, Body: body, At: row.CreatedAt}, nil
}

// DeleteNote removes one note.
//
// Both ids go into the WHERE clause, so a mistyped path cannot delete a note
// from somebody else's record.
func (o *Ops) DeleteNote(ctx context.Context, guestID, noteID int64) error {
	n, err := o.q.DeleteGuestNote(ctx, db.DeleteGuestNoteParams{ID: noteID, GuestID: guestID})
	if err != nil {
		return fmt.Errorf("console: deleting note %d: %w", noteID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
