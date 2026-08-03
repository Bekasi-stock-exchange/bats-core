package emiten

// Values of the price_source field: which price a valuation was computed from.
const (
	// PriceSourceTrade means the instrument has traded and the valuation uses its
	// most recent execution price.
	PriceSourceTrade = "trade"
	// PriceSourceIPO means the instrument has not traded yet and the valuation
	// falls back to its listing price.
	PriceSourceIPO = "ipo"
)

// CreateRequest is the body of POST /api/admin/emiten.
//
// It carries no offering price. The instrument is registered dormant and is priced
// when its offering runs — see emiten.Service.Activate — so a price here would be
// a number with nothing to attach it to.
type CreateRequest struct {
	// Instrument code, unique across the exchange.
	Kode string `json:"kode" example:"BBNI" validate:"required"`
	// Company name.
	Nama string `json:"nama" example:"Bank Negara Indonesia Tbk" validate:"required"`
	// Publicly tradeable shares. Must be > 0.
	ListedShares int64 `json:"listed_shares" format:"int64" example:"18462169893" validate:"required"`
	// Restricted shares not available for trading. Defaults to 0.
	UnlistedShares int64 `json:"unlisted_shares" format:"int64" example:"0"`
}

// EmitenView is a listed instrument's master data, as returned by the admin list.
type EmitenView struct {
	Kode string `json:"kode" example:"BBCA"`
	Nama string `json:"nama" example:"Bank Central Asia Tbk"`
	// Whether the instrument accepts new orders. An inactive emiten still has a
	// readable book and history.
	IsActive       bool  `json:"is_active" example:"true"`
	ListedShares   int64 `json:"listed_shares" format:"int64" example:"123275050000"`
	UnlistedShares int64 `json:"unlisted_shares" format:"int64" example:"1000000000"`
	TotalShares    int64 `json:"total_shares" format:"int64" example:"124275050000"`
}

// EmitenDetail adds price statistics and derived valuations to the master data.
//
// The trade-derived price fields are null — never zero — for an instrument that has
// never traded, because no price exists yet rather than the price being nothing.
//
// current_price and reference_price are not the same field. current_price is
// strictly what the market last paid; reference_price is what the instrument is
// valued at, and falls back to ipo_price while the market has not spoken yet.
// price_source names which of the two backs the valuation, so a client never has to
// guess whether market cap reflects real trading.
type EmitenDetail struct {
	Kode string `json:"kode" example:"BBCA"`
	Nama string `json:"nama" example:"Bank Central Asia Tbk"`
	// Whether the instrument accepts new orders.
	IsActive bool `json:"is_active" example:"true"`
	// Most recent execution price. Null if never traded.
	CurrentPrice *int64 `json:"current_price,omitempty" format:"int64" example:"8050"`
	// Highest price ever executed. Null if never traded.
	HighestPrice *int64 `json:"highest_price,omitempty" format:"int64" example:"8200"`
	// Lowest price ever executed. Null if never traded.
	LowestPrice *int64 `json:"lowest_price,omitempty" format:"int64" example:"7800"`
	// Offering price this instrument was listed at. Null for instruments listed
	// before the field existed.
	IPOPrice *int64 `json:"ipo_price,omitempty" format:"int64" example:"1000"`
	// Price backing the valuation: the last trade, or ipo_price until the
	// instrument first trades. Null only if it has neither.
	ReferencePrice *int64 `json:"reference_price,omitempty" format:"int64" example:"8050"`
	// Where reference_price came from: "trade" or "ipo". Omitted when there is no
	// reference price at all.
	PriceSource string `json:"price_source,omitempty" enums:"trade,ipo" example:"trade"`

	ListedShares   int64 `json:"listed_shares" format:"int64" example:"123275050000"`
	UnlistedShares int64 `json:"unlisted_shares" format:"int64" example:"1000000000"`
	TotalShares    int64 `json:"total_shares" format:"int64" example:"124275050000"`

	// Share of total shares that is publicly tradeable. Null when there are no
	// shares at all. Always sums to 100 with UnlistedPercentage.
	FreeFloatPercentage *float64 `json:"free_float_percentage,omitempty" example:"99.2"`
	// Share of total shares that is restricted. Null when there are no shares.
	UnlistedPercentage *float64 `json:"unlisted_percentage,omitempty" example:"0.8"`

	// Market capitalisation: reference price × total shares. Null only when the
	// instrument has neither a trade nor an IPO price.
	Value *int64 `json:"value,omitempty" format:"int64" example:"1000414152500000"`
}
