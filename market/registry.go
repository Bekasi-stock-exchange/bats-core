// Package market is the shared kernel between the order and orderbook domains:
// the per-emiten matching engines, the lock that serializes them, the master-data
// directory, and the book-state fan-out hub.
//
// It exists so that neither domain has to import the other. The order domain
// submits through the Registry and publishes to the Hub; the orderbook domain
// reads snapshots from the Registry and subscribes to the Hub. Both depend on
// market; market depends only on engine.
package market

import (
	"errors"
	"sync"

	"bekasi-automatic-trading-system/market/engine"
)

// ErrUnknownEmiten is returned when no engine exists for an emiten id. Callers
// resolve emiten codes through Directory first, so this indicates an internal
// inconsistency rather than bad input.
var ErrUnknownEmiten = errors.New("market: unknown emiten id")

// Registry owns every order book and the lock that serializes access to them.
//
// Concurrency model (spec §6): only one goroutine may touch a given order book at
// a time, and matching must be sequential and deterministic. A single mutex
// guards every book access — matching and snapshotting alike — so price-time
// priority stays deterministic. There is no per-order locking and no parallel
// matching.
//
// The lock is deliberately private and every operation that touches a book is a
// method here. Previously each call site took the mutex itself and the invariant
// survived only as a comment; now it cannot be forgotten.
type Registry struct {
	mu    sync.Mutex
	books map[int64]*book
}

// book pairs an engine with its emiten code, so a snapshot can be labelled
// without a second lookup through Directory.
type book struct {
	kode   string
	engine *engine.Engine
}

// NewRegistry builds one engine per emiten, all drawing sequence numbers from
// seq so order and trade Seq stay globally unique and monotonic across emiten.
func NewRegistry(emitens []Emiten, seq *engine.Sequencer) *Registry {
	books := make(map[int64]*book, len(emitens))
	for _, e := range emitens {
		books[e.ID] = &book{kode: e.Kode, engine: engine.NewEngineWithSequencer(e.ID, seq)}
	}
	return &Registry{books: books}
}

// Submit runs an order through its emiten's matching engine and returns the
// resulting trades together with the book state immediately afterwards.
//
// Matching and snapshotting happen under a single lock acquisition, so the
// returned state is exactly the book that produced those trades. The engine
// mutates o in place: Seq, Remaining and Status are set on return.
func (r *Registry) Submit(o *engine.Order) ([]engine.Trade, BookState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[o.EmitenID]
	if !ok {
		return nil, BookState{}, ErrUnknownEmiten
	}
	trades := b.engine.Submit(o)
	return trades, b.state(), nil
}

// Snapshot returns the current book state for one emiten.
func (r *Registry) Snapshot(emitenID int64) (BookState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[emitenID]
	if !ok {
		return BookState{}, ErrUnknownEmiten
	}
	return b.state(), nil
}

// SnapshotAll returns the book state for each of the given emiten ids, in order.
// Unknown ids are skipped. One lock acquisition covers the whole set.
func (r *Registry) SnapshotAll(emitenIDs []int64) []BookState {
	out := make([]BookState, 0, len(emitenIDs))

	r.mu.Lock()
	defer r.mu.Unlock()

	for _, id := range emitenIDs {
		if b, ok := r.books[id]; ok {
			out = append(out, b.state())
		}
	}
	return out
}

// state aggregates the book into price levels. Caller must hold r.mu.
func (b *book) state() BookState {
	bk := b.engine.Book()
	return BookState{
		Emiten: b.kode,
		Bids:   aggregate(bk.Bids),
		Asks:   aggregate(bk.Asks),
	}
}
