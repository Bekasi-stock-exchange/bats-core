package order

import "bekasi-automatic-trading-system/market/engine"

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
