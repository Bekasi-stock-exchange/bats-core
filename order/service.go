package order

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"bekasi-automatic-trading-system/engine"
	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/httpx"
)

// ValidationError is a rejected submission: the input is well-formed JSON but
// violates a business rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// SubmitCommand is a validated-on-entry request to submit an order. Codes are
// resolved to master data by the service, not by the caller.
type SubmitCommand struct {
	Emiten      string
	Participant string
	Side        string
	Type        string
	Price       int64
	Qty         int64
}

// Result is the outcome of a matching pass: the submitted order at its final
// state, and the trades it produced in execution order.
type Result struct {
	Order  *engine.Order
	Trades []engine.Trade
}

// Limits supplies the exchange-wide trading rules an order is validated against.
//
// Declared here as an interface rather than taking the config package's cache
// directly, so this package depends on the one rule it enforces instead of on
// another domain. Satisfied by marketconfig.Cache.
type Limits interface {
	// MinPrice is the lowest price a limit order may carry, in rupiah.
	MinPrice() int64
}

// TradeObserver is notified after a matching pass has committed trades.
//
// Declared here as an interface rather than taking the index package directly,
// so this package depends on the one thing it announces rather than on another
// domain. Satisfied by index.Notifier.
//
// Implementations must not block: they are called on the submit path, and the
// exchange's matching latency is not theirs to spend.
type TradeObserver interface {
	// TradesExecuted announces that trades committed and prices have moved.
	TradesExecuted()
}

// Service accepts orders: validate, match, persist, publish.
type Service struct {
	dir    *market.Directory
	reg    *market.Registry
	hub    *market.Hub
	repo   Repository
	limits Limits

	// trades is notified after a committed matching pass, so derived market data
	// — the composite index — can refresh. Optional: nil means nothing observes,
	// which is what the engine tests and any wiring without an index expect.
	trades TradeObserver

	// submitMu serializes the id-reserve -> match -> persist sequence across every
	// emiten. Registry.Submit already serializes matching by itself, but its lock
	// is released before the database transaction runs — without a lock spanning
	// both, two concurrent submissions can match in-memory in one order (correct,
	// under Registry's own lock) and then race each other to commit, landing in
	// the database in the opposite order. When one of the two trades references
	// the other's not-yet-committed order as its resting side, that surfaces as a
	// foreign-key violation or a transient negative-balance constraint failure —
	// not a business rule the client broke, but the persisted order of events
	// disagreeing with the order matching actually produced them in. Holding this
	// for the whole reserve-match-persist sequence keeps both orders identical.
	submitMu sync.Mutex
}

// NewService wires the write side against the market kernel, the repository, and
// the exchange's trading limits.
func NewService(dir *market.Directory, reg *market.Registry, hub *market.Hub, repo Repository, limits Limits) *Service {
	return &Service{dir: dir, reg: reg, hub: hub, repo: repo, limits: limits}
}

// Observe registers the observer notified after each committed matching pass.
//
// A setter rather than a constructor parameter because the observer is optional
// and, in the composition root, is built after this service: the index service
// values the market the order path feeds, so requiring it up front would make
// the two mutually dependent at construction time.
func (s *Service) Observe(o TradeObserver) { s.trades = o }

// Submit validates an order, matches it, persists the outcome atomically, and
// publishes the resulting book state.
//
// Ordering is deliberate. The order id is reserved first because trades reference
// it. Matching happens next, which is where the engine assigns the sequence
// number that gives the order its time priority. Only then is anything written —
// as a single transaction, so the database never holds a partial outcome. The
// broadcast comes last, so subscribers are never told about a trade that failed
// to commit.
//
// A failed transaction does not leave the book divergent: the engine unwinds
// the whole matching pass when persist errors (engine.SubmitAtomic), so the
// in-memory book, the ledgers, and the database all still agree — the caller
// just sees the error and can retry.
//
// submitMu holds for the entire reserve-match-persist sequence, across every
// emiten — not just the match. Registry.Submit serializes matching on its own,
// but releases its lock before this function's database transaction runs; two
// concurrent calls could then match in one order but commit in the other,
// producing a trade that foreign-keys to an order not yet committed. See the
// field comment on submitMu for the full reasoning.
func (s *Service) Submit(ctx context.Context, cmd SubmitCommand) (Result, error) {
	em, part, side, typ, price, err := s.resolve(cmd)
	if err != nil {
		return Result{}, err
	}

	s.submitMu.Lock()
	defer s.submitMu.Unlock()

	id, err := s.repo.NextOrderID(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("order: reserve id: %w", err)
	}

	o := &engine.Order{
		ID:            id,
		EmitenID:      em.ID,
		ParticipantID: part.ID,
		Side:          side,
		Type:          typ,
		Price:         price,
		Qty:           cmd.Qty,
	}

	// persist runs inside Registry.Submit's lock, after matching but before any
	// of its effects reach the share/cash ledger — see the field comment on
	// submitMu and the doc comment on Registry.Submit for why the ordering
	// matters. o already carries its final Seq/Remaining/Status at this point,
	// because the engine sets them before returning control here.
	persist := func(trades []engine.Trade) error {
		if err := s.repo.SaveExecution(ctx, buildExecution(o, trades)); err != nil {
			return fmt.Errorf("order: save execution: %w", err)
		}
		return nil
	}

	trades, state, err := s.reg.Submit(o, persist)
	if err != nil {
		// A short sell or a suspended instrument is the client's mistake, not a
		// server fault, so it becomes a 400 rather than a 500.
		switch {
		case errors.Is(err, market.ErrInsufficientShares):
			return Result{}, invalid("insufficient shares to sell %d %s", cmd.Qty, cmd.Emiten)
		case errors.Is(err, market.ErrInsufficientBalance):
			return Result{}, invalid("insufficient balance to buy %d %s", cmd.Qty, cmd.Emiten)
		case errors.Is(err, market.ErrEmitenInactive):
			return Result{}, invalid("emiten is not active: %s", cmd.Emiten)
		case errors.Is(err, market.ErrEmitenHalted):
			// Reported with the resume time, because the halt is temporary and
			// the client's next question is always when it ends. Read after the
			// rejection rather than carried out of it: the registry's lock is
			// already released, and the deadline cannot have moved — a halt is
			// never shortened by anything but an operator.
			if h, herr := s.reg.Halt(em.ID); herr == nil && h.Halted {
				return Result{}, invalid(
					"trading in %s is halted until %s",
					cmd.Emiten, h.ResumesAt.UTC().Format(time.RFC3339))
			}
			return Result{}, invalid("trading in %s is halted", cmd.Emiten)
		case errors.Is(err, market.ErrOutsideBand):
			// Auto-rejection. The band is quoted in the message rather than just
			// named, so a broker can correct the order without a second call to
			// discover what the limits were.
			if band, ok := s.reg.Band(em.ID); ok {
				return Result{}, invalid(
					"price %d is outside the permitted range %d-%d (reference %d)",
					price, band.Floor, band.Ceiling, band.Reference)
			}
			return Result{}, invalid("price %d is outside the permitted range", price)
		}
		// persist's own error is already wrapped with "order: save execution:",
		// so it is returned as-is rather than wrapped again as a match failure.
		return Result{}, err
	}

	if len(trades) > 0 || o.Status == engine.Open {
		s.hub.Broadcast(em.ID, state)
	}

	// Prices only move when something actually executes, so a pass that only
	// rested an order leaves derived market data untouched. Announced after the
	// transaction has committed, so no observer can act on a trade that failed.
	if len(trades) > 0 && s.trades != nil {
		s.trades.TradesExecuted()
	}

	return Result{Order: o, Trades: trades}, nil
}

// CancelCommand is a request to withdraw a resting order. The participant is
// the broker asking, and must be the one that placed it.
type CancelCommand struct {
	Emiten      string
	Participant string
	OrderID     int64
}

// Cancel withdraws a resting order, releasing the shares or cash it reserved.
//
// The shape mirrors Submit and for the same reasons: the database write runs
// inside the registry's lock through a callback, so the row and the book are
// cancelled together or neither is, and submitMu is held across the whole
// sequence so a cancel and a matching pass cannot interleave. Without that
// lock, an order could be cancelled in memory while a submission already
// matching against it commits a trade for the quantity just released.
//
// Only the owning broker may cancel, enforced by the registry, which is the
// only layer that can see who a resting order belongs to.
func (s *Service) Cancel(ctx context.Context, cmd CancelCommand) (Result, error) {
	em, ok := s.dir.Emiten(cmd.Emiten)
	if !ok {
		return Result{}, invalid("unknown emiten: %s", cmd.Emiten)
	}
	part, ok := s.dir.Participant(cmd.Participant)
	if !ok {
		return Result{}, invalid("unknown participant: %s", cmd.Participant)
	}
	if cmd.OrderID <= 0 {
		return Result{}, invalid("order_id must be > 0")
	}

	s.submitMu.Lock()
	defer s.submitMu.Unlock()

	// errFilledMeanwhile marks the one failure that is the client's fault rather
	// than the database's: the row was no longer open, because a matching pass
	// filled it between this cancel reading the book and the update running. It
	// is a sentinel rather than a ValidationError so it survives the registry's
	// unwind path unchanged and can be told apart from a genuine write failure.
	var errFilledMeanwhile = errors.New("order: no longer open")

	persist := func(o *engine.Order) error {
		updated, err := s.repo.CancelOrder(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("order: cancel: %w", err)
		}
		if !updated {
			return errFilledMeanwhile
		}
		return nil
	}

	res, err := s.reg.Cancel(em.ID, cmd.OrderID, part.ID, persist)
	if err != nil {
		switch {
		case errors.Is(err, errFilledMeanwhile):
			return Result{}, invalid("order %d is no longer open", cmd.OrderID)
		case errors.Is(err, market.ErrOrderNotFound):
			return Result{}, invalid("order %d is not resting in %s", cmd.OrderID, cmd.Emiten)
		case errors.Is(err, market.ErrNotOrderOwner):
			return Result{}, invalid("order %d belongs to another participant", cmd.OrderID)
		case errors.Is(err, market.ErrUnknownEmiten):
			return Result{}, invalid("unknown emiten: %s", cmd.Emiten)
		}
		return Result{}, err
	}

	// The book lost a price level's worth of depth, so subscribers are told —
	// after the cancel has committed, never before.
	s.hub.Broadcast(em.ID, res.State)

	// No trades: a cancel moves no price, so derived market data is untouched
	// and the trade observer is deliberately not notified.
	return Result{Order: res.Order}, nil
}

// History returns one page of order history, newest first, optionally filtered by
// emiten, participant, or status.
//
// An unknown code in a filter is a client mistake, so it becomes a ValidationError
// rather than silently returning an empty page.
func (s *Service) History(ctx context.Context, emitenKode, participantKode, status string, page, limit int) ([]OrderRecord, int, error) {
	var f OrderFilter

	if emitenKode != "" {
		e, ok := s.dir.Emiten(emitenKode)
		if !ok {
			return nil, 0, invalid("unknown emiten: %s", emitenKode)
		}
		f.EmitenID = &e.ID
	}
	if participantKode != "" {
		p, ok := s.dir.Participant(participantKode)
		if !ok {
			return nil, 0, invalid("unknown participant: %s", participantKode)
		}
		f.ParticipantID = &p.ID
	}
	if status != "" {
		switch engine.Status(status) {
		case engine.Open, engine.Filled, engine.Cancelled:
			f.Status = &status
		default:
			return nil, 0, invalid("status must be open, filled or cancelled")
		}
	}

	total, err := s.repo.CountOrders(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	start, _ := httpx.Slice(page, limit, total)

	records, err := s.repo.ListOrders(ctx, f, limit, start)
	return records, total, err
}

// Directory exposes the master-data lookup the transformer needs to turn stored
// ids back into codes.
func (s *Service) Directory() *market.Directory { return s.dir }

// resolve validates a command and resolves its codes to master data.
//
// This is the business-rule boundary: emiten and participant must exist, side and
// type must be known, qty must be positive, and a limit order needs a positive
// price. A market order carries no price, so its price is normalized to 0.
func (s *Service) resolve(cmd SubmitCommand) (market.Emiten, market.Participant, engine.Side, engine.Type, int64, error) {
	var (
		em   market.Emiten
		part market.Participant
	)

	em, ok := s.dir.Emiten(cmd.Emiten)
	if !ok {
		return em, part, "", "", 0, invalid("unknown emiten: %s", cmd.Emiten)
	}
	part, ok = s.dir.Participant(cmd.Participant)
	if !ok {
		return em, part, "", "", 0, invalid("unknown participant: %s", cmd.Participant)
	}

	side := engine.Side(cmd.Side)
	if side != engine.Buy && side != engine.Sell {
		return em, part, "", "", 0, invalid("side must be buy or sell")
	}
	typ := engine.Type(cmd.Type)
	if typ != engine.Limit && typ != engine.Market {
		return em, part, "", "", 0, invalid("type must be limit or market")
	}
	if cmd.Qty <= 0 {
		return em, part, "", "", 0, invalid("qty must be > 0")
	}

	price := cmd.Price
	if typ == engine.Limit {
		if price <= 0 {
			return em, part, "", "", 0, invalid("price must be > 0 for limit order")
		}
		// The exchange's price floor. Checked here, at the gate, so a quote below
		// it is rejected outright rather than resting in the book — where, as the
		// best price on its side, it would set the level every later order matches
		// against. "price > 0" alone is not a market rule: it accepts 58, then 5,
		// then 1, each one legal on its own and each one dragging the book down
		// behind it.
		//
		// Only limit orders are checked. A market order carries no price at all;
		// what it pays is the resting order's price, which was itself validated
		// here on the way in.
		if floor := s.limits.MinPrice(); price < floor {
			return em, part, "", "", 0, invalid(
				"price %d is below the minimum price of %d", price, floor)
		}
	} else {
		price = 0 // market orders carry no price
	}

	return em, part, side, typ, price, nil
}

// buildExecution assembles the persistable outcome of a matching pass.
func buildExecution(o *engine.Order, trades []engine.Trade) Execution {
	ex := Execution{
		Order: OrderRecord{
			ID:            o.ID,
			EmitenID:      o.EmitenID,
			ParticipantID: o.ParticipantID,
			Side:          string(o.Side),
			Type:          string(o.Type),
			Price:         o.Price,
			Qty:           o.Qty,
			Remaining:     o.Remaining,
			Status:        string(o.Status),
			Seq:           o.Seq,
		},
		Trades: make([]TradeRecord, 0, len(trades)),
	}

	for _, t := range trades {
		ex.Trades = append(ex.Trades, TradeRecord{
			EmitenID:          t.EmitenID,
			BuyOrderID:        t.BuyOrderID,
			SellOrderID:       t.SellOrderID,
			BuyParticipantID:  t.BuyParticipantID,
			SellParticipantID: t.SellParticipantID,
			Price:             t.Price,
			Qty:               t.Qty,
			Seq:               t.Seq,
		})
	}
	ex.Fills = passiveFills(o, trades)
	ex.Assets = assetDeltas(trades)
	ex.Wallets = walletDeltas(trades)
	return ex
}

// assetDeltas nets the share movements of a matching pass, one entry per broker
// and emiten: buyers gain, sellers lose.
//
// Netting matters. A broker that matches its own resting order, or sweeps several
// of its own price levels, appears more than once in a single batch — and two rows
// with the same conflict target in one upsert make Postgres fail with "cannot
// affect row a second time". Summing here also lets a self-trade collapse to no
// change at all, which is what actually happened.
//
// Sorted so concurrent transactions lock rows in a consistent order, the same
// reasoning as passiveFills.
func assetDeltas(trades []engine.Trade) []AssetDelta {
	type key struct{ participantID, emitenID int64 }

	netted := make(map[key]int64, len(trades)*2)
	for _, t := range trades {
		netted[key{t.BuyParticipantID, t.EmitenID}] += t.Qty
		netted[key{t.SellParticipantID, t.EmitenID}] -= t.Qty
	}

	out := make([]AssetDelta, 0, len(netted))
	for k, delta := range netted {
		if delta == 0 {
			continue // a self-trade nets out; no row needs touching
		}
		out = append(out, AssetDelta{ParticipantID: k.participantID, EmitenID: k.emitenID, Delta: delta})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ParticipantID != out[j].ParticipantID {
			return out[i].ParticipantID < out[j].ParticipantID
		}
		return out[i].EmitenID < out[j].EmitenID
	})
	return out
}

// walletDeltas nets the cash movements of a matching pass, one entry per
// broker: buyers pay, sellers receive. Netted for the same reason as
// assetDeltas — a broker appearing more than once in a batch must collapse to
// a single row, or the upsert below fails on "cannot affect row a second time".
//
// Sorted so concurrent transactions lock rows in a consistent order.
func walletDeltas(trades []engine.Trade) []WalletDelta {
	netted := make(map[int64]int64, len(trades)*2)
	for _, t := range trades {
		cost := t.Qty * t.Price
		netted[t.BuyParticipantID] -= cost
		netted[t.SellParticipantID] += cost
	}

	out := make([]WalletDelta, 0, len(netted))
	for participantID, delta := range netted {
		if delta == 0 {
			continue // a self-trade nets out; no row needs touching
		}
		out = append(out, WalletDelta{ParticipantID: participantID, Delta: delta})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ParticipantID < out[j].ParticipantID })
	return out
}

// passiveFills totals, per resting order, the quantity it traded away in this
// pass. The passive order is whichever side of each trade is not the incoming
// order.
//
// The result is sorted by order id so concurrent transactions lock rows in a
// consistent order.
func passiveFills(incoming *engine.Order, trades []engine.Trade) []Fill {
	traded := make(map[int64]int64, len(trades))
	for _, t := range trades {
		passiveID := t.BuyOrderID
		if t.BuyOrderID == incoming.ID {
			passiveID = t.SellOrderID
		}
		traded[passiveID] += t.Qty
	}

	fills := make([]Fill, 0, len(traded))
	for id, qty := range traded {
		fills = append(fills, Fill{OrderID: id, Qty: qty})
	}
	sort.Slice(fills, func(i, j int) bool { return fills[i].OrderID < fills[j].OrderID })
	return fills
}
