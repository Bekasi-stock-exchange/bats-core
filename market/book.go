package market

import "bekasi-automatic-trading-system/engine"

// Level is one aggregated price level: the total remaining quantity resting at a
// single price.
type Level struct {
	Price int64
	Qty   int64
}

// BookState is a point-in-time view of one emiten's order book, aggregated by
// price level.
//
// It carries no struct tags. Transport shapes are the transformers' job in the
// order/orderbook packages; keeping this type tag-free is what allows the
// WebSocket hub to fan out book state without the services depending on a JSON
// DTO.
type BookState struct {
	Emiten string
	Bids   []Level
	Asks   []Level
}

// aggregate collapses one already-sorted side of a book into per-price-level
// totals, preserving the side's existing price-time order.
func aggregate(orders []*engine.Order) []Level {
	levels := make([]Level, 0)
	for _, o := range orders {
		n := len(levels)
		if n > 0 && levels[n-1].Price == o.Price {
			levels[n-1].Qty += o.Remaining
			continue
		}
		levels = append(levels, Level{Price: o.Price, Qty: o.Remaining})
	}
	return levels
}
