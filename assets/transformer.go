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
		// Value is derived, never stored: it depends on the last traded price, so
		// a stored column would go stale for every broker that did not trade.
		if rec.LastPrice != nil {
			value := *rec.LastPrice * rec.AmountShared
			view.Value = &value
		}
		out = append(out, view)
	}
	return out
}
