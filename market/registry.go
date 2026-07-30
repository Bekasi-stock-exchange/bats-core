// Package market is the shared kernel between the order and orderbook domains:
// the per-emiten matching engines, the lock that serializes them, the master-data
// directory, and the book-state fan-out hub.
//
// It exists so that neither domain has to import the other. The order domain
// submits through the Registry and publishes to the Hub; the orderbook domain
// reads snapshots from the Registry and subscribes to the Hub. Both depend on
// market, and market depends on nothing above it.
//
// The matching rules live one level up, in package engine, rather than under
// market. That placement is honest about the dependency graph: engine.Order and
// engine.Trade are domain types the order package speaks directly, so engine is
// a shared kernel in its own right, not a private detail of market. What market
// adds on top is everything engine deliberately omits — the lock that serializes
// matching, the master-data directory, and the book-state fan-out.
package market

import (
	"errors"
	"sync"

	"bekasi-automatic-trading-system/engine"
)

// ErrUnknownEmiten is returned when no engine exists for an emiten id. Callers
// resolve emiten codes through Directory first, so this indicates an internal
// inconsistency rather than bad input.
var ErrUnknownEmiten = errors.New("market: unknown emiten id")

// ErrInsufficientShares rejects a sell the broker cannot cover once its resting
// sell orders are taken into account. It is client error, not a server fault.
var ErrInsufficientShares = errors.New("market: insufficient shares")

// ErrInsufficientBalance rejects a buy the broker cannot cover once its resting
// buy orders are taken into account. It is client error, not a server fault.
var ErrInsufficientBalance = errors.New("market: insufficient balance")

// ErrEmitenInactive rejects an order for a suspended instrument. The book stays
// readable; only new orders are refused.
var ErrEmitenInactive = errors.New("market: emiten is not active")

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
	seq   *engine.Sequencer
	pos   *positions
}

// book pairs an engine with its emiten code and trading status, so a snapshot can
// be labelled and an order gated without a second lookup through Directory.
type book struct {
	kode   string
	active bool
	engine *engine.Engine
}

// NewRegistry builds one engine per emiten, all drawing sequence numbers from
// seq so order and trade Seq stay globally unique and monotonic across emiten.
//
// holdings seeds the share ledger from broker_assets_list and wallets seeds the
// cash ledger from broker_wallet. Orders still open when the process last
// stopped are not restored here — call RestoreOpenOrders once the registry is
// built, so their reservations land on top of these opening balances.
func NewRegistry(emitens []Emiten, holdings []Holding, wallets []Wallet, seq *engine.Sequencer) *Registry {
	books := make(map[int64]*book, len(emitens))
	for _, e := range emitens {
		books[e.ID] = newBook(e, seq)
	}

	pos := newPositions()
	for _, h := range holdings {
		pos.addHeld(h.ParticipantID, h.EmitenID, h.AmountShared)
	}
	for _, w := range wallets {
		pos.addCash(w.ParticipantID, w.Balance)
	}
	return &Registry{books: books, seq: seq, pos: pos}
}

// RestoreOpenOrders repopulates every book from orders that were still open
// when the process last stopped, and re-commits the reservations they hold
// against the share and cash ledgers.
//
// Without this, the in-memory book starts empty on every restart while the
// database still records those orders as open. The ledgers would then treat
// their shares and cash as fully available, letting a broker oversell or
// overbuy past what is actually still promised to a resting order — a
// mismatch that surfaces later as a database constraint violation when the
// stale reservation and a new trade collide, rather than as the clean 400 the
// availability check is supposed to produce.
//
// orders must be sorted by Seq ascending: that is the order they were
// originally inserted in, and inserting them in any other order would still
// produce a correctly *sorted* book (insertBid/insertAsk sort on every call),
// but ties in the Sequencer's next value would no longer match what a fresh
// process would have assigned, since it continues from the highest persisted
// Seq.
//
// Takes the lock itself: called once at startup, before the registry serves
// any request, or standalone by a caller that is not already holding it.
// Rebuild calls the unlocked half directly because it already holds the lock.
func (r *Registry) RestoreOpenOrders(orders []OpenOrder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreOpenOrdersLocked(orders)
}

// restoreOpenOrdersLocked is RestoreOpenOrders' body. Caller must hold r.mu.
func (r *Registry) restoreOpenOrdersLocked(orders []OpenOrder) {
	for _, o := range orders {
		b, ok := r.books[o.EmitenID]
		if !ok {
			continue // emiten no longer exists; nothing to restore into
		}

		eo := &engine.Order{
			ID:            o.ID,
			EmitenID:      o.EmitenID,
			ParticipantID: o.ParticipantID,
			Side:          engine.Side(o.Side),
			Type:          engine.Type(o.Type),
			Price:         o.Price,
			Qty:           o.Qty,
			Remaining:     o.Remaining,
			Status:        engine.Open,
			Seq:           o.Seq,
		}
		b.engine.Restore(eo)

		if eo.Side == engine.Sell {
			r.pos.addReserved(o.ParticipantID, o.EmitenID, o.Remaining)
		} else {
			r.pos.addCashReserved(o.ParticipantID, o.Remaining*o.Price)
		}
	}
}

func newBook(e Emiten, seq *engine.Sequencer) *book {
	return &book{
		kode:   e.Kode,
		active: e.IsActive,
		engine: engine.NewEngineWithSequencer(e.ID, seq),
	}
}

// AddBook registers a newly created emiten with an empty order book, which is the
// correct state for a just-listed instrument. It shares the same sequencer, so
// sequence numbers stay globally unique.
func (r *Registry) AddBook(e Emiten) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.books[e.ID]; exists {
		return
	}
	r.books[e.ID] = newBook(e, r.seq)
}

// Holding reports a broker's current holding of one emiten.
func (r *Registry) Holding(participantID, emitenID int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pos.Held(participantID, emitenID)
}

// Submit runs an order through its emiten's matching engine, then invokes
// persist with the resulting trades before applying any of them to the share
// and cash ledgers.
//
// Everything happens under a single lock acquisition — the availability check,
// matching, the persist callback, the ledger bookkeeping, and the snapshot.
// That is what makes the check trustworthy: there is no window in which
// another goroutine could spend the same shares, and the returned state is
// exactly the book that produced those trades. The engine mutates o in place:
// Seq, Remaining and Status are set on return.
//
// The ledger (held/reserved shares, cash/cashReserved balances) is deliberately
// applied only after persist returns success. Matching itself cannot be
// undone once run — the engine has already popped consumed resting orders and
// possibly inserted o into the book — but that is a queue-ordering fact, not
// money or shares, and staying wrong about it until the next successful
// submission or restart is a far cheaper mistake than a ledger entry that
// silently disagrees with the database. If persist fails, the ledger is
// untouched and will not drift: the only cost is that Submit's caller sees an
// error for an order the book already reflects, which SaveExecution's own
// transaction guarantees never happened at the database's expense.
func (r *Registry) Submit(o *engine.Order, persist func([]engine.Trade) error) ([]engine.Trade, BookState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[o.EmitenID]
	if !ok {
		return nil, BookState{}, ErrUnknownEmiten
	}
	if !b.active {
		return nil, BookState{}, ErrEmitenInactive
	}

	// A sell must be covered by shares that are not already promised to this
	// broker's resting sell orders. Checked before matching, because matching
	// cannot be undone.
	if o.Side == engine.Sell {
		if o.Qty > r.pos.available(o.ParticipantID, o.EmitenID) {
			return nil, BookState{}, ErrInsufficientShares
		}
	}

	// A buy must be covered by cash that is not already promised to this broker's
	// resting buy orders. A market buy carries no price, so it is checked against
	// the best opposing price instead — the worst price it could possibly pay.
	if o.Side == engine.Buy {
		cost, ok := b.engine.EstimateCost(o)
		if ok && cost > r.pos.availableCash(o.ParticipantID) {
			return nil, BookState{}, ErrInsufficientBalance
		}
	}

	trades := b.engine.Submit(o)

	if err := persist(trades); err != nil {
		return nil, BookState{}, err
	}

	r.applyTrades(o, trades)

	// A sell order that rests keeps its remainder committed until it fills.
	if o.Side == engine.Sell && o.Status == engine.Open && o.Remaining > 0 {
		r.pos.addReserved(o.ParticipantID, o.EmitenID, o.Remaining)
	}
	// A buy order that rests keeps its remaining cost committed until it fills.
	if o.Side == engine.Buy && o.Status == engine.Open && o.Remaining > 0 {
		r.pos.addCashReserved(o.ParticipantID, o.Remaining*o.Price)
	}

	return trades, b.state(), nil
}

// applyTrades moves shares between the two sides of every execution. Caller must
// hold r.mu.
//
// When the sell side was the resting order, its reservation is released by the
// same quantity: the shares have now actually left, so they must not be counted
// as committed as well as gone.
func (r *Registry) applyTrades(incoming *engine.Order, trades []engine.Trade) {
	for _, t := range trades {
		r.pos.addHeld(t.BuyParticipantID, t.EmitenID, t.Qty)
		r.pos.addHeld(t.SellParticipantID, t.EmitenID, -t.Qty)

		cost := t.Qty * t.Price
		r.pos.addCash(t.BuyParticipantID, -cost)
		r.pos.addCash(t.SellParticipantID, cost)

		if t.SellOrderID != incoming.ID {
			r.pos.addReserved(t.SellParticipantID, t.EmitenID, -t.Qty)
		}
		if t.BuyOrderID != incoming.ID {
			// The resting buy order's reservation was sized at its own limit price,
			// which may differ from the price it actually traded at (price-time
			// priority fills against the resting order's price, but a later,
			// better-priced match is still possible in principle) — releasing the
			// same notional it reserved keeps the ledger exactly balanced.
			r.pos.addCashReserved(t.BuyParticipantID, -cost)
		}
	}
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
