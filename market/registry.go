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
	"time"

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

// ErrEmitenHalted rejects an order for an instrument whose circuit breaker has
// tripped. Distinct from ErrEmitenInactive: inactive is an administrative state
// an operator sets and clears, while a halt is automatic, temporary, and ends by
// itself. A client that sees this one should retry after the halt expires; a
// client that sees the other should not.
var ErrEmitenHalted = errors.New("market: trading halted")

// ErrOutsideBand rejects an order priced beyond the session's permitted range —
// auto-rejection, ARA above and ARB below. The order never reaches the book, so
// the price it asked for never becomes one anything else can match against.
var ErrOutsideBand = errors.New("market: price outside permitted band")

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

	// breaker supplies the band threshold and halt duration in force. Optional:
	// nil disables auto-rejection and the circuit breaker entirely, which is what
	// the engine and registry tests expect and what a deployment without the
	// config domain wired up falls back to.
	breaker BreakerPolicy

	// halts is notified when a breaker trips or a halt expires, so the halt can
	// be persisted and announced. Optional, and never called while r.mu is held.
	halts HaltObserver

	// now is the clock, injected so tests can drive a halt to its expiry without
	// sleeping through it. nil means time.Now.
	now func() time.Time
}

// BreakerPolicy supplies the circuit breaker configuration the registry
// enforces.
//
// Declared here rather than importing marketconfig: market is the shared kernel
// and sits below every domain package, so depending on one would invert the
// layering the whole package graph rests on. Satisfied by an adapter over
// marketconfig.Cache.
type BreakerPolicy interface {
	// EmitenBandBPS is how far an instrument may move from its session reference
	// before orders are auto-rejected and, at the edge, trading halts. In basis
	// points.
	EmitenBandBPS() int64

	// HaltDuration is how long a triggered halt lasts.
	HaltDuration() time.Duration
}

// HaltObserver is notified when an instrument's trading status changes.
//
// Implementations must not call back into the Registry: they are invoked from
// the submit path, and the registry's lock is not reentrant. They are called
// after the lock is released, so an implementation that needs registry state
// may read it — but must expect it to have moved on.
type HaltObserver interface {
	// Halted announces that a breaker tripped. price is what printed, reference
	// the anchor it was measured against, and until when trading may resume.
	Halted(emitenID int64, price, reference int64, until time.Time)

	// Resumed announces that a halt expired and the book is open again.
	Resumed(emitenID int64)
}

// WithBreaker installs the circuit breaker policy and the observer notified when
// it trips.
//
// A setter rather than a constructor parameter, matching how the order service
// takes its trade observer: the registry is built early in the composition root,
// from master data, while the config cache and the persistence that records a
// halt are wired later. Either argument may be nil.
func (r *Registry) WithBreaker(p BreakerPolicy, o HaltObserver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.breaker = p
	r.halts = o
}

// clock returns the time source, defaulting to time.Now.
func (r *Registry) clock() time.Time {
	if r.now != nil {
		return r.now()
	}
	return time.Now()
}

// book pairs an engine with its emiten code and trading status, so a snapshot can
// be labelled and an order gated without a second lookup through Directory.
//
// reference and haltedUntil live here, beside the engine, rather than in a table
// of their own. Both are read on the submit path under the registry's lock, and
// that is the point: the band check, the halt check, matching, and the halt that
// a resulting trade may trigger all happen in one critical section, so no order
// can slip through between a breaker tripping and the book closing.
type book struct {
	kode   string
	active bool
	engine *engine.Engine

	// reference is the session anchor the price band is measured from. Zero
	// means the instrument has no anchor yet — never traded, never priced — and
	// no band applies.
	reference int64

	// haltedUntil is when an active halt expires. Zero means not halted.
	haltedUntil time.Time
}

// halted reports whether the book is halted as of now. Caller must hold r.mu.
//
// Compared against the clock on each read rather than cleared by a timer,
// because those two can disagree: a timer that has fired but whose goroutine has
// not yet been scheduled would leave a book that is logically open still
// rejecting orders. Deriving the answer from the deadline makes the halt end at
// exactly the instant it is supposed to, and leaves the timer responsible only
// for the side effects — persisting the resume and announcing it.
func (b *book) halted(now time.Time) bool {
	return !b.haltedUntil.IsZero() && now.Before(b.haltedUntil)
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
// any request.
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
	b := &book{
		kode:   e.Kode,
		active: e.IsActive,
		engine: engine.NewEngineWithSequencer(e.ID, seq),
	}
	if e.SessionReference != nil {
		b.reference = *e.SessionReference
	}
	return b
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

// ActivateBook opens a dormant instrument's book for matching.
//
// The active flag is copied onto the book at registration, so flipping the
// database row and the directory entry is not enough — Submit consults this copy,
// and without this call an activated instrument would keep rejecting orders with
// ErrEmitenInactive. Registering the book at creation and only opening it here is
// deliberate: the book must exist to be readable while the instrument is dormant.
func (r *Registry) ActivateBook(emitenID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.books[emitenID]; ok {
		b.active = true
	}
}

// CreditShares adds shares to a broker's holding outside of any trade.
//
// It exists for primary-market issuance: an IPO allocation puts shares into a
// participant's hands without a matching sell side, which applyTrades — built
// around a buyer and a seller exchanging an existing holding — cannot express.
//
// The caller must have already persisted the credit. This only moves the
// in-memory ledger into agreement with the database; without it the underwriter's
// first sell is refused for insufficient shares even though the row says otherwise.
func (r *Registry) CreditShares(participantID, emitenID, shares int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pos.addHeld(participantID, emitenID, shares)
}

// Holding reports a broker's current holding of one emiten.
func (r *Registry) Holding(participantID, emitenID int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pos.Held(participantID, emitenID)
}

// AdjustCash moves a broker's cash balance outside of any trade, and reports the
// balance it settles at.
//
// It exists for administrative funding: an operator crediting or debiting a
// broker has no counterparty, which applyTrades — built around a buyer and a
// seller exchanging cash for shares — cannot express.
//
// A debit is checked against available cash, not the balance: cash already
// promised to resting buy orders is spent as far as this ledger is concerned,
// and letting an operator withdraw it would leave those orders unfunded and
// surface later as a CHECK (balance >= 0) violation when they fill, rather than
// as the clean rejection returned here. A credit is never refused.
//
// Persisting is the caller's job and must come first: this only moves the
// in-memory ledger, and crediting before the write is durable would let a
// broker spend money the database never recorded.
func (r *Registry) AdjustCash(participantID, delta int64) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if delta < 0 && -delta > r.pos.availableCash(participantID) {
		return 0, ErrInsufficientBalance
	}
	r.pos.addCash(participantID, delta)
	return r.pos.Cash(participantID), nil
}

// AvailableCash reports what a broker may still spend: its balance, minus what
// its resting buy orders have already promised away.
func (r *Registry) AvailableCash(participantID int64) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pos.availableCash(participantID)
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
// The ledger (held/reserved shares, cash/cashReserved balances) is applied
// only after persist returns success, and a failed persist unwinds the
// matching pass itself (SubmitAtomic), so on error neither the book nor the
// ledger has moved. Both halves matter: a book entry the database never saw
// is a phantom that every later match trips over — its trades foreign-key to
// a missing order row, and its unreserved shares let a seller go negative at
// the balance check — and none of that can self-heal short of a restart.
func (r *Registry) Submit(o *engine.Order, persist func([]engine.Trade) error) ([]engine.Trade, BookState, error) {
	trades, state, halt, err := r.submitLocked(o, persist)

	// Announced after the lock is released. The observer persists the halt and
	// broadcasts it, neither of which belongs inside the critical section that
	// every other emiten's matching is waiting on.
	if halt != nil && r.halts != nil {
		r.halts.Halted(halt.emitenID, halt.price, halt.reference, halt.until)
	}
	return trades, state, err
}

// haltTrigger records a breaker that tripped during a matching pass, so Submit
// can announce it once the lock is released.
type haltTrigger struct {
	emitenID  int64
	price     int64
	reference int64
	until     time.Time
}

// submitLocked is Submit's body. It returns the halt it triggered, if any,
// rather than announcing it itself — the announcement must happen outside r.mu.
func (r *Registry) submitLocked(o *engine.Order, persist func([]engine.Trade) error) ([]engine.Trade, BookState, *haltTrigger, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[o.EmitenID]
	if !ok {
		return nil, BookState{}, nil, ErrUnknownEmiten
	}
	if !b.active {
		return nil, BookState{}, nil, ErrEmitenInactive
	}

	now := r.clock()

	// Auto-rejection is checked before the halt itself. A limit order priced
	// outside the session band never reaches the book regardless of whether the
	// instrument happens to be halted — the client's mistake is the price it
	// asked for, and that is the error worth reporting. A market order carries no
	// price and is not checked here; what it pays is a resting order's price, and
	// that was validated on its own way in, so it falls through to the halt check
	// below like every other order that passes the band.
	band, hasBand := r.bandLocked(b)
	if hasBand && o.Type == engine.Limit && !band.Allows(o.Price) {
		return nil, BookState{}, nil, ErrOutsideBand
	}

	if b.halted(now) {
		return nil, BookState{}, nil, ErrEmitenHalted
	}

	// A sell must be covered by shares that are not already promised to this
	// broker's resting sell orders. Checked before matching, so a short sell is
	// a clean rejection rather than a matching pass that has to be unwound.
	if o.Side == engine.Sell {
		if o.Qty > r.pos.available(o.ParticipantID, o.EmitenID) {
			return nil, BookState{}, nil, ErrInsufficientShares
		}
	}

	// A buy must be covered by cash that is not already promised to this broker's
	// resting buy orders. A market buy carries no price, so it is checked against
	// the best opposing price instead — the worst price it could possibly pay.
	if o.Side == engine.Buy {
		cost, ok := b.engine.EstimateCost(o)
		if ok && cost > r.pos.availableCash(o.ParticipantID) {
			return nil, BookState{}, nil, ErrInsufficientBalance
		}
	}

	trades, err := b.engine.SubmitAtomic(o, persist)
	if err != nil {
		return nil, BookState{}, nil, err
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

	// The circuit breaker, measured against trades that actually printed rather
	// than against what an order asked for. Auto-rejection above has already
	// refused anything outside the band, so a price can only reach the edge
	// legally — which is exactly the event worth halting on.
	//
	// The halt lands before the book state is captured, so the snapshot this
	// returns already reflects a halted instrument rather than one that is about
	// to be.
	var halt *haltTrigger
	if hasBand && len(trades) > 0 {
		if last := trades[len(trades)-1].Price; band.AtLimit(last) {
			until := now.Add(r.breaker.HaltDuration())
			b.haltedUntil = until
			halt = &haltTrigger{
				emitenID:  o.EmitenID,
				price:     last,
				reference: band.Reference,
				until:     until,
			}
		}
	}

	return trades, b.state(), halt, nil
}

// bandLocked returns the price band in force for a book, and whether one applies
// at all. Caller must hold r.mu.
//
// No band applies when the breaker is unconfigured or the instrument has no
// session reference — a freshly listed instrument that never had an offering
// price has nothing to measure 30% against, and inventing an anchor would either
// reject every order or bound the price to a number nobody chose.
func (r *Registry) bandLocked(b *book) (Band, bool) {
	if r.breaker == nil || b.reference <= 0 {
		return Band{}, false
	}
	bps := r.breaker.EmitenBandBPS()
	if bps <= 0 {
		return Band{}, false
	}
	return NewBand(b.reference, bps), true
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

// ErrOrderNotFound rejects a cancel for an order that is not resting in any
// book — it never existed, already filled, or was already cancelled. Client
// error: only a resting order can be withdrawn.
var ErrOrderNotFound = errors.New("market: order not resting")

// ErrNotOrderOwner rejects a cancel issued by a broker that does not own the
// order. Withdrawing another broker's liquidity is not a permission any
// participant has.
var ErrNotOrderOwner = errors.New("market: order belongs to another participant")

// Cancelled is a withdrawn order as the caller sees it: its final state, and
// the book it left behind.
type Cancelled struct {
	Order *engine.Order
	State BookState
}

// Cancel withdraws a resting order from its book and releases the shares or
// cash it had promised away, invoking persist before either takes effect.
//
// participantID is the broker asking. An order may only be cancelled by the
// broker that placed it, checked here rather than in the service because this
// is the only layer that holds the resting order and can see who owns it.
//
// Everything happens under one lock acquisition — the lookup, the ownership
// check, the removal, the persist callback, and the ledger release — for the
// same reason Submit does: a cancel that overlapped a matching pass could
// release a reservation against a quantity that had just traded away, leaving
// the ledger claiming shares the broker no longer has. Serializing the two
// makes the remainder this reads the remainder that is actually still resting.
//
// The ledger is released only after persist succeeds, and a failed persist puts
// the order back in the book at its original price and Seq (CancelAtomic), so
// on error neither the book nor the ledger has moved and the caller may retry.
//
// A halted instrument is deliberately not blocked. A halt stops new orders from
// arriving; it does not trap the orders already resting — a broker that cannot
// withdraw its own liquidity during a halt is exposed for the halt's whole
// duration to a reopening it cannot react to.
func (r *Registry) Cancel(emitenID, orderID, participantID int64, persist func(*engine.Order) error) (Cancelled, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[emitenID]
	if !ok {
		return Cancelled{}, ErrUnknownEmiten
	}

	// Ownership is checked before the removal, so a cancel aimed at someone
	// else's order never disturbs the book even momentarily.
	resting := b.engine.Find(orderID)
	if resting == nil {
		return Cancelled{}, ErrOrderNotFound
	}
	if resting.ParticipantID != participantID {
		return Cancelled{}, ErrNotOrderOwner
	}

	// Captured before the cancel: CancelAtomic leaves Remaining untouched, but
	// reading it up front keeps the release sized to what this call actually
	// withdrew rather than to whatever the order holds by the time we get here.
	remaining := resting.Remaining

	o, found, err := b.engine.CancelAtomic(orderID, persist)
	if err != nil {
		return Cancelled{}, err
	}
	if !found {
		return Cancelled{}, ErrOrderNotFound
	}

	// Release what the order had promised away. A sell reserved shares; a buy
	// reserved cash at its own limit price — the same notional Submit committed
	// when it rested, so the ledger nets back to exactly zero.
	if o.Side == engine.Sell {
		r.pos.addReserved(o.ParticipantID, o.EmitenID, -remaining)
	} else {
		r.pos.addCashReserved(o.ParticipantID, -remaining*o.Price)
	}

	return Cancelled{Order: o, State: b.state()}, nil
}

// HaltState is an instrument's halt as seen from outside: whether it is halted
// right now, and when it resumes.
type HaltState struct {
	Halted    bool
	ResumesAt time.Time
}

// Halt reports the halt state of one emiten.
func (r *Registry) Halt(emitenID int64) (HaltState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[emitenID]
	if !ok {
		return HaltState{}, ErrUnknownEmiten
	}
	if !b.halted(r.clock()) {
		return HaltState{}, nil
	}
	return HaltState{Halted: true, ResumesAt: b.haltedUntil}, nil
}

// HaltUntil places an instrument under a halt that expires at until, and reports
// whether the emiten exists.
//
// It exists for two callers: the operator who halts an instrument by hand, and
// startup, which restores halts that were still running when the process
// stopped. A halt that did not survive a restart would reopen the instrument
// early — at exactly the moment least likely to be watched.
func (r *Registry) HaltUntil(emitenID int64, until time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[emitenID]
	if !ok {
		return false
	}
	b.haltedUntil = until
	return true
}

// Resume lifts a halt early, reporting whether the emiten exists. An operator's
// override; an untouched halt ends on its own.
func (r *Registry) Resume(emitenID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[emitenID]
	if !ok {
		return false
	}
	b.haltedUntil = time.Time{}
	return true
}

// ExpireHalts clears every halt whose deadline has passed and returns the emiten
// ids that resumed.
//
// This is the whole of the time-based machinery, and it is deliberately a poll
// rather than a timer per halt. The registry's concurrency model is one lock
// around every book, and matching must stay sequential and deterministic; a
// goroutine per halt firing on its own schedule would reopen a book from
// whatever thread the runtime happened to pick, which is precisely the race that
// model exists to prevent. One caller, on one interval, taking the lock once, is
// both simpler and the only shape that keeps the guarantee.
//
// Note that a halt has already stopped gating orders by the time this runs:
// book.halted compares against the clock, so the instrument reopens at its
// deadline whether or not this has been called yet. What this adds is the side
// effects — clearing the state, and giving the caller the list to persist and
// announce.
func (r *Registry) ExpireHalts() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.clock()
	var resumed []int64
	for id, b := range r.books {
		if b.haltedUntil.IsZero() || now.Before(b.haltedUntil) {
			continue
		}
		b.haltedUntil = time.Time{}
		resumed = append(resumed, id)
	}
	return resumed
}

// SetReference updates the session anchor the price band is measured from, and
// reports whether the emiten exists.
//
// Called at the session boundary, and when an instrument is activated at its
// offering price. Not called on every trade: an anchor that moved with the
// market would let the band walk, which is the failure the band exists to
// prevent.
func (r *Registry) SetReference(emitenID, price int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[emitenID]
	if !ok {
		return false
	}
	b.reference = price
	return true
}

// Band returns the price range in force for one emiten, and whether one applies.
func (r *Registry) Band(emitenID int64) (Band, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	b, ok := r.books[emitenID]
	if !ok {
		return Band{}, false
	}
	return r.bandLocked(b)
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
