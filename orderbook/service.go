package orderbook

import (
	"errors"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

// ErrEmitenNotFound means the requested emiten code is not listed.
var ErrEmitenNotFound = errors.New("orderbook: emiten not found")

// Service reads book state. It holds no lock of its own — serialization is the
// Registry's job.
type Service struct {
	dir *market.Directory
	reg *market.Registry
	hub *market.Hub
}

// NewService wires the read side against the market kernel.
func NewService(dir *market.Directory, reg *market.Registry, hub *market.Hub) *Service {
	return &Service{dir: dir, reg: reg, hub: hub}
}

// Book returns the current book state for one emiten code, or ErrEmitenNotFound.
func (s *Service) Book(kode string) (market.BookState, error) {
	em, ok := s.dir.Emiten(kode)
	if !ok {
		return market.BookState{}, ErrEmitenNotFound
	}
	return s.reg.Snapshot(em.ID)
}

// Books returns one page of book states ordered by emiten code, plus the total
// number of emiten available for the pagination envelope.
func (s *Service) Books(page, limit int) ([]market.BookState, int) {
	emitens := s.dir.Emitens()
	total := len(emitens)

	start, end := httpx.Slice(page, limit, total)
	ids := make([]int64, 0, end-start)
	for _, em := range emitens[start:end] {
		ids = append(ids, em.ID)
	}
	return s.reg.SnapshotAll(ids), total
}

// Emiten resolves an emiten code, or returns ErrEmitenNotFound.
func (s *Service) Emiten(kode string) (market.Emiten, error) {
	em, ok := s.dir.Emiten(kode)
	if !ok {
		return market.Emiten{}, ErrEmitenNotFound
	}
	return em, nil
}

// Subscribe registers for book updates on an emiten. Callers must Unsubscribe.
//
// Subscribing is separate from taking the initial snapshot so a caller can
// subscribe first and therefore miss no update in the gap between the two.
func (s *Service) Subscribe(emitenID int64) *market.Subscription {
	return s.hub.Subscribe(emitenID)
}

// Unsubscribe releases a subscription.
func (s *Service) Unsubscribe(emitenID int64, sub *market.Subscription) {
	s.hub.Unsubscribe(emitenID, sub)
}

// Snapshot returns the current state of one book by emiten id.
func (s *Service) Snapshot(emitenID int64) (market.BookState, error) {
	return s.reg.Snapshot(emitenID)
}
