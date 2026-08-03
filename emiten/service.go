package emiten

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

// ErrNotFound means the requested emiten code is not listed.
var ErrNotFound = errors.New("emiten: not found")

// ValidationError is a rejected request: well-formed, but breaking a business
// rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Repository writes listed instruments.
type Repository interface {
	CreateEmiten(ctx context.Context, e market.Emiten) (market.Emiten, error)

	// ActivateEmiten opens a dormant instrument for trading at its offering
	// price, setting is_active and ipo_price together because they are one
	// event: an instrument becomes tradeable *at* a price.
	ActivateEmiten(ctx context.Context, emitenID, ipoPrice int64) error
}

// RestateShares updates an instrument's listed share count and band anchor after
// a corporate action has already persisted them, bringing the live directory and
// registry into agreement with the database.
//
// It writes nothing itself. The corporate action domain restates the emiten row
// inside the same transaction that moves every holder's position — the two must
// not be able to disagree — so by the time this is called the database is already
// correct and only the in-memory copies are stale. Splitting it out this way is
// what lets that domain own its transaction while this one owns the kernel state.
//
// Without it, the instrument keeps trading against its pre-split numbers: the
// band would reject orders at the new fair value, and every valuation would use
// the old share count.
func (s *Service) RestateShares(ctx context.Context, emitenID, listedShares int64, reference *int64) error {
	e, ok := s.dir.EmitenByID(emitenID)
	if !ok {
		return ErrNotFound
	}

	s.dir.RestateShares(e.Kode, listedShares, reference)

	// The registry keeps its own copy of the anchor, read on the submit path under
	// its lock; the directory alone would leave the band measuring from the
	// pre-split price.
	if reference != nil {
		s.reg.SetReference(emitenID, *reference)
	}
	return nil
}

// ListingObserver is notified around a new listing, so market-wide statistics
// derived from the set of instruments can absorb the change.
//
// It is two calls rather than one because absorbing a listing needs the total
// from before it as well as after: MarketCap is read first, and the result is
// handed back to ListingAdded once the instrument is registered.
//
// Declared here as an interface rather than taking the index package directly,
// so this package depends on the one event it announces rather than on another
// domain. Satisfied by index.Service.
type ListingObserver interface {
	// MarketCap reports the current total, captured before the listing.
	MarketCap(ctx context.Context) int64

	// ListingAdded announces that an instrument has been registered, given the
	// total from before it existed.
	ListingAdded(ctx context.Context, capBefore int64) error
}

// Service reads and creates listed instruments.
type Service struct {
	dir    *market.Directory
	reg    *market.Registry
	repo   Repository
	prices PriceStatsRepository

	// listings observes new instruments so the composite index can restate its
	// divisor. Optional: nil means nothing observes, which is what a wiring
	// without an index expects.
	listings ListingObserver
}

// NewService wires the emiten domain to the market kernel and its repositories.
func NewService(dir *market.Directory, reg *market.Registry, repo Repository, prices PriceStatsRepository) *Service {
	return &Service{dir: dir, reg: reg, repo: repo, prices: prices}
}

// ObserveListings registers the observer notified around each new listing.
//
// A setter rather than a constructor parameter because the index service is
// built after this one — the underwriter domain already depends on this service,
// and requiring the index up front would make the construction order circular.
func (s *Service) ObserveListings(o ListingObserver) { s.listings = o }

// List returns one page of instruments, ordered by code, plus the total for the
// pagination envelope.
//
// Master data only: computing price statistics per row would be a query per
// instrument. Those live on the detail endpoint.
func (s *Service) List(page, limit int) ([]market.Emiten, int) {
	all := s.dir.Emitens()
	total := len(all)

	start, end := httpx.Slice(page, limit, total)
	return all[start:end], total
}

// Detail returns one instrument with its all-time price statistics.
func (s *Service) Detail(ctx context.Context, kode string) (market.Emiten, PriceStats, error) {
	e, ok := s.dir.Emiten(kode)
	if !ok {
		return market.Emiten{}, PriceStats{}, ErrNotFound
	}

	stats, err := s.prices.PriceStats(ctx, e.ID)
	if err != nil {
		return market.Emiten{}, PriceStats{}, err
	}
	return e, stats, nil
}

// Create registers a new instrument, dormant.
//
// It is deliberately *not* tradeable on return. A listing has two halves — the
// instrument existing, and its shares being placed with the market — and only the
// first happens here. An instrument whose shares sit nowhere has nobody who could
// sell it and no offering price to quote against, so opening its book would
// advertise a market that cannot trade. Activate does the second half, and is
// reachable only through an IPO.
//
// No IPO price is set for the same reason: the offering price is decided when the
// offering runs, not when the instrument is first registered.
//
// Order matters: the row is written first, because the database owns uniqueness
// and a duplicate code must fail before any in-memory state moves. Registration
// follows and cannot fail — it is two map inserts — so the two can never disagree.
// The book is registered here despite being closed, so the instrument is readable
// while dormant rather than 404-ing until its offering runs.
//
// The index is not notified: a dormant instrument has no reference price, so
// index.marketCap skips it and the divisor needs no restating. It joins the index
// when Activate gives it a price.
func (s *Service) Create(ctx context.Context, req CreateRequest) (market.Emiten, error) {
	kode := strings.TrimSpace(req.Kode)
	nama := strings.TrimSpace(req.Nama)

	switch {
	case kode == "":
		return market.Emiten{}, invalid("kode is required")
	case nama == "":
		return market.Emiten{}, invalid("nama is required")
	case req.ListedShares <= 0:
		return market.Emiten{}, invalid("listed_shares must be > 0")
	case req.UnlistedShares < 0:
		return market.Emiten{}, invalid("unlisted_shares must be >= 0")
	}

	e, err := s.repo.CreateEmiten(ctx, market.Emiten{
		Kode:           kode,
		Nama:           nama,
		ListedShares:   req.ListedShares,
		UnlistedShares: req.UnlistedShares,
		IsActive:       false,
	})
	if err != nil {
		return market.Emiten{}, err
	}

	s.dir.AddEmiten(e)
	s.reg.AddBook(e)
	return e, nil
}

// Activate opens a dormant instrument for trading at its offering price.
//
// This is the second half of a listing, and the only way an instrument becomes
// tradeable. It is called by the underwriter domain once an offering's shares have
// been placed — never directly from an admin endpoint — because activating an
// instrument whose shares sit nowhere is the state Create exists to avoid.
//
// It refuses an instrument that is already active. That check is what makes an
// offering unrepeatable: a second IPO over a live instrument would issue its
// shares twice, and the price it was listed at would stop being the price it was
// listed at.
//
// Order mirrors Create: the row is written first because the database is the
// record, and the in-memory state follows only once that succeeds. The directory
// and the book are both updated — Submit reads the book's own copy of the flag, so
// the directory alone would leave the instrument advertised as active while still
// rejecting every order.
func (s *Service) Activate(ctx context.Context, kode string, ipoPrice int64) (market.Emiten, error) {
	e, ok := s.dir.Emiten(kode)
	switch {
	case !ok:
		return market.Emiten{}, ErrNotFound
	case e.IsActive:
		return market.Emiten{}, invalid("emiten %s is already listed and trading", kode)
	case ipoPrice <= 0:
		return market.Emiten{}, invalid("ipo_price must be > 0")
	}

	// Captured before anything is written, because absorbing the listing needs the
	// market's value as it stood without this instrument priced. Read here rather
	// than after the row lands so it cannot already include it.
	var capBefore int64
	if s.listings != nil {
		capBefore = s.listings.MarketCap(ctx)
	}

	if err := s.repo.ActivateEmiten(ctx, e.ID, ipoPrice); err != nil {
		return market.Emiten{}, err
	}

	s.dir.ActivateEmiten(kode, ipoPrice)
	s.reg.ActivateBook(e.ID)

	e.IsActive = true
	e.IPOPrice = &ipoPrice

	// Announced after the instrument is priced and registered, so the observer's
	// own valuation already includes it. This is the point the instrument enters
	// the index: before it, marketCap skipped it for having no reference price.
	//
	// A failure here is logged and swallowed rather than returned: the instrument
	// is listed, persisted, and tradeable by this point, and failing the request
	// would report a listing that did not happen. The cost is an index whose
	// divisor needs correcting, which is recoverable; unlisting is not.
	if s.listings != nil {
		if err := s.listings.ListingAdded(ctx, capBefore); err != nil {
			slog.Error("emiten: index adjust for listing", "kode", e.Kode, "err", err)
		}
	}
	return e, nil
}
