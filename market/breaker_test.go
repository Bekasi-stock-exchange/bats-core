package market

import (
	"errors"
	"testing"
	"time"

	"bekasi-automatic-trading-system/engine"
)

type stubPolicy struct {
	bps      int64
	duration time.Duration
}

func (p stubPolicy) EmitenBandBPS() int64        { return p.bps }
func (p stubPolicy) HaltDuration() time.Duration { return p.duration }

type recordingObserver struct {
	halted  []haltTrigger
	resumed []int64
}

func (o *recordingObserver) Halted(emitenID, price, reference int64, until time.Time) {
	o.halted = append(o.halted, haltTrigger{
		emitenID: emitenID, price: price, reference: reference, until: until,
	})
}

func (o *recordingObserver) Resumed(emitenID int64) { o.resumed = append(o.resumed, emitenID) }

// Holds one emiten anchored at reference, with both brokers funded well past
// anything these tests spend, and a clock the test controls.
func testRegistry(t *testing.T, reference int64, policy BreakerPolicy) (*Registry, *recordingObserver, *time.Time) {
	t.Helper()

	ref := reference
	em := Emiten{ID: 1, Kode: "AAAA", IsActive: true}
	if reference > 0 {
		em.SessionReference = &ref
	}

	const funded = 1_000_000_000
	reg := NewRegistry(
		[]Emiten{em},
		[]Holding{{ParticipantID: 1, EmitenID: 1, AmountShared: funded},
			{ParticipantID: 2, EmitenID: 1, AmountShared: funded}},
		[]Wallet{{ParticipantID: 1, Balance: funded}, {ParticipantID: 2, Balance: funded}},
		engine.NewSequencer(0, 0),
	)

	now := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	reg.now = func() time.Time { return now }

	obs := &recordingObserver{}
	reg.WithBreaker(policy, obs)
	return reg, obs, &now
}

// Places one order, discarding the book state.
func submit(reg *Registry, id int64, participant int64, side engine.Side, price, qty int64) ([]engine.Trade, error) {
	o := &engine.Order{
		ID: id, EmitenID: 1, ParticipantID: participant,
		Side: side, Type: engine.Limit, Price: price, Qty: qty,
	}
	trades, _, err := reg.Submit(o, func([]engine.Trade) error { return nil })
	return trades, err
}

// The reported case: an instrument anchored at 190 must not be liftable to 1000.
// Auto-rejection refuses the order outright, so no trade ever prints at that
// price and nothing rests in the book asking for it.
func TestOrderFarOutsideBandIsRejected(t *testing.T) {
	reg, obs, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	_, err := submit(reg, 1, 1, engine.Sell, 1000, 10)

	if !errors.Is(err, ErrOutsideBand) {
		t.Fatalf("err = %v, want ErrOutsideBand — 1000 is +426%% on a 30%% band", err)
	}
	if len(obs.halted) != 0 {
		t.Error("a rejected order tripped the breaker; nothing executed to measure")
	}

	// Nothing may rest in the book at the refused price, or the next order would
	// match against a level auto-rejection was supposed to have prevented.
	state, _ := reg.Snapshot(1)
	if len(state.Asks) != 0 {
		t.Errorf("rejected order rested in the book: %+v", state.Asks)
	}
}

// The band is anchored to the session reference, not to the last trade. Walking
// the price up in legal steps must not walk the band up with it — that is the
// failure that would let 190 reach 1000 one order at a time.
func TestBandDoesNotWalkWithTheMarket(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	// Trade at 240, comfortably inside the 133..247 band.
	if _, err := submit(reg, 1, 1, engine.Sell, 240, 10); err != nil {
		t.Fatalf("resting sell at 240: %v", err)
	}
	if _, err := submit(reg, 2, 2, engine.Buy, 240, 10); err != nil {
		t.Fatalf("buy at 240: %v", err)
	}

	// If the band had re-anchored to 240, its ceiling would now be 312 and this
	// would be accepted.
	_, err := submit(reg, 3, 1, engine.Sell, 300, 10)
	if !errors.Is(err, ErrOutsideBand) {
		t.Fatalf("err = %v, want ErrOutsideBand — the band must stay anchored at 190", err)
	}
}

// Reaching the ceiling legally is what trips the breaker. The trade executes —
// it was inside the band — and the instrument then stops trading.
func TestTradeAtTheCeilingTripsTheBreaker(t *testing.T) {
	reg, obs, now := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 247, 10); err != nil {
		t.Fatalf("resting sell at the ceiling: %v", err)
	}
	trades, err := submit(reg, 2, 2, engine.Buy, 247, 10)
	if err != nil {
		t.Fatalf("buy at the ceiling: %v", err)
	}
	if len(trades) != 1 {
		t.Fatalf("got %d trades, want 1 — a price at the ceiling still executes", len(trades))
	}

	if len(obs.halted) != 1 {
		t.Fatalf("got %d halts, want 1", len(obs.halted))
	}
	if got := obs.halted[0]; got.price != 247 || got.reference != 190 {
		t.Errorf("halt recorded price %d against reference %d, want 247 against 190",
			got.price, got.reference)
	}
	if want := now.Add(2 * time.Minute); !obs.halted[0].until.Equal(want) {
		t.Errorf("halt until %v, want %v", obs.halted[0].until, want)
	}

	// The instrument is now closed to new orders.
	if _, err := submit(reg, 3, 1, engine.Sell, 200, 10); !errors.Is(err, ErrEmitenHalted) {
		t.Errorf("err = %v, want ErrEmitenHalted", err)
	}
}

// Once halted, an order priced outside the band must still be reported as
// ErrOutsideBand rather than ErrEmitenHalted — the client's mistake is the price
// it asked for, and that is true whether or not the instrument happens to be
// halted right now. Checking the halt first would mask that mistake behind a
// halt message that says nothing about why the order itself was bad.
func TestOutsideBandTakesPriorityOverHaltedWhenBothApply(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 247, 10); err != nil {
		t.Fatalf("resting sell at the ceiling: %v", err)
	}
	if _, err := submit(reg, 2, 2, engine.Buy, 247, 10); err != nil {
		t.Fatalf("buy at the ceiling: %v", err)
	}

	// The breaker has now tripped. An order priced far outside the band must
	// still be rejected as ErrOutsideBand, not ErrEmitenHalted.
	if _, err := submit(reg, 3, 1, engine.Sell, 1000, 10); !errors.Is(err, ErrOutsideBand) {
		t.Errorf("err = %v, want ErrOutsideBand even while halted", err)
	}

	// An order priced inside the band still gets ErrEmitenHalted, since there is
	// nothing wrong with its price — only with the instrument's state.
	if _, err := submit(reg, 4, 1, engine.Sell, 200, 10); !errors.Is(err, ErrEmitenHalted) {
		t.Errorf("err = %v, want ErrEmitenHalted for a price inside the band", err)
	}
}

// A halt ends by itself. The registry decides that against the clock, so trading
// reopens at the deadline whether or not the sweep has run yet.
func TestHaltExpiresOnTheClock(t *testing.T) {
	reg, _, now := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 247, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}
	if _, err := submit(reg, 2, 2, engine.Buy, 247, 10); err != nil {
		t.Fatalf("buy at the ceiling: %v", err)
	}

	// One second before the deadline: still closed.
	*now = now.Add(2*time.Minute - time.Second)
	if _, err := submit(reg, 3, 1, engine.Sell, 200, 10); !errors.Is(err, ErrEmitenHalted) {
		t.Errorf("err = %v, want the halt still in force one second early", err)
	}

	// At the deadline: open again, without ExpireHalts having run.
	*now = now.Add(time.Second)
	if _, err := submit(reg, 4, 1, engine.Sell, 200, 10); err != nil {
		t.Errorf("err = %v, want the halt to have expired on the clock alone", err)
	}
}

// ExpireHalts is what produces the side effects — the resume announcement and
// the database cleanup — and it must only report a halt once.
func TestExpireHaltsReportsEachResumeOnce(t *testing.T) {
	reg, _, now := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 247, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}
	if _, err := submit(reg, 2, 2, engine.Buy, 247, 10); err != nil {
		t.Fatalf("buy at the ceiling: %v", err)
	}

	if resumed := reg.ExpireHalts(); len(resumed) != 0 {
		t.Errorf("resumed %v before the deadline, want none", resumed)
	}

	*now = now.Add(2 * time.Minute)
	resumed := reg.ExpireHalts()
	if len(resumed) != 1 || resumed[0] != 1 {
		t.Fatalf("resumed = %v, want [1]", resumed)
	}
	if again := reg.ExpireHalts(); len(again) != 0 {
		t.Errorf("resumed %v on a second sweep, want none — each halt reports once", again)
	}
}

// An instrument with no reference has no band. Rejecting every order instead
// would make a newly listed instrument untradeable, and inventing an anchor
// would bound its price to a number nobody chose.
func TestNoReferenceMeansNoBand(t *testing.T) {
	reg, obs, _ := testRegistry(t, 0, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 1000, 10); err != nil {
		t.Fatalf("err = %v, want the order accepted when no band applies", err)
	}
	if _, err := submit(reg, 2, 2, engine.Buy, 1000, 10); err != nil {
		t.Fatalf("err = %v, want the order accepted when no band applies", err)
	}
	if len(obs.halted) != 0 {
		t.Error("the breaker tripped on an instrument with no reference price")
	}
}

// Without a configured policy the registry behaves exactly as it did before the
// breaker existed. This is what the pre-existing engine and registry tests rely
// on, and what a deployment without the config domain falls back to.
func TestNilPolicyDisablesTheBreaker(t *testing.T) {
	reg, _, _ := testRegistry(t, 190, nil)

	if _, err := submit(reg, 1, 1, engine.Sell, 1000, 10); err != nil {
		t.Fatalf("err = %v, want no band enforced without a policy", err)
	}
}

// A market order carries no price of its own, so it is not band-checked on the
// way in — what it pays is a resting order's price, which was checked on its own
// way in. It must still trip the breaker if what it pays reaches the edge.
func TestMarketOrderTripsTheBreakerAtTheCeiling(t *testing.T) {
	reg, obs, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 247, 10); err != nil {
		t.Fatalf("resting sell at the ceiling: %v", err)
	}

	o := &engine.Order{
		ID: 2, EmitenID: 1, ParticipantID: 2,
		Side: engine.Buy, Type: engine.Market, Qty: 10,
	}
	if _, _, err := reg.Submit(o, func([]engine.Trade) error { return nil }); err != nil {
		t.Fatalf("market buy: %v", err)
	}

	if len(obs.halted) != 1 {
		t.Fatalf("got %d halts, want 1 — a market order that prints at the ceiling still trips it", len(obs.halted))
	}
}

// A failed persist unwinds the matching pass, so nothing executed — and a
// breaker that fires anyway would close an instrument over a trade that never
// happened.
func TestFailedPersistDoesNotTripTheBreaker(t *testing.T) {
	reg, obs, _ := testRegistry(t, 190, stubPolicy{bps: 3000, duration: 2 * time.Minute})

	if _, err := submit(reg, 1, 1, engine.Sell, 247, 10); err != nil {
		t.Fatalf("resting sell: %v", err)
	}

	o := &engine.Order{
		ID: 2, EmitenID: 1, ParticipantID: 2,
		Side: engine.Buy, Type: engine.Limit, Price: 247, Qty: 10,
	}
	boom := errors.New("database down")
	if _, _, err := reg.Submit(o, func([]engine.Trade) error { return boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the persist error", err)
	}

	if len(obs.halted) != 0 {
		t.Error("the breaker tripped on a matching pass that was unwound")
	}
	if _, err := submit(reg, 3, 1, engine.Sell, 200, 10); err != nil {
		t.Errorf("err = %v, want the instrument still open", err)
	}
}
