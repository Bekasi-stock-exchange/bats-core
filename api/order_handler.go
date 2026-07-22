package api

import (
	"context"
	"encoding/json"
	"net/http"

	"bekasi-automatic-trading-system/engine"
	"bekasi-automatic-trading-system/store"
)

// handleSubmitOrder handles POST /orders.
//
// Validation lives here (not in engine): emiten and participant must exist,
// qty > 0, and for a limit order price > 0. The order row is inserted first so
// PostgreSQL assigns its id (used as the trade FK), then matching runs under the
// book lock, then trades and the resulting order states are persisted.
func (s *Server) handleSubmitOrder(w http.ResponseWriter, r *http.Request) {
	var req submitOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	em, ok := s.emitenByKode[req.Emiten]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown emiten: "+req.Emiten)
		return
	}
	part, ok := s.partByKode[req.Participant]
	if !ok {
		writeError(w, http.StatusBadRequest, "unknown participant: "+req.Participant)
		return
	}

	side := engine.Side(req.Side)
	if side != engine.Buy && side != engine.Sell {
		writeError(w, http.StatusBadRequest, "side must be buy or sell")
		return
	}
	typ := engine.Type(req.Type)
	if typ != engine.Limit && typ != engine.Market {
		writeError(w, http.StatusBadRequest, "type must be limit or market")
		return
	}
	if req.Qty <= 0 {
		writeError(w, http.StatusBadRequest, "qty must be > 0")
		return
	}
	price := req.Price
	if typ == engine.Limit {
		if price <= 0 {
			writeError(w, http.StatusBadRequest, "price must be > 0 for limit order")
			return
		}
	} else {
		price = 0 // market order convention
	}

	ctx := r.Context()

	// Insert the order row first to obtain its DB-generated id. Status/remaining
	// are provisional here (open/full qty) and updated after matching.
	orderID, err := s.store.InsertOrder(ctx, store.OrderRow{
		EmitenID:      em.ID,
		ParticipantID: part.ID,
		Side:          req.Side,
		Type:          string(typ),
		Price:         price,
		Qty:           req.Qty,
		Remaining:     req.Qty,
		Status:        string(engine.Open),
		Seq:           0, // Seq is assigned by the engine below; provisional.
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "persist order failed")
		return
	}

	o := &engine.Order{
		ID:            orderID,
		EmitenID:      em.ID,
		ParticipantID: part.ID,
		Side:          side,
		Type:          typ,
		Price:         price,
		Qty:           req.Qty,
	}

	// Match under the book lock (single-goroutine discipline).
	s.mu.Lock()
	eng := s.engineFor(em.ID)
	trades := eng.Submit(o)
	snap := snapshotBook(em.Kode, eng.Book())
	s.mu.Unlock()

	// Persist the seq the engine assigned, the final incoming-order state, and
	// the resulting trades. Also update any passive orders that filled.
	if err := s.store.UpdateOrderSeq(ctx, o.ID, o.Seq); err != nil {
		writeError(w, http.StatusInternalServerError, "persist order seq failed")
		return
	}
	if err := s.persistResults(ctx, o, trades); err != nil {
		writeError(w, http.StatusInternalServerError, "persist results failed")
		return
	}

	// Broadcast the new book state to WS subscribers of this emiten.
	if len(trades) > 0 || o.Status == engine.Open {
		s.hub.broadcast(em.ID, updateSnapshot(snap))
	}

	writeJSON(w, http.StatusOK, submitOrderResponse{
		Order: orderView{
			ID:        o.ID,
			Status:    string(o.Status),
			Remaining: o.Remaining,
		},
		Trades: toTradeViews(trades),
	})
}

// persistResults records trades and updates the final state of every order that
// changed: the incoming order plus any passive orders touched by the trades.
func (s *Server) persistResults(ctx context.Context, incoming *engine.Order, trades []engine.Trade) error {
	rows := make([]store.TradeRow, 0, len(trades))
	for _, t := range trades {
		rows = append(rows, store.TradeRow{
			EmitenID:    t.EmitenID,
			BuyOrderID:  t.BuyOrderID,
			SellOrderID: t.SellOrderID,
			Price:       t.Price,
			Qty:         t.Qty,
			Seq:         t.Seq,
		})
	}
	if err := s.store.InsertTrades(ctx, rows); err != nil {
		return err
	}

	// Update the incoming order's final state.
	if err := s.store.UpdateOrderResult(ctx, incoming.ID, incoming.Remaining, string(incoming.Status)); err != nil {
		return err
	}

	// Update every passive order that participated. Its remaining is derived from
	// the persisted total qty traded against it; a fully consumed passive order
	// is filled, otherwise it is still open.
	traded := passiveTradedQty(incoming, trades)
	for passiveID, qty := range traded {
		if err := s.store.ApplyFill(ctx, passiveID, qty); err != nil {
			return err
		}
	}
	return nil
}

// passiveTradedQty returns, per passive order id, the total quantity it traded
// in this batch. The passive order is whichever side of each trade is not the
// incoming order.
func passiveTradedQty(incoming *engine.Order, trades []engine.Trade) map[int64]int64 {
	out := make(map[int64]int64)
	for _, t := range trades {
		var passiveID int64
		if t.BuyOrderID == incoming.ID {
			passiveID = t.SellOrderID
		} else {
			passiveID = t.BuyOrderID
		}
		out[passiveID] += t.Qty
	}
	return out
}

func updateSnapshot(s bookSnapshot) bookSnapshot {
	s.Type = "update"
	return s
}
