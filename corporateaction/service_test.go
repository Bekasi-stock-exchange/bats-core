package corporateaction

import (
	"errors"
	"testing"

	"bekasi-automatic-trading-system/market"
)

func TestRestate(t *testing.T) {
	cases := []struct {
		name     string
		shares   int64
		from, to int64
		want     int64
	}{
		{"1:2 split doubles", 1000, 1, 2, 2000},
		{"1:5 split", 300, 1, 5, 1500},
		{"2:1 reverse halves", 1000, 2, 1, 500},
		{"10:1 reverse", 5000, 10, 1, 500},
		{"2:3 bonus adds one per two held", 1000, 2, 3, 1500},

		// A share is indivisible, so a ratio that does not divide evenly drops the
		// fraction. Rounding up would issue shares the action never authorised.
		{"2:3 bonus on an odd holding truncates", 1001, 2, 3, 1501},
		{"3:1 reverse on a non-multiple truncates", 1000, 3, 1, 333},
		{"a holding too small to survive a reverse split goes to zero", 2, 10, 1, 0},

		{"zero stays zero", 0, 1, 2, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := restate(tc.shares, tc.from, tc.to)
			if err != nil {
				t.Fatalf("restate(%d, %d, %d) errored: %v", tc.shares, tc.from, tc.to, err)
			}
			if got != tc.want {
				t.Errorf("restate(%d, %d:%d) = %d, want %d",
					tc.shares, tc.from, tc.to, got, tc.want)
			}
		})
	}
}

// The multiplication happens before the division, which is what keeps the result
// exact — but it also means a large holding can overflow on the way. That must be
// an error rather than a silently wrapped negative holding.
func TestRestateRejectsOverflow(t *testing.T) {
	_, err := restate(maxInt64/2, 1, 3)

	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("restate on an overflowing holding = %v, want a ValidationError", err)
	}
}

// A split must not change what a holding is worth. Shares are multiplied and the
// band anchor divided by the same ratio, so the product stays put — that is the
// property the reference adjustment exists to preserve, and getting its direction
// backwards would quadruple the notional instead of holding it.
func TestSplitPreservesNotionalValue(t *testing.T) {
	const shares, reference int64 = 1000, 800
	const from, to int64 = 1, 2

	after, err := restate(shares, from, to)
	if err != nil {
		t.Fatalf("restate: %v", err)
	}
	adjusted := reference * from / to

	if before, now := shares*reference, after*adjusted; before != now {
		t.Errorf("notional moved across the split: %d -> %d", before, now)
	}
}

func active(listed int64) market.Emiten {
	return market.Emiten{ID: 1, Kode: "BBCA", Nama: "Bank Central Asia", ListedShares: listed, IsActive: true}
}

// The terms must match the kind. Sending a ratio with a dividend, or an amount
// with a split, means the operator has misunderstood the request — accepting it
// silently would carry that misunderstanding through to execution, where it moves
// real balances.
func TestApplyTermsRejectsMismatchedTerms(t *testing.T) {
	cases := []struct {
		name  string
		jenis Jenis
		req   AnnounceRequest
	}{
		{"dividend with no amount", Dividend, AnnounceRequest{}},
		{"dividend carrying a ratio", Dividend, AnnounceRequest{Amount: 50, RatioFrom: 1, RatioTo: 2}},
		{"split with no ratio", Split, AnnounceRequest{}},
		{"split carrying an amount", Split, AnnounceRequest{RatioFrom: 1, RatioTo: 2, Amount: 50}},
		{"ratio with a zero leg", Split, AnnounceRequest{RatioFrom: 0, RatioTo: 2}},
		{"negative ratio", Split, AnnounceRequest{RatioFrom: -1, RatioTo: 2}},

		// The direction has to match the kind, or the announcement says one thing
		// and does another.
		{"split that shrinks the count", Split, AnnounceRequest{RatioFrom: 2, RatioTo: 1}},
		{"split with a 1:1 ratio changes nothing", Split, AnnounceRequest{RatioFrom: 1, RatioTo: 1}},
		{"reverse split that grows the count", ReverseSplit, AnnounceRequest{RatioFrom: 1, RatioTo: 2}},
		{"bonus that shrinks the count", Bonus, AnnounceRequest{RatioFrom: 3, RatioTo: 2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := Record{Jenis: tc.jenis}

			err := applyTerms(&rec, tc.req, active(40000))

			var invalid ValidationError
			if !errors.As(err, &invalid) {
				t.Fatalf("applyTerms = %v, want a ValidationError", err)
			}
		})
	}
}

func TestApplyTermsAcceptsWellFormedTerms(t *testing.T) {
	cases := []struct {
		name  string
		jenis Jenis
		req   AnnounceRequest
	}{
		{"1:2 split", Split, AnnounceRequest{RatioFrom: 1, RatioTo: 2}},
		{"2:1 reverse split", ReverseSplit, AnnounceRequest{RatioFrom: 2, RatioTo: 1}},
		{"2:3 bonus", Bonus, AnnounceRequest{RatioFrom: 2, RatioTo: 3}},
		{"dividend", Dividend, AnnounceRequest{Amount: 50}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := Record{Jenis: tc.jenis}

			if err := applyTerms(&rec, tc.req, active(40000)); err != nil {
				t.Fatalf("applyTerms = %v, want nil", err)
			}

			if tc.jenis == Dividend {
				if rec.Amount == nil || *rec.Amount != tc.req.Amount {
					t.Errorf("amount = %v, want %d", rec.Amount, tc.req.Amount)
				}
				// A dividend has no ratio, and nil is the honest representation:
				// a zero would read as a ratio set to something meaningless.
				if rec.RatioFrom != nil || rec.RatioTo != nil {
					t.Error("a dividend must carry no ratio")
				}
				return
			}

			if rec.RatioFrom == nil || rec.RatioTo == nil {
				t.Fatal("a share action must carry a ratio")
			}
			if *rec.RatioFrom != tc.req.RatioFrom || *rec.RatioTo != tc.req.RatioTo {
				t.Errorf("ratio = %d:%d, want %d:%d",
					*rec.RatioFrom, *rec.RatioTo, tc.req.RatioFrom, tc.req.RatioTo)
			}
			if rec.Amount != nil {
				t.Error("a share action must carry no amount")
			}
		})
	}
}

// The payout is amount × shares for every holder, so a large dividend over a
// large float must be refused at announcement rather than wrapping at execution,
// when the market is already waiting for it.
func TestApplyTermsRejectsOverflowingDividend(t *testing.T) {
	rec := Record{Jenis: Dividend}

	err := applyTerms(&rec, AnnounceRequest{Amount: maxInt64 / 2}, active(1000))

	var invalid ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("applyTerms = %v, want a ValidationError", err)
	}
}

func TestJenisAffectsShares(t *testing.T) {
	for _, j := range []Jenis{Split, ReverseSplit, Bonus} {
		if !j.AffectsShares() {
			t.Errorf("%s must restate share counts", j)
		}
	}
	// A dividend pays cash and restates nothing — the one kind that leaves every
	// holding exactly as it found it.
	if Dividend.AffectsShares() {
		t.Error("a dividend must not restate share counts")
	}
}

func TestJenisValid(t *testing.T) {
	for _, j := range []Jenis{Split, ReverseSplit, Bonus, Dividend} {
		if !j.Valid() {
			t.Errorf("%s must be a valid jenis", j)
		}
	}
	for _, j := range []Jenis{"", "split", "MERGER", "DIVIDEN"} {
		if j.Valid() {
			t.Errorf("%q must not be a valid jenis", j)
		}
	}
}
