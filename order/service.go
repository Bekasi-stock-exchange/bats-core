package order

import (
	"context"
	"fmt"
	"sort"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/market/engine"
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

// Service accepts orders: validate, match, persist, publish.
type Service struct {
	dir  *market.Directory
	reg  *market.Registry
	hub  *market.Hub
	repo Repository
}

// NewService wires the write side against the market kernel and the repository.
func NewService(dir *market.Directory, reg *market.Registry, hub *market.Hub, repo Repository) *Service {
	return &Service{dir: dir, reg: reg, hub: hub, repo: repo}
}

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
// Known residual risk: matching mutates the in-memory book before the commit, so
// a failed transaction leaves an executed-but-unpersisted trade. That window is
// unavoidable without a write-ahead record, and it is narrower than persisting
// mid-match; the book is already not reconstructed from the database on restart.
func (s *Service) Submit(ctx context.Context, cmd SubmitCommand) (Result, error) {
	em, part, side, typ, price, err := s.resolve(cmd)
	if err != nil {
		return Result{}, err
	}

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

	trades, state, err := s.reg.Submit(o)
	if err != nil {
		return Result{}, fmt.Errorf("order: match: %w", err)
	}

	if err := s.repo.SaveExecution(ctx, buildExecution(o, trades)); err != nil {
		return Result{}, fmt.Errorf("order: save execution: %w", err)
	}

	if len(trades) > 0 || o.Status == engine.Open {
		s.hub.Broadcast(em.ID, state)
	}

	return Result{Order: o, Trades: trades}, nil
}

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
			EmitenID:    t.EmitenID,
			BuyOrderID:  t.BuyOrderID,
			SellOrderID: t.SellOrderID,
			Price:       t.Price,
			Qty:         t.Qty,
			Seq:         t.Seq,
		})
	}
	ex.Fills = passiveFills(o, trades)
	return ex
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
