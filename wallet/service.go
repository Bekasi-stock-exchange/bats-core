package wallet

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

// ErrParticipantNotFound means the requested broker code is not registered.
var ErrParticipantNotFound = errors.New("wallet: participant not found")

// ValidationError is a rejected request: well-formed, but breaking a business
// rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Service reads broker cash balances and applies administrative adjustments to
// them.
type Service struct {
	dir  *market.Directory
	repo Repository
	reg  *market.Registry
}

// NewService wires the wallet domain to the directory, the market kernel, and
// its repository.
func NewService(dir *market.Directory, reg *market.Registry, repo Repository) *Service {
	return &Service{dir: dir, reg: reg, repo: repo}
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

// Adjust credits or debits a broker's wallet and returns the balance it settles
// at.
//
// The order of the three steps is the whole design. The debit is checked
// against the in-memory ledger's *available* cash first, because cash already
// promised to resting buy orders is spent as far as matching is concerned:
// checking against the stored balance instead would let an operator withdraw
// money those orders are counting on, and the shortfall would only surface as a
// CHECK (balance >= 0) violation when they fill. The row is then written, and
// only once that write is durable does the ledger move — crediting first would
// let a broker spend money the database never recorded.
//
// AdjustCash re-checks availability under the registry's own lock rather than
// trusting the earlier read, so a buy order that rests in the gap between the
// two cannot have its cash withdrawn from under it. That leaves one ordering
// cost: a debit rejected there has already been persisted, so it is reversed
// before returning. The reversal is a plain credit and cannot itself be
// refused.
func (s *Service) Adjust(ctx context.Context, req AdjustRequest) (Record, error) {
	kode := strings.TrimSpace(req.Participant)
	if kode == "" {
		return Record{}, invalid("participant is required")
	}
	if req.Amount == 0 {
		return Record{}, invalid("amount must not be 0")
	}

	participantID, err := s.ResolveParticipant(kode)
	if err != nil {
		return Record{}, err
	}

	if req.Amount < 0 && -req.Amount > s.reg.AvailableCash(participantID) {
		return Record{}, invalid(
			"insufficient available balance: %s may withdraw at most %d",
			kode, s.reg.AvailableCash(participantID))
	}

	rec, err := s.repo.AdjustWallet(ctx, participantID, req.Amount)
	if err != nil {
		return Record{}, err
	}

	if _, err := s.reg.AdjustCash(participantID, req.Amount); err != nil {
		if _, rerr := s.repo.AdjustWallet(ctx, participantID, -req.Amount); rerr != nil {
			return Record{}, fmt.Errorf(
				"wallet: reverse adjustment for %s after rejected debit: %w", kode, rerr)
		}
		return Record{}, invalid(
			"insufficient available balance: %s may withdraw at most %d",
			kode, s.reg.AvailableCash(participantID))
	}
	return rec, nil
}

// ResolveParticipant maps a broker code to its id, for the admin filter.
func (s *Service) ResolveParticipant(kode string) (int64, error) {
	p, ok := s.dir.Participant(kode)
	if !ok {
		return 0, ErrParticipantNotFound
	}
	return p.ID, nil
}
