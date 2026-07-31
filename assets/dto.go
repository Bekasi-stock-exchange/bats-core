package assets

// Values of the price_source field: which price a valuation was computed from.
const (
	// PriceSourceTrade means the instrument has traded and the valuation uses its
	// most recent execution price.
	PriceSourceTrade = "trade"
	// PriceSourceIPO means the instrument has not traded yet and the valuation
	// falls back to its listing price.
	PriceSourceIPO = "ipo"
)

// HoldingView is one broker's stake in one emiten.
//
// The price fields are **null**, not 0, when no price applies: no price existing is
// a different statement from the holding being worthless.
//
// Two prices are reported because they answer different questions. last_price is
// strictly what the market last paid, and stays null until the instrument actually
// trades. reference_price is what the holding is valued at, which falls back to the
// listing price while the market has not spoken yet; price_source says which of the
// two it came from, so a client never has to guess whether a valuation reflects real
// trading.
type HoldingView struct {
	Participant string `json:"participant" example:"YP"`
	Emiten      string `json:"emiten" example:"BBCA"`
	// Shares held.
	AmountShared int64 `json:"amount_shared" format:"int64" example:"1000000"`
	// Most recent execution price for this emiten. Null if never traded.
	LastPrice *int64 `json:"last_price,omitempty" format:"int64" example:"8050"`
	// Price backing the valuation: the last trade, or the IPO price until the
	// instrument first trades. Null only if it has neither.
	ReferencePrice *int64 `json:"reference_price,omitempty" format:"int64" example:"8050"`
	// Where reference_price came from: "trade" or "ipo". Omitted when there is no
	// reference price at all.
	PriceSource string `json:"price_source,omitempty" enums:"trade,ipo" example:"trade"`
	// Market value: reference_price × amount_shared. Derived on read, so it always
	// reflects the latest trade even when this broker did not trade. Null only when
	// the instrument has neither a trade nor an IPO price.
	Value *int64 `json:"value,omitempty" format:"int64" example:"8050000000"`
	// When this holding last changed.
	UpdatedAt string `json:"updated_at" example:"2026-07-30T09:15:02Z"`
}
