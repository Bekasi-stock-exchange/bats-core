package wallet

import (
	"context"
	"errors"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

// ErrParticipantNotFound means the requested broker code is not registered.
var ErrParticipantNotFound = errors.New("wallet: participant not found")

// Service reads broker cash balances.
type Service struct {
	dir  *market.Directory
	repo Repository
}

// NewService wires the wallet domain to the directory and its repository.
func NewService(dir *market.Directory, repo Repository) *Service {
	return &Service{dir: dir, repo: repo}
}

// LoadWallets returns every broker wallet, to seed the in-memory cash ledger at
// startup.
func (s *Service) LoadWallets(ctx context.Context) ([]market.Wallet, error) {
	return s.repo.LoadWallets(ctx)
}

// List returns one page of wallets. A nil participantID means every broker.
func (s *Service) List(ctx context.Context, participantID *int64, page, limit int) ([]Record, int, error) {
	total, err := s.repo.CountWallets(ctx, participantID)
	if err != nil {
		return nil, 0, err
	}
	start, _ := httpx.Slice(page, limit, total)

	records, err := s.repo.ListWallets(ctx, participantID, limit, start)
	return records, total, err
}

// Mine returns the authenticated broker's own wallet.
func (s *Service) Mine(ctx context.Context, participantID int64) (Record, error) {
	return s.repo.FindWallet(ctx, participantID)
}

// ResolveParticipant maps a broker code to its id, for the admin filter.
func (s *Service) ResolveParticipant(kode string) (int64, error) {
	p, ok := s.dir.Participant(kode)
	if !ok {
		return 0, ErrParticipantNotFound
	}
	return p.ID, nil
}
