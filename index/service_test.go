package index

import (
	"context"
	"math"
	"testing"
	"time"

	"bekasi-automatic-trading-system/market"
)

// stubRepo is an in-memory Repository. Only the divisor round-trip and the
// snapshot append matter to these tests; history paging is a thin pass-through
// to SQL and is not exercised here.
type stubRepo struct {
	def       Definition
	snapshots []Snapshot
	saveErr   error
	saves     int
}

func (s *stubRepo) LoadIndex(context.Context, string) (Definition, error) {
	return s.def, nil
}

func (s *stubRepo) SaveDivisor(_ context.Context, _ int16, divisor float64) (Definition, error) {
	if s.saveErr != nil {
		return Definition{}, s.saveErr
	}
	s.saves++
	s.def.Divisor = divisor
	return s.def, nil
}

func (s *stubRepo) InsertSnapshot(_ context.Context, _ int64, snap Snapshot) error {
	s.snapshots = append(s.snapshots, snap)
	return nil
}

func (s *stubRepo) ListSnapshots(context.Context, int16, *time.Time, *time.Time, int, int) ([]Snapshot, error) {
	return s.snapshots, nil
}

func (s *stubRepo) CountSnapshots(context.Context, int16, *time.Time, *time.Time) (int, error) {
	return len(s.snapshots), nil
}

type stubPrices struct {
	prices map[int64]int64
	err    error
}

func (s *stubPrices) LastPrices(context.Context) (map[int64]int64, error) {
	return s.prices, s.err
}

// The divisor is already established, so computation is not bootstrapping.
func fixture(t *testing.T, emitens []market.Emiten, prices map[int64]int64, divisor float64) (*Service, *stubRepo, *Cache) {
	t.Helper()

	repo := &stubRepo{def: Definition{
		ID: 1, Kode: IHSG, Nama: "Indeks Harga Saham Gabungan",
		BaseValue: 100, Divisor: divisor,
	}}
	cache := NewCache()
	cache.SetDefinition(repo.def)

	svc := NewService(
		market.NewDirectory(emitens, nil),
		repo,
		&stubPrices{prices: prices},
		cache,
	)
	return svc, repo, cache
}

func ipo(v int64) *int64 { return &v }

// A tolerance, since the index is a ratio and exact equality would make these
// tests fragile for no benefit.
func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-6 }

func TestRecomputeWeightsByListedShares(t *testing.T) {
	// Two instruments, 1000 listed shares each, but the first holds a large
	// unlisted block. Free-float weighting must ignore that block entirely.
	emitens := []market.Emiten{
		{ID: 1, Kode: "AAAA", ListedShares: 1000, UnlistedShares: 9000, IsActive: true},
		{ID: 2, Kode: "BBBB", ListedShares: 1000, IsActive: true},
	}
	prices := map[int64]int64{1: 100, 2: 200}

	// cap = 1000*100 + 1000*200 = 300_000. Divisor 3000 with base 100 puts the
	// level at 10_000.
	svc, _, _ := fixture(t, emitens, prices, 3000)

	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}

	got, err := svc.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if got.MarketCap != 300_000 {
		t.Errorf("MarketCap = %d, want 300000 (unlisted shares must not count)", got.MarketCap)
	}
	if !closeTo(got.Value, 10_000) {
		t.Errorf("Value = %v, want 10000", got.Value)
	}
	if got.Members != 2 || got.Total != 2 {
		t.Errorf("Members/Total = %d/%d, want 2/2", got.Members, got.Total)
	}
}

func TestRecomputeFallsBackToIPOPrice(t *testing.T) {
	// BBBB has never traded but carries an IPO price, so it must still be valued
	// — that is the rule market.Emiten.ReferencePrice encodes.
	emitens := []market.Emiten{
		{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true},
		{ID: 2, Kode: "BBBB", ListedShares: 1000, IPOPrice: ipo(500), IsActive: true},
	}
	prices := map[int64]int64{1: 100} // only AAAA has traded

	svc, _, _ := fixture(t, emitens, prices, 1)

	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}

	got, _ := svc.Current()
	if want := int64(1000*100 + 1000*500); got.MarketCap != want {
		t.Errorf("MarketCap = %d, want %d (IPO price must back an untraded instrument)", got.MarketCap, want)
	}
	if got.Members != 2 {
		t.Errorf("Members = %d, want 2", got.Members)
	}
}

func TestRecomputeExcludesUnpricedInstrument(t *testing.T) {
	// BBBB has neither a trade nor an IPO price. It must be excluded from the
	// sum rather than folded in as zero, and the members count must reveal it.
	emitens := []market.Emiten{
		{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true},
		{ID: 2, Kode: "BBBB", ListedShares: 1000, IsActive: true},
	}
	prices := map[int64]int64{1: 100}

	svc, _, _ := fixture(t, emitens, prices, 1)

	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}

	got, _ := svc.Current()
	if got.MarketCap != 100_000 {
		t.Errorf("MarketCap = %d, want 100000", got.MarketCap)
	}
	if got.Members != 1 || got.Total != 2 {
		t.Errorf("Members/Total = %d/%d, want 1/2 — the gap is what makes exclusion visible",
			got.Members, got.Total)
	}
}

func TestRecomputeKeepsLastLevelWhenPricesFail(t *testing.T) {
	// A failed price read must not publish an index of zero: that would report a
	// market that lost all its value, which is a different and false statement.
	emitens := []market.Emiten{{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true}}

	svc, _, _ := fixture(t, emitens, map[int64]int64{1: 100}, 10)
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	before, _ := svc.Current()

	svc.prices = &stubPrices{err: context.DeadlineExceeded}
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute after failure: %v", err)
	}

	after, err := svc.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if after.Value != before.Value {
		t.Errorf("Value = %v after a failed price read, want the previous %v", after.Value, before.Value)
	}
}

func TestCurrentBeforeComputationReportsNoLevel(t *testing.T) {
	svc, _, _ := fixture(t, nil, nil, 1)

	if _, err := svc.Current(); err != ErrNoLevel {
		t.Errorf("Current() error = %v, want ErrNoLevel — an uncomputed index is not a level of 0", err)
	}
}

func TestBootstrapDivisorOpensAtBaseValue(t *testing.T) {
	// Migration 014 seeds divisor = 1 because it cannot see market cap. Load must
	// resolve it so the index opens at its base rather than at raw market cap.
	emitens := []market.Emiten{{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true}}
	prices := map[int64]int64{1: 5000}

	svc, repo, _ := fixture(t, emitens, prices, 1)

	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, err := svc.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if !closeTo(got.Value, 100) {
		t.Errorf("Value = %v on bootstrap, want the base value 100", got.Value)
	}
	// value = cap / divisor * base, so opening at base means divisor = cap. The
	// base cancels; dividing by it here too would open the index at base².
	if want := float64(5_000_000); !closeTo(repo.def.Divisor, want) {
		t.Errorf("stored divisor = %v, want %v", repo.def.Divisor, want)
	}
}

func TestBootstrapDivisorLeavesEstablishedDivisorAlone(t *testing.T) {
	// A divisor other than 1 carries every listing adjustment ever applied.
	// Re-deriving it from current market cap would reset the level to base and
	// erase the entire history of real price movement.
	emitens := []market.Emiten{{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true}}
	prices := map[int64]int64{1: 5000}

	svc, repo, _ := fixture(t, emitens, prices, 250)

	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}

	if repo.saves != 0 {
		t.Errorf("SaveDivisor called %d times, want 0 for an established divisor", repo.saves)
	}
	got, _ := svc.Current()
	if want := float64(5_000_000) / 250 * 100; !closeTo(got.Value, want) {
		t.Errorf("Value = %v, want %v — the stored divisor must stand", got.Value, want)
	}
}

func TestAdjustForListingKeepsLevelUnchanged(t *testing.T) {
	// The point of the divisor: a new listing adds its whole market cap, and the
	// index must not move on an event where no price changed.
	emitens := []market.Emiten{
		{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true},
		{ID: 2, Kode: "BBBB", ListedShares: 1000, IsActive: true},
	}
	prices := map[int64]int64{1: 100, 2: 200}

	svc, _, _ := fixture(t, emitens, prices, 3000)
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	before, _ := svc.Current()

	// Capture the pre-listing cap, then list a third instrument — exactly the
	// sequence the emiten service performs.
	capBefore := svc.MarketCap(context.Background())

	svc.dir.AddEmiten(market.Emiten{
		ID: 3, Kode: "CCCC", ListedShares: 5000, IPOPrice: ipo(1000), IsActive: true,
	})

	if err := svc.ListingAdded(context.Background(), capBefore); err != nil {
		t.Fatalf("AdjustForListing: %v", err)
	}

	after, _ := svc.Current()
	if !closeTo(after.Value, before.Value) {
		t.Errorf("Value = %v after listing, want it unchanged at %v", after.Value, before.Value)
	}
	if after.MarketCap <= before.MarketCap {
		t.Errorf("MarketCap = %d, want it above %d — the listing does add value",
			after.MarketCap, before.MarketCap)
	}
	if after.Members != 3 {
		t.Errorf("Members = %d, want 3", after.Members)
	}
}

func TestAdjustForListingThenPriceMoveIsRealMovement(t *testing.T) {
	// After a listing is absorbed, a genuine price change must still move the
	// index — the adjustment neutralises the listing, not the market.
	emitens := []market.Emiten{{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true}}
	prices := map[int64]int64{1: 100}

	svc, _, _ := fixture(t, emitens, prices, 10)
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	before, _ := svc.Current()

	capBefore := svc.MarketCap(context.Background())
	svc.dir.AddEmiten(market.Emiten{
		ID: 2, Kode: "BBBB", ListedShares: 1000, IPOPrice: ipo(300), IsActive: true,
	})
	if err := svc.ListingAdded(context.Background(), capBefore); err != nil {
		t.Fatalf("AdjustForListing: %v", err)
	}

	// AAAA doubles.
	svc.prices = &stubPrices{prices: map[int64]int64{1: 200}}
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute after price move: %v", err)
	}

	after, _ := svc.Current()
	if after.Value <= before.Value {
		t.Errorf("Value = %v after a price rise, want it above %v", after.Value, before.Value)
	}
}

func TestCaptureRecordsTheComputedLevel(t *testing.T) {
	emitens := []market.Emiten{{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true}}

	svc, repo, _ := fixture(t, emitens, map[int64]int64{1: 100}, 10)
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	if err := svc.Capture(context.Background()); err != nil {
		t.Fatalf("Capture: %v", err)
	}

	if len(repo.snapshots) != 1 {
		t.Fatalf("snapshots = %d, want 1", len(repo.snapshots))
	}
	level, _ := svc.Current()
	got := repo.snapshots[0]
	if !closeTo(got.Value, level.Value) {
		t.Errorf("snapshot value = %v, want the computed %v", got.Value, level.Value)
	}
	if got.Divisor != level.Divisor {
		t.Errorf("snapshot divisor = %v, want %v — a stored level must stay verifiable",
			got.Divisor, level.Divisor)
	}
}

func TestCaptureTwiceAppendsTwoPoints(t *testing.T) {
	// The admin capture endpoint can be called at will, and the periodic capture
	// keeps running alongside it. Each call must append rather than overwrite, or
	// a manually marked moment would replace the automatic series.
	emitens := []market.Emiten{{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true}}

	svc, repo, _ := fixture(t, emitens, map[int64]int64{1: 100}, 10)
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}

	for i := range 2 {
		if err := svc.Capture(context.Background()); err != nil {
			t.Fatalf("Capture %d: %v", i, err)
		}
	}
	if len(repo.snapshots) != 2 {
		t.Errorf("snapshots = %d, want 2", len(repo.snapshots))
	}
}

func TestRecomputeFallsWhenPricesCollapse(t *testing.T) {
	// Panic selling: every instrument's last price drops sharply between two
	// Recompute calls, as consecutive sell-side trades walk the book down.
	// MarketCap is a straight sum of price*shares and Value is cap/divisor*base
	// with no other term, so a market-wide price collapse must lower Value —
	// never raise it — regardless of how many trades produced that price.
	emitens := []market.Emiten{
		{ID: 1, Kode: "AAAA", ListedShares: 1000, IsActive: true},
		{ID: 2, Kode: "BBBB", ListedShares: 1000, IsActive: true},
		{ID: 3, Kode: "CCCC", ListedShares: 1000, IsActive: true},
	}

	svc, _, _ := fixture(t, emitens, map[int64]int64{1: 1000, 2: 1000, 3: 1000}, 3_000_000)
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute: %v", err)
	}
	before, _ := svc.Current()
	if !closeTo(before.Value, 100) {
		t.Fatalf("Value = %v before panic selling, want 100", before.Value)
	}

	// A wave of panic selling drives every last price down to a quarter of what
	// it was — the same shape as the reported incident (100 -> 400 is this
	// inverted 4x, i.e. the bug would show up here as the level rising instead
	// of falling to 25).
	svc.prices = &stubPrices{prices: map[int64]int64{1: 250, 2: 250, 3: 250}}
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute after panic selling: %v", err)
	}

	after, err := svc.Current()
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if after.Value >= before.Value {
		t.Errorf("Value = %v after panic selling, want it below %v (a market-wide price collapse must lower the index)",
			after.Value, before.Value)
	}
	if !closeTo(after.Value, 25) {
		t.Errorf("Value = %v after panic selling, want 25 (proportional to the 4x price drop)", after.Value)
	}

	// A further, smaller step down must keep moving the level in the same
	// direction — this catches an oscillation/sign bug that only a single big
	// jump would miss.
	svc.prices = &stubPrices{prices: map[int64]int64{1: 100, 2: 100, 3: 100}}
	if err := svc.Recompute(context.Background()); err != nil {
		t.Fatalf("Recompute after further selling: %v", err)
	}
	final, _ := svc.Current()
	if final.Value >= after.Value {
		t.Errorf("Value = %v after further panic selling, want it below %v", final.Value, after.Value)
	}
}

func TestCaptureBeforeComputationStoresNothing(t *testing.T) {
	svc, repo, _ := fixture(t, nil, nil, 1)

	if err := svc.Capture(context.Background()); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(repo.snapshots) != 0 {
		t.Errorf("snapshots = %d, want 0 when nothing has been computed", len(repo.snapshots))
	}
}
