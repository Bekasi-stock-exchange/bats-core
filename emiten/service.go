package emiten

import (
	"context"
	"errors"
	"fmt"
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

// Service reads and creates listed instruments.
type Service struct {
	dir    *market.Directory
	reg    *market.Registry
	repo   Repository
	prices PriceStatsRepository
}

// NewService wires the emiten domain to the market kernel and its repositories.
func NewService(dir *market.Directory, reg *market.Registry, repo Repository, prices PriceStatsRepository) *Service {
	return &Service{dir: dir, reg: reg, repo: repo, prices: prices}
}

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
	}

	e, err := s.repo.CreateEmiten(ctx, market.Emiten{
		Kode:           kode,
		Nama:           nama,
		ListedShares:   req.ListedShares,
		UnlistedShares: req.UnlistedShares,
		IsActive:       true,
	})
	if err != nil {
		return market.Emiten{}, err
	}

	s.dir.AddEmiten(e)
	s.reg.AddBook(e)
	return e, nil
}
