package index

import "time"

// valueScale rounds index levels to two decimal places, matching how an index is
// conventionally quoted.
const valueScale = 100

// ToIndexView renders the current level for the wire.
func ToIndexView(l Level) IndexView {
	return IndexView{
		Kode:       l.Kode,
		Nama:       l.Nama,
		Value:      round2(l.Value),
		MarketCap:  l.MarketCap,
		Divisor:    l.Divisor,
		Members:    l.Members,
		Total:      l.Total,
		CapturedAt: utc(l.CapturedAt),
	}
}

// ToSnapshotViews converts a page of history, preserving order. Always non-nil
// so the field marshals as [] rather than null.
func ToSnapshotViews(snaps []Snapshot) []SnapshotView {
	out := make([]SnapshotView, 0, len(snaps))
	for _, s := range snaps {
		out = append(out, SnapshotView{
			Value:      round2(s.Value),
			MarketCap:  s.MarketCap,
			Divisor:    s.Divisor,
			Members:    s.Members,
			CapturedAt: utc(s.CapturedAt),
		})
	}
	return out
}

// round2 rounds to two decimal places, keeping the level readable in JSON.
// Mirrors emiten.round2 rather than importing it: the two packages share no
// dependency, and one small function is a better price than a coupling.
func round2(v float64) float64 {
	return float64(int64(v*valueScale+0.5)) / valueScale
}

// utc renders a timestamp in RFC 3339 UTC, so clients never have to reason about
// the server's timezone.
func utc(t time.Time) string { return t.UTC().Format(time.RFC3339) }
