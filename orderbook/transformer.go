package orderbook

import "bekasi-automatic-trading-system/market"

// ToBookSnapshot converts book state into the REST response shape.
func ToBookSnapshot(state market.BookState) BookSnapshot {
	return BookSnapshot{
		Emiten: state.Emiten,
		Bids:   toPriceLevels(state.Bids),
		Asks:   toPriceLevels(state.Asks),
	}
}

// ToBookUpdate converts book state into the WebSocket push shape, which is the
// snapshot tagged as an update.
//
// This is a second constructor rather than a mutator applied to an already-built
// snapshot: the discriminator is set once, at construction, so a BookSnapshot is
// never half-formed.
func ToBookUpdate(state market.BookState) BookSnapshot {
	snap := ToBookSnapshot(state)
	snap.Type = "update"
	return snap
}

// ToBookSnapshots converts a batch of book states, preserving order.
func ToBookSnapshots(states []market.BookState) []BookSnapshot {
	out := make([]BookSnapshot, 0, len(states))
	for _, s := range states {
		out = append(out, ToBookSnapshot(s))
	}
	return out
}

// toPriceLevels maps aggregated levels to their JSON shape. The result is always
// non-nil so the field marshals as [] rather than null.
func toPriceLevels(levels []market.Level) []PriceLevel {
	out := make([]PriceLevel, 0, len(levels))
	for _, l := range levels {
		out = append(out, PriceLevel{Price: l.Price, Qty: l.Qty})
	}
	return out
}
