// Package assets reports broker share holdings: what each broker owns of each
// emiten, and what that is currently worth.
package assets

import (
	"context"
	"time"

	"bekasi-automatic-trading-system/market"
)

// Record is one broker's holding of one emiten, as stored.
//
// LastPrice is the emiten's most recent execution price, or nil when it has never
// traded. It is carried rather than a computed value so the nil case stays visible
// all the way to the transformer, instead of collapsing into a misleading zero.
type Record struct {
	ParticipantID int64
	EmitenID      int64
	AmountShared  int64
	UpdatedAt     time.Time
	LastPrice     *int64
}

// Repository reads broker holdings.
//
// Declared here, in the package that consumes it, and satisfied by the repository
// package — so this package depends on a behaviour, not on a database handle.
type Repository interface {
	LoadHoldings(ctx context.Context) ([]market.Holding, error)
	ListHoldings(ctx context.Context, participantID *int64, limit, offset int) ([]Record, error)
	CountHoldings(ctx context.Context, participantID *int64) (int, error)
}
