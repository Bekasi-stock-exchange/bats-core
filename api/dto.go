package api

import (
	"net/http"

	"encoding/json"

	"bekasi-automatic-trading-system/engine"
)

// --- Request / response shapes (JSON lives only in the api layer) ---

type submitOrderRequest struct {
	Emiten      string `json:"emiten"`
	Participant string `json:"participant"`
	Side        string `json:"side"`
	Type        string `json:"type"`
	Price       int64  `json:"price"`
	Qty         int64  `json:"qty"`
}

type orderView struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Remaining int64  `json:"remaining"`
}

type tradeView struct {
	Price       int64 `json:"price"`
	Qty         int64 `json:"qty"`
	BuyOrderID  int64 `json:"buy_order_id"`
	SellOrderID int64 `json:"sell_order_id"`
}

type submitOrderResponse struct {
	Order  orderView   `json:"order"`
	Trades []tradeView `json:"trades"`
}

type priceLevel struct {
	Price int64 `json:"price"`
	Qty   int64 `json:"qty"`
}

// bookSnapshot is the shape returned by GET /orderbook and pushed over WS.
type bookSnapshot struct {
	Type   string       `json:"type,omitempty"` // "update" for WS pushes; empty for REST
	Emiten string       `json:"emiten"`
	Bids   []priceLevel `json:"bids"`
	Asks   []priceLevel `json:"asks"`
}

// --- Conversions ---

func toTradeViews(trades []engine.Trade) []tradeView {
	out := make([]tradeView, 0, len(trades))
	for _, t := range trades {
		out = append(out, tradeView{
			Price:       t.Price,
			Qty:         t.Qty,
			BuyOrderID:  t.BuyOrderID,
			SellOrderID: t.SellOrderID,
		})
	}
	return out
}

// snapshotBook aggregates a book into price levels (qty summed per price).
// Caller must hold s.mu. kode is the emiten code for the payload.
func snapshotBook(kode string, book *engine.OrderBook) bookSnapshot {
	return bookSnapshot{
		Emiten: kode,
		Bids:   aggregate(book.Bids),
		Asks:   aggregate(book.Asks),
	}
}

// aggregate collapses a sorted side into per-price-level totals, preserving the
// side's existing order.
func aggregate(orders []*engine.Order) []priceLevel {
	levels := make([]priceLevel, 0)
	for _, o := range orders {
		n := len(levels)
		if n > 0 && levels[n-1].Price == o.Price {
			levels[n-1].Qty += o.Remaining
			continue
		}
		levels = append(levels, priceLevel{Price: o.Price, Qty: o.Remaining})
	}
	return levels
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
