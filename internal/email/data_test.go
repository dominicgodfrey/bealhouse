package email

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMoneyReadsTheWayAGuestWritesIt(t *testing.T) {
	for _, c := range []struct {
		cents int64
		want  string
	}{
		{0, "$0.00"},
		{5, "$0.05"},
		{50, "$0.50"},
		{100, "$1.00"},
		{99999, "$999.99"},
		{100000, "$1,000.00"},
		{123456789, "$1,234,567.89"},
		{-2500, "-$25.00"},
	} {
		if got := Money(c.cents); got != c.want {
			t.Errorf("Money(%d) = %q, want %q", c.cents, got, c.want)
		}
	}
}

func TestDayCarriesItsYear(t *testing.T) {
	got := Day(time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC))
	if want := "Thursday, October 1, 2026"; got != want {
		t.Errorf("Day = %q, want %q", got, want)
	}
}

// A payload is queued as a struct and rendered as whatever JSON gives back, so
// a template written against `.Data.Code` has to keep working across the jobs
// table. Tags that differ from the field names would break that silently, and
// only for messages that were in flight during a deploy.
func TestPayloadKeysSurviveTheQueue(t *testing.T) {
	in := BalanceWarningData{
		Code:      "BH-1234",
		GuestName: "Ada Lovelace",
		Amount:    "$412.50",
		ChargeOn:  "Thursday, October 1, 2026",
		Checkin:   "Thursday, October 8, 2026",
		Checkout:  "Saturday, October 10, 2026",
	}

	encoded, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("encoding: %v", err)
	}

	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	for _, key := range []string{"Code", "GuestName", "Amount", "ChargeOn", "Checkin", "Checkout"} {
		if _, ok := out[key]; !ok {
			t.Errorf("a template reading .Data.%s finds nothing after the round trip", key)
		}
	}

	// Nothing in a payload may be a number. Cents that became a float64 here
	// would be the one place in the system money is not an integer.
	for key, value := range out {
		if _, isNumber := value.(float64); isNumber {
			t.Errorf("%s came back as a number; amounts must be formatted before queueing", key)
		}
	}
}
