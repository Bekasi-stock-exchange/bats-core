package index

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"bekasi-automatic-trading-system/market"
)

// ErrNoLevel means the index has not been computed yet, so there is nothing to
// report. It is a transient startup condition, not a client error.
var ErrNoLevel = errors.New("index: no level computed yet")

type Service struct {
	dir    *market.Directory
	repo   Repository
	prices PriceRepository
	cache  *Cache
}

func NewService(dir *market.Directory, repo Repository, prices PriceRepository, cache *Cache) *Service {
	return &Service{dir: dir, repo: repo, prices: prices, cache: cache}
}

// Reads the stored definition and computes an opening level.
//
// Called once at startup, before the server accepts a request. The definition
// must load or startup fails — a missing row means migration 014 has not run,
// which is a deployment fault rather than something to paper over with a
// default divisor that no operator chose.
//
// Bootstrapping the divisor happens here too. Migration 014 seeds it at 1
// because it cannot see the market cap of the instruments present; on the first
// run this resolves it so the index opens at its base value rather than at
// whatever total market cap happens to be.
func (s *Service) Load(ctx context.Context) error {
	def, err := s.repo.LoadIndex(ctx, IHSG)
	if err != nil {
		return err
	}
	s.cache.SetDefinition(def)

	if err := s.bootstrapDivisor(ctx, def); err != nil {
		return err
	}
	return s.Recompute(ctx)
}

// Sets the divisor so the index opens at its base value, but only if it has
// never been set.
//
// The guard is the important half. Once the index has been running, the divisor
// carries every listing adjustment ever applied, and recomputing it from current
// market cap would silently reset the level to base — erasing the entire history
// of real price movement. The seeded 1 is the only value safe to overwrite,
// because it is the one value that means "not yet established".
func (s *Service) bootstrapDivisor(ctx context.Context, def Definition) error {
	if def.Divisor != 1 {
		return nil
	}

	cap, _, _ := s.marketCap(ctx)
	if cap == 0 {
		return nil // nothing priced yet; the seeded divisor stands until there is
	}

	// Recompute evaluates value = cap / divisor * base. Setting value = base and
	// solving gives divisor = cap, not cap / base — the base cancels out. Dividing
	// by it here as well would scale the opening level by base a second time,
	// opening the index at base² (10 000 rather than 100).
	divisor := float64(cap)
	saved, err := s.repo.SaveDivisor(ctx, def.ID, divisor)
	if err != nil {
		return err
	}
	s.cache.SetDefinition(saved)

	slog.Info("index divisor bootstrapped",
		"kode", saved.Kode, "divisor", divisor, "market_cap", cap)
	return nil
}

// Reports how many instruments were priced out of how many exist.
//
// An instrument with no reference price is skipped rather than counted as 0.
// market.Emiten.ReferencePrice returns nil precisely to avoid claiming an
// unpriced instrument is worthless, and folding a 0 into the sum here would
// reintroduce that claim one layer up. The members count is what makes the
// omission visible to a reader.
//
// Free-float weighted: ListedShares, not TotalShares. That is the tradeable
// portion, and it is how BEI has computed IHSG since 2021 — weighting by total
// shares would let a mostly-unlisted issuer dominate the index with stock that
// cannot actually be bought.
func (s *Service) marketCap(ctx context.Context) (int64, int, int) {
	emitens := s.dir.Emitens()

	last, err := s.prices.LastPrices(ctx)
	if err != nil {
		// A price read failure must not be reported as a market that lost all its
		// value. Returning zero members lets the caller distinguish it from a
		// genuinely empty market and leave the last good level standing.
		slog.Error("index: last prices", "err", err)
		return 0, 0, len(emitens)
	}

	var total int64
	var members int
	for _, e := range emitens {
		var lastPrice *int64
		if p, ok := last[e.ID]; ok {
			lastPrice = &p
		}

		ref := e.ReferencePrice(lastPrice)
		if ref == nil {
			continue // never traded and no IPO price: nothing to value it at
		}

		// price × shares for a large issuer is around 10^15, comfortably inside
		// int64's 9.2×10^18, and the sum across a market of this size stays well
		// under it.
		total += *ref * e.ListedShares
		members++
	}
	return total, members, len(emitens)
}

// Does not persist a snapshot. This runs on the trade path, where a database
// write per execution would put the index's storage cost onto matching latency;
// history is captured by Capture on its own schedule instead.
//
// A market that prices nothing leaves the previous level standing rather than
// publishing 0. That covers both the empty exchange and the failed price read,
// neither of which is news that the index has fallen to nothing.
func (s *Service) Recompute(ctx context.Context) error {
	def := s.cache.Definition()

	cap, members, total := s.marketCap(ctx)
	if members == 0 {
		return nil
	}
	if def.Divisor <= 0 {
		return fmt.Errorf("index: divisor is %v, refusing to compute", def.Divisor)
	}

	s.cache.SetLevel(Level{
		Kode:       def.Kode,
		Nama:       def.Nama,
		Value:      float64(cap) / def.Divisor * def.BaseValue,
		MarketCap:  cap,
		Divisor:    def.Divisor,
		Members:    members,
		Total:      total,
		CapturedAt: time.Now(),
	})
	return nil
}

// Served from the cache, not recomputed, so a client polling the level does not
// each time trigger a full market valuation.
func (s *Service) Current() (Level, error) {
	l, ok := s.cache.Level()
	if !ok {
		return Level{}, ErrNoLevel
	}
	return l, nil
}

// Separate from Recompute so that how often the index is *computed* (every
// trade) and how often it is *stored* (periodically) are independent decisions.
// Tying them together would either bloat the history with a row per execution or
// slow matching down to the speed of a write.
func (s *Service) Capture(ctx context.Context) error {
	l, ok := s.cache.Level()
	if !ok {
		return nil // nothing computed yet; nothing to record
	}

	def := s.cache.Definition()
	return s.repo.InsertSnapshot(ctx, int64(def.ID), Snapshot{
		Value:      l.Value,
		MarketCap:  l.MarketCap,
		Divisor:    l.Divisor,
		Members:    l.Members,
		CapturedAt: l.CapturedAt,
	})
}

// Newest first, plus the total for the pagination envelope.
func (s *Service) History(ctx context.Context, from, to *time.Time, page, limit int) ([]Snapshot, int, error) {
	def := s.cache.Definition()

	total, err := s.repo.CountSnapshots(ctx, def.ID, from, to)
	if err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * limit
	if offset >= total {
		return nil, total, nil
	}

	snaps, err := s.repo.ListSnapshots(ctx, def.ID, from, to, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	return snaps, total, nil
}

// Restates the divisor so a new listing does not move the index.
//
// This is the mechanism that makes the index a price series rather than a
// running total of market capitalisation. When an instrument lists, total market
// cap jumps by that instrument's entire value — a change that has nothing to do
// with any price moving. Scaling the divisor by the same ratio absorbs the jump,
// leaving the level unchanged across the event and every subsequent move a real
// one.
//
//	newDivisor = oldDivisor × (capAfter / capBefore)
//
// Called after the listing is registered in the directory, so capAfter includes
// it. A failure here is logged and swallowed by the caller rather than failing
// the listing: the instrument is already listed and tradeable, and refusing that
// after the fact would be worse than an index that needs its divisor corrected.
func (s *Service) ListingAdded(ctx context.Context, capBefore int64) error {
	if capBefore <= 0 {
		// No prior valuation to hold constant — the first priced instrument sets
		// the level rather than being adjusted against nothing.
		return s.Recompute(ctx)
	}

	capAfter, members, _ := s.marketCap(ctx)
	if members == 0 || capAfter <= 0 {
		return nil
	}

	def := s.cache.Definition()
	next := def.Divisor * (float64(capAfter) / float64(capBefore))

	saved, err := s.repo.SaveDivisor(ctx, def.ID, next)
	if err != nil {
		return err
	}
	s.cache.SetDefinition(saved)

	slog.Info("index divisor adjusted for listing",
		"kode", saved.Kode, "from", def.Divisor, "to", next,
		"cap_before", capBefore, "cap_after", capAfter)

	return s.Recompute(ctx)
}

// For a caller that needs to capture the total before a listing, so it can be
// passed to AdjustForListing afterwards.
func (s *Service) MarketCap(ctx context.Context) int64 {
	cap, _, _ := s.marketCap(ctx)
	return cap
}
