package emiten

import "bekasi-automatic-trading-system/market"

// percentScale rounds percentages to two decimal places.
const percentScale = 100

// ToEmitenView converts master data into the list shape.
func ToEmitenView(e market.Emiten) EmitenView {
	return EmitenView{
		Kode:           e.Kode,
		Nama:           e.Nama,
		IsActive:       e.IsActive,
		ListedShares:   e.ListedShares,
		UnlistedShares: e.UnlistedShares,
		TotalShares:    e.TotalShares(),
	}
}

// ToEmitenViews converts a batch, preserving order. Always non-nil so the field
// marshals as [] rather than null.
func ToEmitenViews(emitens []market.Emiten) []EmitenView {
	out := make([]EmitenView, 0, len(emitens))
	for _, e := range emitens {
		out = append(out, ToEmitenView(e))
	}
	return out
}

// ToEmitenDetail combines master data with price statistics.
//
// Market value is derived here rather than stored: it depends on the last traded
// price, so one trade would otherwise invalidate the stored value of every holder
// of that instrument.
func ToEmitenDetail(e market.Emiten, stats PriceStats) EmitenDetail {
	total := e.TotalShares()

	detail := EmitenDetail{
		Kode:           e.Kode,
		Nama:           e.Nama,
		IsActive:       e.IsActive,
		CurrentPrice:   stats.Current,
		HighestPrice:   stats.Highest,
		LowestPrice:    stats.Lowest,
		ListedShares:   e.ListedShares,
		UnlistedShares: e.UnlistedShares,
		TotalShares:    total,
	}

	// No shares means no meaningful split; report null rather than dividing by
	// zero or inventing a 0%.
	if total > 0 {
		listed := round2(float64(e.ListedShares) / float64(total) * 100)
		unlisted := round2(100 - listed)
		detail.FreeFloatPercentage = &listed
		detail.UnlistedPercentage = &unlisted
	}

	// price × shares for a large issuer is around 10^15, comfortably inside
	// int64's 9.2×10^18.
	if stats.Current != nil {
		value := *stats.Current * total
		detail.Value = &value
	}
	return detail
}

// round2 rounds to two decimal places without pulling in math, keeping the
// percentages readable in JSON.
func round2(v float64) float64 {
	return float64(int64(v*percentScale+0.5)) / percentScale
}
