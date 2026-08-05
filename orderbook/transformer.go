package orderbook

import "bekasi-automatic-trading-system/market"

func ToBookSnapshot(state market.BookState) BookSnapshot {
	return BookSnapshot{
		Emiten: state.Emiten,
		Bids:   toPriceLevels(state.Bids),
		Asks:   toPriceLevels(state.Asks),
	}
}

// A second constructor rather than a mutator applied to an already-built
// snapshot: the discriminator is set once, at construction, so a BookSnapshot is
// never half-formed.
func ToBookUpdate(state market.BookState) BookSnapshot {
	snap := ToBookSnapshot(state)
	snap.Type = "update"
	return snap
}

func ToBookSnapshots(states []market.BookState) []BookSnapshot {
	out := make([]BookSnapshot, 0, len(states))
	for _, s := range states {
		out = append(out, ToBookSnapshot(s))
	}
	return out
}

// Always non-nil so the field marshals as [] rather than null.
func toPriceLevels(levels []market.Level) []PriceLevel {
	out := make([]PriceLevel, 0, len(levels))
	for _, l := range levels {
		out = append(out, PriceLevel{Price: l.Price, Qty: l.Qty})
	}
	return out
}
