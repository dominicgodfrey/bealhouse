package pricing

import (
	"testing"
	"time"
)

// NH Meals & Rooms, the only rate the inn currently uses.
const nh = Rate(8500)

func TestCompute(t *testing.T) {
	tests := []struct {
		name string
		in   Input
		want Quote
	}{
		{
			// Rose Chamber at the $150 base rate, the shortest stay the
			// 2-night minimum allows.
			name: "two nights, no pet",
			in:   Input{NightlyCents: []int64{15000, 15000}, TaxRate: nh},
			want: Quote{
				Nights:            2,
				RoomSubtotalCents: 30000,
				TaxableCents:      30000,
				TaxCents:          2550,
				TotalCents:        32550,
				DepositCents:      16275,
				BalanceCents:      16275,
			},
		},
		{
			// Back Lavender with a pet. The fee is taxed with the room, and the
			// resulting total is odd, so the deposit takes the extra cent.
			name: "two nights with pet fee",
			in:   Input{NightlyCents: []int64{15000, 15000}, PetFeeCents: 5000, TaxRate: nh},
			want: Quote{
				Nights:            2,
				RoomSubtotalCents: 30000,
				PetFeeCents:       5000,
				TaxableCents:      35000,
				TaxCents:          2975,
				TotalCents:        37975,
				DepositCents:      18988,
				BalanceCents:      18987,
			},
		},
		{
			// Three nights is where a 50% deposit stops coinciding with one
			// night's rate: $244.13 rather than $162.75.
			name: "three nights",
			in:   Input{NightlyCents: []int64{15000, 15000, 15000}, TaxRate: nh},
			want: Quote{
				Nights:            3,
				RoomSubtotalCents: 45000,
				TaxableCents:      45000,
				TaxCents:          3825,
				TotalCents:        48825,
				DepositCents:      24413,
				BalanceCents:      24412,
			},
		},
		{
			// Mrs. Beal's Suite at the $200 base rate.
			name: "two nights at the higher base rate",
			in:   Input{NightlyCents: []int64{20000, 20000}, TaxRate: nh},
			want: Quote{
				Nights:            2,
				RoomSubtotalCents: 40000,
				TaxableCents:      40000,
				TaxCents:          3400,
				TotalCents:        43400,
				DepositCents:      21700,
				BalanceCents:      21700,
			},
		},
		{
			// Seasonal rates vary per night; the quote must not assume they are
			// uniform.
			name: "varying nightly rates",
			in:   Input{NightlyCents: []int64{15000, 22500, 30000}, TaxRate: nh},
			want: Quote{
				Nights:            3,
				RoomSubtotalCents: 67500,
				TaxableCents:      67500,
				TaxCents:          5738, // 5737.5, rounded half up
				TotalCents:        73238,
				DepositCents:      36619,
				BalanceCents:      36619,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Compute(tt.in)
			if got != tt.want {
				t.Errorf("Compute() mismatch\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

// A tax of exactly half a cent must round up, not truncate. $1.00 at 8.5% is
// 8.5 cents and must bill as 9.
func TestTaxRoundsHalfUp(t *testing.T) {
	if got := tax(100, nh); got != 9 {
		t.Errorf("tax(100) = %d, want 9", got)
	}
}

// deposit + balance must reconcile to the total for every possible total, or
// the inn either loses or double-charges a cent.
func TestDepositAndBalanceAlwaysReconcile(t *testing.T) {
	for total := int64(0); total <= 5000; total++ {
		deposit := halfRoundedUp(total)
		balance := total - deposit
		if deposit+balance != total {
			t.Fatalf("total %d: deposit %d + balance %d != total", total, deposit, balance)
		}
		if balance > deposit {
			t.Fatalf("total %d: balance %d exceeds deposit %d; rounding went the wrong way", total, balance, deposit)
		}
	}
}

func TestChargeAtBooking(t *testing.T) {
	q := Compute(Input{NightlyCents: []int64{15000, 15000}, TaxRate: nh})

	if got := q.ChargeAtBooking(false); got != q.DepositCents {
		t.Errorf("normal booking charges %d, want the deposit %d", got, q.DepositCents)
	}
	// Arriving inside the balance window leaves no time for the T-7 job.
	if got := q.ChargeAtBooking(true); got != q.TotalCents {
		t.Errorf("short-notice booking charges %d, want the full total %d", got, q.TotalCents)
	}
}

func TestRefund(t *testing.T) {
	// Three nights, so the deposit and one night's rate are not the same
	// number and a mistake cannot hide behind a coincidence.
	q := Compute(Input{NightlyCents: []int64{15000, 15000, 15000}, TaxRate: nh})

	tests := []struct {
		name string
		paid int64
		late bool
		want int64
	}{
		{"cancel on time after paying in full", q.TotalCents, false, q.TotalCents},
		{"cancel on time having paid only the deposit", q.DepositCents, false, q.DepositCents},
		{"cancel late after paying in full", q.TotalCents, true, q.BalanceCents},
		// The T-7 charge failed, so the guest paid only the deposit and the
		// penalty consumes all of it. The inn must not compute a negative
		// refund and try to collect it.
		{"cancel late having paid only the deposit", q.DepositCents, true, 0},
		{"cancel late having paid nothing", 0, true, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := q.Refund(tt.paid, tt.late); got != tt.want {
				t.Errorf("Refund(paid=%d, late=%v) = %d, want %d", tt.paid, tt.late, got, tt.want)
			}
		})
	}
}

// The stated policy is that a late cancellation forfeits half the stay, which
// is the same number as the deposit. This is what makes the ordinary case
// simple: refund the balance, keep the deposit.
func TestLatePenaltyEqualsDeposit(t *testing.T) {
	q := Compute(Input{NightlyCents: []int64{15000, 15000, 15000}, PetFeeCents: 5000, TaxRate: nh})

	if q.Penalty(true) != q.DepositCents {
		t.Errorf("late penalty %d != deposit %d", q.Penalty(true), q.DepositCents)
	}
	if q.Penalty(false) != 0 {
		t.Errorf("on-time penalty = %d, want 0", q.Penalty(false))
	}
}

// The short-notice boundary decides whether a guest is charged half or all of
// their stay, so it is worth pinning to the day. Decision #7 draws it at
// "arrival in fewer than 8 days".
func TestShortNoticeBoundary(t *testing.T) {
	today := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		daysOut int
		want    bool
	}{
		{0, true}, // arriving today
		{6, true},
		{7, true},  // T-7 is today: the job would have to have run already
		{8, false}, // the first arrival with a day to spare
		{9, false},
		{60, false},
	}

	for _, tt := range tests {
		checkin := today.AddDate(0, 0, tt.daysOut)
		if got := IsShortNotice(today, checkin); got != tt.want {
			t.Errorf("arrival in %d days: short notice = %v, want %v", tt.daysOut, got, tt.want)
		}
	}
}

func TestBalanceChargeDate(t *testing.T) {
	checkin := time.Date(2026, time.June, 15, 0, 0, 0, 0, time.UTC)
	want := time.Date(2026, time.June, 8, 0, 0, 0, 0, time.UTC)

	if got := BalanceChargeDate(checkin); !got.Equal(want) {
		t.Errorf("balance charges on %s, want %s", got.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}
