package market

import "testing"

func TestNewBand(t *testing.T) {
	cases := []struct {
		name                   string
		reference, bps         int64
		wantFloor, wantCeiling int64
	}{
		{"30% on 190", 190, 3000, 133, 247},
		{"30% on 1000", 1000, 3000, 700, 1300},
		{"25% on 50", 50, 2500, 38, 62},

		// The division comes last, so the error is at most one rupiah rather
		// than compounding: 3 * 3000 / 10000 = 0, not 0.9 rounded somewhere.
		{"tiny reference truncates rather than compounding", 3, 3000, 3, 3},

		// A 100% band would put the floor at 0, which is not a cheap quote but a
		// broken one. Clamped so it cannot disagree with the exchange's own
		// minimum price rule.
		{"100% clamps the floor to 1", 100, 10000, 1, 200},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBand(tc.reference, tc.bps)

			if b.Floor != tc.wantFloor {
				t.Errorf("floor = %d, want %d", b.Floor, tc.wantFloor)
			}
			if b.Ceiling != tc.wantCeiling {
				t.Errorf("ceiling = %d, want %d", b.Ceiling, tc.wantCeiling)
			}
			if b.Reference != tc.reference {
				t.Errorf("reference = %d, want %d", b.Reference, tc.reference)
			}
		})
	}
}

// The band's edges are inclusive. An order exactly at the ceiling must trade —
// if it were rejected the ceiling would be a price nothing could ever reach, and
// the circuit breaker, which fires on reaching it, could never fire at all.
func TestBandEdgesAreInclusive(t *testing.T) {
	b := NewBand(190, 3000) // 133..247

	for _, price := range []int64{133, 190, 247} {
		if !b.Allows(price) {
			t.Errorf("Allows(%d) = false, want true — the edges are tradeable", price)
		}
	}
	for _, price := range []int64{132, 248, 1000} {
		if b.Allows(price) {
			t.Errorf("Allows(%d) = true, want false", price)
		}
	}
}

// AtLimit and !Allows are deliberately different predicates: one fires on
// reaching the edge, the other on passing it. Conflating them would make the
// breaker trip only on orders auto-rejection had already refused, which is to
// say never.
func TestAtLimitFiresOnTheEdgeNotBeyondIt(t *testing.T) {
	b := NewBand(190, 3000) // 133..247

	if !b.AtLimit(247) {
		t.Error("AtLimit(247) = false, want true — the ceiling is the trigger")
	}
	if !b.AtLimit(133) {
		t.Error("AtLimit(133) = false, want true — the floor is the trigger")
	}
	if b.AtLimit(246) || b.AtLimit(134) {
		t.Error("a price inside the band tripped the breaker")
	}
}
