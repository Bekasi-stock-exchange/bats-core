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

// Create lists a new instrument and makes it immediately tradeable.
//
// Order matters: the row is written first, because the database owns uniqueness
// and a duplicate code must fail before any in-memory state moves. Registration
// follows and cannot fail — it is two map inserts — so the two can never disagree.
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
	// Required for new listings, though the column is nullable: the five seeded
	// instruments predate it and already have a trade history, so the database
	// cannot demand it without inventing a price for them. Anything listed from
	// here on must carry one, or it enters the exchange with nothing to quote
	// against.
	case req.IPOPrice <= 0:
		return market.Emiten{}, invalid("ipo_price must be > 0")
	}

	// Captured before anything is written, because absorbing the listing needs the
	// market's value as it stood without this instrument. Read here rather than
	// after the row lands so it cannot already include it.
	var capBefore int64
	if s.listings != nil {
		capBefore = s.listings.MarketCap(ctx)
	}

	ipoPrice := req.IPOPrice
	e, err := s.repo.CreateEmiten(ctx, market.Emiten{
		Kode:           kode,
		Nama:           nama,
		ListedShares:   req.ListedShares,
		UnlistedShares: req.UnlistedShares,
		IsActive:       true,
		IPOPrice:       &ipoPrice,
	})
	if err != nil {
		return market.Emiten{}, err
	}

	s.dir.AddEmiten(e)
	s.reg.AddBook(e)

	// Announced after the instrument is registered, so the observer's own
	// valuation already includes it.
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
