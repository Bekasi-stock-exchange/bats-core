package assets

import (
	"context"
	"errors"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

var ErrParticipantNotFound = errors.New("assets: participant not found")

type Service struct {
	dir  *market.Directory
	repo Repository
}

func NewService(dir *market.Directory, repo Repository) *Service {
	return &Service{dir: dir, repo: repo}
}

// Seeds the in-memory ledger at startup.
func (s *Service) LoadHoldings(ctx context.Context) ([]market.Holding, error) {
	return s.repo.LoadHoldings(ctx)
}

// A nil participantID means every broker.
func (s *Service) List(ctx context.Context, participantID *int64, page, limit int) ([]Record, int, error) {
	total, err := s.repo.CountHoldings(ctx, participantID)
	if err != nil {
		return nil, 0, err
	}
	start, _ := httpx.Slice(page, limit, total)

	records, err := s.repo.ListHoldings(ctx, participantID, limit, start)
	return records, total, err
}

// For the admin filter.
func (s *Service) ResolveParticipant(kode string) (int64, error) {
	p, ok := s.dir.Participant(kode)
	if !ok {
		return 0, ErrParticipantNotFound
	}
	return p.ID, nil
}
