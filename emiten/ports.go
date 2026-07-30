// Package emiten is the listed-instrument surface: the admin list and create
// endpoints, and the participant-facing detail view with price statistics.
package emiten

import "context"

// PriceStats summarises an emiten's trade history.
//
// Every field is a pointer because an instrument that has never traded has no
// price at all. Returning 0 would claim it is worth nothing, which is a different
// and false statement.
type PriceStats struct {
	Current *int64
	Highest *int64
	Lowest  *int64
}

// PriceStatsRepository reads price statistics for one emiten.
//
// Declared here and satisfied by the repository package, which owns the trades
// table — so this package gains no database dependency.
type PriceStatsRepository interface {
	PriceStats(ctx context.Context, emitenID int64) (PriceStats, error)
}
