package underwriter

// CreateRequest is the body of POST /api/admin/underwriters.
type CreateRequest struct {
	// Underwriter code, unique across the exchange.
	Kode string `json:"kode" example:"UW03" validate:"required"`
	// Firm name.
	Nama string `json:"nama" example:"BNI Sekuritas" validate:"required"`
	// Role: "utama" (lead) or "pendukung" (supporting).
	Jenis string `json:"jenis" enums:"utama,pendukung" example:"utama" validate:"required"`
	// Broker code this underwriter trades through. Allocated shares land in this
	// participant's holdings, because only a participant can trade them.
	Participant string `json:"participant" example:"YP" validate:"required"`
}

// UnderwriterView is an underwriter as returned by the admin listing.
type UnderwriterView struct {
	Kode string `json:"kode" example:"UW01"`
	Nama string `json:"nama" example:"Danareksa Sekuritas"`
	// Role: "utama" (lead) or "pendukung" (supporting).
	Jenis string `json:"jenis" enums:"utama,pendukung" example:"utama"`
	// Broker code this underwriter trades through.
	Participant string `json:"participant" example:"YP"`
	IsActive    bool   `json:"is_active" example:"true"`
}

// UnderwriterAllocation is one underwriter's tranche in an IPO request.
type UnderwriterAllocation struct {
	// Underwriter code taking this tranche.
	Underwriter string `json:"underwriter" example:"UW01" validate:"required"`
	// Shares allocated. Must be > 0.
	Shares int64 `json:"shares" format:"int64" example:"25000" validate:"required"`
}

// IPORequest is the body of POST /api/admin/ipo.
//
// It is a listing and a share hand-out in one request because that is what an IPO
// is: an instrument that exists but whose shares sit nowhere is not yet an
// offering, and shares allocated against an instrument that does not exist cannot
// be written at all.
type IPORequest struct {
	// Instrument code, unique across the exchange.
	Kode string `json:"kode" example:"SSIA" validate:"required"`
	// Company name.
	Nama string `json:"nama" example:"PT Surya Cipta Semesta Tbk" validate:"required"`
	// Publicly tradeable shares. The allocations must sum to exactly this.
	ListedShares int64 `json:"listed_shares" format:"int64" example:"40000" validate:"required"`
	// Restricted shares not available for trading. Defaults to 0.
	UnlistedShares int64 `json:"unlisted_shares" format:"int64" example:"10000"`
	// Offering price per share. Must be > 0; becomes the instrument's reference
	// price until it first trades.
	IPOPrice int64 `json:"ipo_price" format:"int64" example:"1000" validate:"required"`
	// The syndicate. At least one entry, exactly one of which must be "utama",
	// and the lead's tranche must be the largest.
	Underwriters []UnderwriterAllocation `json:"underwriters" validate:"required"`
}

// AllocationView is one underwriter's tranche as returned after an IPO.
type AllocationView struct {
	Underwriter string `json:"underwriter" example:"UW01"`
	Nama        string `json:"nama" example:"Danareksa Sekuritas"`
	// Role in this offering: "utama" or "pendukung".
	Jenis string `json:"jenis" enums:"utama,pendukung" example:"utama"`
	// Broker code the shares were credited to.
	Participant string `json:"participant" example:"YP"`
	// Shares allocated.
	Shares int64 `json:"shares" format:"int64" example:"25000"`
	// Share of listed_shares this tranche represents, to two decimals.
	Percentage float64 `json:"percentage" example:"62.5"`
	// Proceeds for this tranche: shares × ipo_price.
	Value int64 `json:"value" format:"int64" example:"25000000"`
}

// IPOResponse is the result of a completed offering: the instrument that was
// listed, and where its shares went.
type IPOResponse struct {
	Kode string `json:"kode" example:"SSIA"`
	Nama string `json:"nama" example:"PT Surya Cipta Semesta Tbk"`
	// Offering price per share.
	IPOPrice       int64 `json:"ipo_price" format:"int64" example:"1000"`
	ListedShares   int64 `json:"listed_shares" format:"int64" example:"40000"`
	UnlistedShares int64 `json:"unlisted_shares" format:"int64" example:"10000"`
	TotalShares    int64 `json:"total_shares" format:"int64" example:"50000"`
	// Total raised: listed_shares × ipo_price.
	TotalValue int64 `json:"total_value" format:"int64" example:"40000000"`
	// The syndicate, lead first.
	Underwriters []AllocationView `json:"underwriters"`
}
