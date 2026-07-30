package emiten

// CreateRequest is the body of POST /api/admin/emiten.
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
// The price fields and value are null — never zero — for an instrument that has
// never traded, because no price exists yet rather than the price being nothing.
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

	ListedShares   int64 `json:"listed_shares" format:"int64" example:"123275050000"`
	UnlistedShares int64 `json:"unlisted_shares" format:"int64" example:"1000000000"`
	TotalShares    int64 `json:"total_shares" format:"int64" example:"124275050000"`

	// Share of total shares that is publicly tradeable. Null when there are no
	// shares at all. Always sums to 100 with UnlistedPercentage.
	FreeFloatPercentage *float64 `json:"free_float_percentage,omitempty" example:"99.2"`
	// Share of total shares that is restricted. Null when there are no shares.
	UnlistedPercentage *float64 `json:"unlisted_percentage,omitempty" example:"0.8"`

	// Market capitalisation: current price × total shares. Null if never traded.
	Value *int64 `json:"value,omitempty" format:"int64" example:"1000414152500000"`
}
