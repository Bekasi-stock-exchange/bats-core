package underwriter

// CreateRequest is the body of POST /api/admin/underwriters.
//
// One field, because registering an underwriter grants a permission to an
// existing broker rather than creating a new firm. Its code and name are that
// broker's already.
type CreateRequest struct {
	// Broker code permitted to underwrite. Must be a registered participant.
	// Allocated shares land in this participant's holdings, because only a
	// participant can trade them.
	Participant string `json:"participant" example:"YP" validate:"required"`
}

// UnderwriterView is an underwriter as returned by the admin listing.
//
// kode and nama are the participant's, joined on read — this is the same firm
// under its trading identity, not a separate entity with a name of its own.
type UnderwriterView struct {
	// Broker code. Identical to participant; both are present so a client can
	// key on kode like every other listing without knowing they are the same.
	Kode string `json:"kode" example:"YP"`
	Nama string `json:"nama" example:"Mirae Asset Sekuritas"`
	// Broker code this underwriter trades through.
	Participant string `json:"participant" example:"YP"`
	IsActive    bool   `json:"is_active" example:"true"`
}

type UnderwriterAllocation struct {
	// Broker code taking this tranche. Must be a registered underwriter.
	Underwriter string `json:"underwriter" example:"YP" validate:"required"`
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
	// The syndicate. At least one entry, and the tranches must sum to exactly
	// listed_shares. Every member is on equal terms.
	Underwriters []UnderwriterAllocation `json:"underwriters" validate:"required"`
}

// ExistingIPORequest is the body of POST /api/admin/emiten/{kode}/ipo: an offering
// over an instrument that is already registered but not yet trading.
//
// It carries no kode, nama, listed_shares or unlisted_shares. The instrument
// already exists and those are its own facts, fixed when it was registered — an
// offering decides who underwrites it and at what price, not how many shares the
// company has. Accepting them here would let an offering silently restate the
// share count every valuation is derived from.
type ExistingIPORequest struct {
	// Offering price per share. Must be > 0; becomes the instrument's reference
	// price until it first trades.
	IPOPrice int64 `json:"ipo_price" format:"int64" example:"1000" validate:"required"`
	// The syndicate. At least one entry, and the tranches must sum to exactly
	// the instrument's existing listed_shares. Every member is on equal terms.
	Underwriters []UnderwriterAllocation `json:"underwriters" validate:"required"`
}

type AllocationView struct {
	// Broker code that took this tranche. Identical to participant, since an
	// underwriter is a participant; both are present so the field a client keys
	// on does not depend on knowing that.
	Underwriter string `json:"underwriter" example:"YP"`
	Nama        string `json:"nama" example:"Mirae Asset Sekuritas"`
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
