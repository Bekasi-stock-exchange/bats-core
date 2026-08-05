package orderbook

import (
	"errors"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

// ErrEmitenNotFound means the requested emiten code is not listed.
var ErrEmitenNotFound = errors.New("orderbook: emiten not found")

// Holds no lock of its own — serialization is the Registry's job.
type Service struct {
	dir *market.Directory
	reg *market.Registry
	hub *market.Hub
}

func NewService(dir *market.Directory, reg *market.Registry, hub *market.Hub) *Service {
	return &Service{dir: dir, reg: reg, hub: hub}
}

func (s *Service) Book(kode string) (market.BookState, error) {
	em, ok := s.dir.Emiten(kode)
	if !ok {
		return market.BookState{}, ErrEmitenNotFound
	}
	return s.reg.Snapshot(em.ID)
}

// Ordered by emiten code. The total is the number of emiten available, for the
// pagination envelope.
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

func (s *Service) Emiten(kode string) (market.Emiten, error) {
	em, ok := s.dir.Emiten(kode)
	if !ok {
		return market.Emiten{}, ErrEmitenNotFound
	}
	return em, nil
}

// Callers must Unsubscribe.
//
// Subscribing is separate from taking the initial snapshot so a caller can
// subscribe first and therefore miss no update in the gap between the two.
func (s *Service) Subscribe(emitenID int64) *market.Subscription {
	return s.hub.Subscribe(emitenID)
}

func (s *Service) Unsubscribe(emitenID int64, sub *market.Subscription) {
	s.hub.Unsubscribe(emitenID, sub)
}

func (s *Service) Snapshot(emitenID int64) (market.BookState, error) {
	return s.reg.Snapshot(emitenID)
}
