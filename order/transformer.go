package order

import (
	"bekasi-automatic-trading-system/engine"
	"bekasi-automatic-trading-system/market"
)

// ToSubmitOrderResponse converts a matching result into the response shape.
func ToSubmitOrderResponse(res Result) SubmitOrderResponse {
	return SubmitOrderResponse{
		Order: OrderView{
			ID:        res.Order.ID,
			Status:    string(res.Order.Status),
			Remaining: res.Order.Remaining,
		},
		Trades: toTradeViews(res.Trades),
	}
}

// ToCancelOrderResponse converts a cancellation into the response shape.
//
// Remaining is the quantity that never filled and has now been released — not a
// quantity still working, which is what the same field means on a submission.
func ToCancelOrderResponse(res Result) CancelOrderResponse {
	return CancelOrderResponse{
		Order: OrderView{
			ID:        res.Order.ID,
			Status:    string(res.Order.Status),
			Remaining: res.Order.Remaining,
		},
	}
}

// ToOrderHistoryViews converts stored orders into the admin history shape.
//
// Codes are resolved through market.Directory, which already holds every emiten
// and participant in memory — so listing order history needs no join. Always
// non-nil so the field marshals as [] rather than null.
func ToOrderHistoryViews(records []OrderRecord, dir *market.Directory) []OrderHistoryView {
	out := make([]OrderHistoryView, 0, len(records))
	for _, o := range records {
		view := OrderHistoryView{
			ID:        o.ID,
			Seq:       o.Seq,
			Side:      o.Side,
			Type:      o.Type,
			Price:     o.Price,
			Qty:       o.Qty,
			Remaining: o.Remaining,
			Status:    o.Status,
		}
		if e, ok := dir.EmitenByID(o.EmitenID); ok {
			view.Emiten = e.Kode
		}
		if p, ok := dir.ParticipantByID(o.ParticipantID); ok {
			view.Participant = p.Kode
		}
		out = append(out, view)
	}
	return out
}

// toTradeViews maps executions to their JSON shape. The result is always non-nil
// so the field marshals as [] rather than null.
func toTradeViews(trades []engine.Trade) []TradeView {
	out := make([]TradeView, 0, len(trades))
	for _, t := range trades {
		out = append(out, TradeView{
			Price:       t.Price,
			Qty:         t.Qty,
			BuyOrderID:  t.BuyOrderID,
			SellOrderID: t.SellOrderID,
		})
	}
	return out
}
