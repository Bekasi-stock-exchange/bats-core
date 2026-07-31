package assets

import (
	"time"

	"bekasi-automatic-trading-system/market"
)

// Codes resolves the ids stored on a holding into their codes, using the
// directory already held in memory — so listing holdings needs no join.
type Codes struct {
	dir *market.Directory
}

// NewCodes returns a resolver backed by the directory.
func NewCodes(dir *market.Directory) Codes { return Codes{dir: dir} }

// ToHoldingViews converts holdings into their API shape. Always non-nil so the
// field marshals as [] rather than null.
func (c Codes) ToHoldingViews(records []Record) []HoldingView {
	out := make([]HoldingView, 0, len(records))
	for _, rec := range records {
		view := HoldingView{
			AmountShared: rec.AmountShared,
			LastPrice:    rec.LastPrice,
			UpdatedAt:    rec.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if p, ok := c.dir.ParticipantByID(rec.ParticipantID); ok {
			view.Participant = p.Kode
		}
		if e, ok := c.dir.EmitenByID(rec.EmitenID); ok {
			view.Emiten = e.Kode
		}

		// The reference price is resolved through market.Emiten rather than from
		// the row directly, so holdings and the emiten detail endpoint cannot
		// disagree about which price values an instrument. The record's own
		// IPOPrice is used instead of the directory's copy because it came from the
		// same read as the holding.
		ref := market.Emiten{IPOPrice: rec.IPOPrice}.ReferencePrice(rec.LastPrice)
		view.ReferencePrice = ref
		switch {
		case rec.LastPrice != nil:
			view.PriceSource = PriceSourceTrade
		case ref != nil:
			view.PriceSource = PriceSourceIPO
		}

		// Value is derived, never stored: it depends on the reference price, so a
		// stored column would go stale for every broker that did not trade.
		if ref != nil {
			value := *ref * rec.AmountShared
			view.Value = &value
		}
		out = append(out, view)
	}
	return out
}
