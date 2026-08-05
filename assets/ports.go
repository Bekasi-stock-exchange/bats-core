// Package assets reports broker share holdings: what each broker owns of each
// emiten, and what that is currently worth.
package assets

import (
	"context"
	"time"

	"bekasi-automatic-trading-system/market"
)

// LastPrice is the emiten's most recent execution price, or nil when it has never
// traded; IPOPrice is the price it was listed at, or nil for the instruments that
// predate that column. Both are carried rather than a single pre-resolved price so
// the nil cases stay visible all the way to the transformer, instead of collapsing
// into a misleading zero — and so the transformer, not the query, decides which one
// values the holding (market.Emiten.ReferencePrice).
type Record struct {
	ParticipantID int64
	EmitenID      int64
	AmountShared  int64
	UpdatedAt     time.Time
	LastPrice     *int64
	IPOPrice      *int64
}

// Declared here, in the package that consumes it, and satisfied by the repository
// package — so this package depends on a behaviour, not on a database handle.
type Repository interface {
	LoadHoldings(ctx context.Context) ([]market.Holding, error)
	ListHoldings(ctx context.Context, participantID *int64, limit, offset int) ([]Record, error)
	CountHoldings(ctx context.Context, participantID *int64) (int, error)
}
