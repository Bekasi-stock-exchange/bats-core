package assets

// HoldingView is one broker's stake in one emiten.
//
// last_price and value are **null**, not 0, when the instrument has never traded:
// no price exists yet, which is a different statement from the holding being
// worthless.
type HoldingView struct {
	Participant string `json:"participant" example:"YP"`
	Emiten      string `json:"emiten" example:"BBCA"`
	// Shares held.
	AmountShared int64 `json:"amount_shared" format:"int64" example:"1000000"`
	// Most recent execution price for this emiten. Null if never traded.
	LastPrice *int64 `json:"last_price,omitempty" format:"int64" example:"8050"`
	// Market value: last_price × amount_shared. Derived on read, so it always
	// reflects the latest trade even when this broker did not trade. Null if the
	// instrument has never traded.
	Value *int64 `json:"value,omitempty" format:"int64" example:"8050000000"`
	// When this holding last changed.
	UpdatedAt string `json:"updated_at" example:"2026-07-30T09:15:02Z"`
}
