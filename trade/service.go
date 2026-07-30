package trade

import (
	"context"
	"errors"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

// ErrEmitenNotFound means the requested emiten code is not listed.
var ErrEmitenNotFound = errors.New("trade: emiten not found")

// intervals is the allowlist of candle widths, in seconds.
//
// Seconds rather than date_trunc unit names because date_trunc has no 5-minute
// unit — bucketing on the epoch handles every width uniformly, and the value
// reaches SQL as an integer parameter. Buckets are UTC-aligned.
var intervals = map[string]int64{
	"1m": 60,
	"5m": 300,
	"1h": 3600,
	"1d": 86400,
}

// DefaultCandleLimit caps how many bars one request returns.
const DefaultCandleLimit = 500

// Service reads executions.
type Service struct {
	dir  *market.Directory
	repo Repository
}

// NewService wires the trade domain to the directory and its repository.
func NewService(dir *market.Directory, repo Repository) *Service {
	return &Service{dir: dir, repo: repo}
}

// Trades returns one page of the raw execution log.
func (s *Service) Trades(ctx context.Context, emitenKode, participantKode string, page, limit int) ([]Record, int, error) {
	f, err := s.filter(emitenKode, participantKode)
	if err != nil {
		return nil, 0, err
	}

	total, err := s.repo.CountTrades(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	start, _ := httpx.Slice(page, limit, total)

	records, err := s.repo.ListTrades(ctx, f, limit, start)
	return records, total, err
}

// Transactions returns one page of a broker's fills, seen from its own side.
//
// The broker is passed in by the caller from the authenticated identity, never
// from a query parameter — this is a read-authorisation boundary.
func (s *Service) Transactions(ctx context.Context, participantID int64, emitenKode string, page, limit int) ([]Transaction, int, error) {
	emitenID, err := s.optionalEmiten(emitenKode)
	if err != nil {
		return nil, 0, err
	}

	total, err := s.repo.CountTransactions(ctx, participantID, emitenID)
	if err != nil {
		return nil, 0, err
	}
	start, _ := httpx.Slice(page, limit, total)

	txs, err := s.repo.ListTransactions(ctx, participantID, emitenID, limit, start)
	return txs, total, err
}

// Ticks returns one page of an instrument's raw executions.
func (s *Service) Ticks(ctx context.Context, kode string, page, limit int) ([]Tick, int, error) {
	e, ok := s.dir.Emiten(kode)
	if !ok {
		return nil, 0, ErrEmitenNotFound
	}

	total, err := s.repo.CountTicks(ctx, e.ID)
	if err != nil {
		return nil, 0, err
	}
	start, _ := httpx.Slice(page, limit, total)

	ticks, err := s.repo.ListTicks(ctx, e.ID, limit, start)
	return ticks, total, err
}

// Candles returns an instrument's OHLC bars at the requested interval.
func (s *Service) Candles(ctx context.Context, kode, interval string) ([]Candle, error) {
	e, ok := s.dir.Emiten(kode)
	if !ok {
		return nil, ErrEmitenNotFound
	}

	seconds, ok := intervals[interval]
	if !ok {
		return nil, ErrInvalidInterval
	}
	return s.repo.ListCandles(ctx, e.ID, seconds, DefaultCandleLimit)
}

// Intervals lists the accepted candle widths, for error messages and docs.
func Intervals() []string { return []string{"1m", "5m", "1h", "1d"} }

// ResolveParticipant maps a broker code to its id, for the admin filter.
func (s *Service) ResolveParticipant(kode string) (int64, bool) {
	p, ok := s.dir.Participant(kode)
	return p.ID, ok
}

func (s *Service) filter(emitenKode, participantKode string) (Filter, error) {
	emitenID, err := s.optionalEmiten(emitenKode)
	if err != nil {
		return Filter{}, err
	}

	var participantID *int64
	if participantKode != "" {
		p, ok := s.dir.Participant(participantKode)
		if !ok {
			return Filter{}, ErrEmitenNotFound
		}
		participantID = &p.ID
	}
	return Filter{EmitenID: emitenID, ParticipantID: participantID}, nil
}

func (s *Service) optionalEmiten(kode string) (*int64, error) {
	if kode == "" {
		return nil, nil
	}
	e, ok := s.dir.Emiten(kode)
	if !ok {
		return nil, ErrEmitenNotFound
	}
	return &e.ID, nil
}
