// Package civil handles the inn's local calendar.
//
// Check-in and check-out are civil dates, not instants. "Is it T-7 yet" and
// "what is today" resolve in America/New_York regardless of where the server
// runs or what the guest's browser thinks, because a night at the inn is a
// night at the inn.
package civil

import (
	"time"
	_ "time/tzdata" // embedded so behaviour never depends on the host's zone files
)

var inn = mustLoad("America/New_York")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic("civil: loading " + name + ": " + err.Error())
	}
	return loc
}

// Location is the inn's timezone.
func Location() *time.Location { return inn }

// Date builds a civil date. Civil dates are represented as midnight UTC so they
// survive the round trip through Postgres' date type unchanged.
func Date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// Today is the current date at the inn.
func Today() time.Time {
	now := time.Now().In(inn)
	return Date(now.Year(), now.Month(), now.Day())
}

// AddDays returns d shifted by n days.
func AddDays(d time.Time, n int) time.Time {
	return d.AddDate(0, 0, n)
}

// AddMonths returns d shifted by n months.
func AddMonths(d time.Time, n int) time.Time {
	return d.AddDate(0, n, 0)
}

// Nights counts the nights in a half-open [checkin, checkout) stay.
func Nights(checkin, checkout time.Time) int {
	return int(checkout.Sub(checkin).Hours() / 24)
}
