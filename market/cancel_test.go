package market

import (
	"errors"
	"testing"
	"time"

	"bekasi-automatic-trading-system/engine"
)

// noPersist is a cancel callback that always commits.
func noPersist(*engine.Order) error { return nil }

// A cancelled sell leaves the book and gives its shares back, so the broker can
// immediately sell the same quantity again. Without the release the shares stay
// reserved against an order that no longer exists, and the broker is refused for
// insufficient shares while holding every one of them.
func TestCancelReleasesReservedShares(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 200, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}

	reg.mu.Lock()
	reserved := reg.pos.reserved[1][1]
	reg.mu.Unlock()
	if reserved != 10 {
		t.Fatalf("reserved = %d, want 10 committed to the resting sell", reserved)
	}

	res, err := reg.Cancel(1, 1, 1, noPersist)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if res.Order.Status != engine.Cancelled {
		t.Errorf("status = %q, want cancelled", res.Order.Status)
	}

	reg.mu.Lock()
	reserved = reg.pos.reserved[1][1]
	reg.mu.Unlock()
	if reserved != 0 {
		t.Errorf("reserved = %d after cancel, want 0 — the shares were never sold", reserved)
	}

	// The order must be gone from the book, not merely marked.
	state, _ := reg.Snapshot(1)
	if len(state.Asks) != 0 {
		t.Errorf("cancelled order still resting: %+v", state.Asks)
	}
}

// A cancelled buy gives its cash back, sized at the limit price it reserved.
func TestCancelReleasesReservedCash(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	before := reg.AvailableCash(1)
	if _, err := submit(reg, 1, 1, engine.Buy, 200, 10); err != nil {
		t.Fatalf("resting buy: %v", err)
	}
	if got := reg.AvailableCash(1); got != before-2000 {
		t.Fatalf("available cash = %d, want %d committed to the resting buy", got, before-2000)
	}

	if _, err := reg.Cancel(1, 1, 1, noPersist); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if got := reg.AvailableCash(1); got != before {
		t.Errorf("available cash = %d after cancel, want %d — nothing was ever spent", got, before)
	}
}

// Only the broker that placed an order may withdraw it. Cancelling another
// broker's resting liquidity is not a permission any participant has, and the
// refusal must leave the book untouched.
func TestCancelRejectsNonOwner(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 200, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}

	_, err := reg.Cancel(1, 1, 2, noPersist)
	if !errors.Is(err, ErrNotOrderOwner) {
		t.Fatalf("err = %v, want ErrNotOrderOwner", err)
	}

	state, _ := reg.Snapshot(1)
	if len(state.Asks) != 1 {
		t.Errorf("a refused cancel disturbed the book: %+v", state.Asks)
	}
}

// An order that is not resting — never existed, or already filled — cannot be
// withdrawn.
func TestCancelRejectsOrderThatIsNotResting(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := reg.Cancel(1, 999, 1, noPersist); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("unknown order: err = %v, want ErrOrderNotFound", err)
	}

	// Fill an order completely, then try to cancel it.
	if _, err := submit(reg, 1, 1, engine.Sell, 200, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}
	if _, err := submit(reg, 2, 2, engine.Buy, 200, 10); err != nil {
		t.Fatalf("filling buy: %v", err)
	}
	if _, err := reg.Cancel(1, 1, 1, noPersist); !errors.Is(err, ErrOrderNotFound) {
		t.Fatalf("filled order: err = %v, want ErrOrderNotFound", err)
	}
}

// A partial fill leaves the remainder resting, and cancelling releases only that
// remainder — the quantity that already traded is gone, not reserved, and giving
// it back would credit the seller shares it no longer owns.
func TestCancelReleasesOnlyTheUnfilledRemainder(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 200, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}
	if _, err := submit(reg, 2, 2, engine.Buy, 200, 4); err != nil {
		t.Fatalf("partial buy: %v", err)
	}

	res, err := reg.Cancel(1, 1, 1, noPersist)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if res.Order.Remaining != 6 {
		t.Errorf("remaining = %d, want 6 released", res.Order.Remaining)
	}

	reg.mu.Lock()
	reserved := reg.pos.reserved[1][1]
	reg.mu.Unlock()
	if reserved != 0 {
		t.Errorf("reserved = %d after cancel, want 0", reserved)
	}
}

// A failed persist must leave the book exactly as it was: the order back at its
// original price and Seq, and its reservation still committed. Otherwise a
// transient database error would strand an order that the database still calls
// open while the book has forgotten it.
func TestCancelUnwindsOnPersistFailure(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	// Two resting sells, so the restored order's position among its peers is
	// observable rather than trivially correct.
	if _, err := submit(reg, 1, 1, engine.Sell, 200, 10); err != nil {
		t.Fatalf("resting sell 1: %v", err)
	}
	if _, err := submit(reg, 2, 1, engine.Sell, 210, 5); err != nil {
		t.Fatalf("resting sell 2: %v", err)
	}
	before, _ := reg.Snapshot(1)

	boom := errors.New("database is down")
	if _, err := reg.Cancel(1, 1, 1, func(*engine.Order) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the persist error", err)
	}

	after, _ := reg.Snapshot(1)
	if len(after.Asks) != len(before.Asks) {
		t.Fatalf("asks = %d after failed cancel, want %d", len(after.Asks), len(before.Asks))
	}
	for i := range before.Asks {
		if after.Asks[i] != before.Asks[i] {
			t.Errorf("ask level %d = %+v, want %+v — the book must be unchanged",
				i, after.Asks[i], before.Asks[i])
		}
	}

	reg.mu.Lock()
	reserved := reg.pos.reserved[1][1]
	reg.mu.Unlock()
	if reserved != 15 {
		t.Errorf("reserved = %d, want 15 still committed — nothing was released", reserved)
	}

	// The order is still cancellable, which is the point of unwinding cleanly.
	if _, err := reg.Cancel(1, 1, 1, noPersist); err != nil {
		t.Errorf("retry after a failed cancel: %v", err)
	}
}

// A halt stops new orders from arriving; it must not trap the ones already
// resting. A broker unable to withdraw during a halt is exposed for the halt's
// whole duration to a reopening it cannot react to.
func TestCancelIsAllowedWhileHalted(t *testing.T) {
	reg, _, now := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 200, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}
	reg.HaltUntil(1, now.Add(2*time.Minute))

	// A new order is refused, confirming the halt is actually in force.
	if _, err := submit(reg, 2, 2, engine.Buy, 200, 10); !errors.Is(err, ErrEmitenHalted) {
		t.Fatalf("submit during halt: err = %v, want ErrEmitenHalted", err)
	}
	if _, err := reg.Cancel(1, 1, 1, noPersist); err != nil {
		t.Errorf("cancel during halt: %v, want it to be allowed", err)
	}
}
