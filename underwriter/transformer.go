package underwriter

import "bekasi-automatic-trading-system/market"

// percentScale rounds percentages to two decimal places.
const percentScale = 100

// Codes resolves the participant ids stored on an underwriter into the code and
// name a reader sees, using the directory already held in memory — so listing
// needs no join.
type Codes struct {
	dir *market.Directory
}

// NewCodes returns a resolver backed by the directory.
func NewCodes(dir *market.Directory) Codes { return Codes{dir: dir} }

// ToUnderwriterView converts a stored underwriter into its API shape.
//
// Every visible field but is_active comes from the participant: the stored row is
// only the permission, so identity is resolved here rather than duplicated in the
// database. kode and participant are deliberately the same value — see
// UnderwriterView.
func (c Codes) ToUnderwriterView(rec Record) UnderwriterView {
	view := UnderwriterView{IsActive: rec.IsActive}
	if p, ok := c.dir.ParticipantByID(rec.ParticipantID); ok {
		view.Kode = p.Kode
		view.Nama = p.Nama
		view.Participant = p.Kode
	}
	return view
}

// ToUnderwriterViews converts a batch, preserving order. Always non-nil so the
// field marshals as [] rather than null.
func (c Codes) ToUnderwriterViews(recs []Record) []UnderwriterView {
	out := make([]UnderwriterView, 0, len(recs))
	for _, rec := range recs {
		out = append(out, c.ToUnderwriterView(rec))
	}
	return out
}

// ToIPOResponse combines the listed instrument with its syndicate.
//
// Percentages are computed against listed_shares, not total shares: the offering
// only ever allocates the listed portion, so measuring a tranche against the
// unlisted remainder would understate every underwriter's share of what was
// actually sold.
func ToIPOResponse(e market.Emiten, allocs []AllocationRecord) IPOResponse {
	price := int64(0)
	if e.IPOPrice != nil {
		price = *e.IPOPrice
	}

	resp := IPOResponse{
		Kode:           e.Kode,
		Nama:           e.Nama,
		IPOPrice:       price,
		ListedShares:   e.ListedShares,
		UnlistedShares: e.UnlistedShares,
		TotalShares:    e.TotalShares(),
		TotalValue:     e.ListedShares * price,
		Underwriters:   make([]AllocationView, 0, len(allocs)),
	}

	for _, a := range allocs {
		view := AllocationView{
			Underwriter: a.ParticipantKode,
			Nama:        a.ParticipantNama,
			Participant: a.ParticipantKode,
			Shares:      a.Shares,
			Value:       a.Shares * a.Price,
		}
		if e.ListedShares > 0 {
			view.Percentage = round2(float64(a.Shares) / float64(e.ListedShares) * 100)
		}
		resp.Underwriters = append(resp.Underwriters, view)
	}
	return resp
}

// round2 rounds to two decimal places without pulling in math, keeping the
// percentages readable in JSON.
func round2(v float64) float64 {
	return float64(int64(v*percentScale+0.5)) / percentScale
}
