// Package wallet reports broker cash balances: how much each broker holds,
// separate from the shares tracked by package assets.
package wallet

import (
	"context"
	"time"

	"bekasi-automatic-trading-system/market"
)

// Record is one broker's cash balance, as stored.
type Record struct {
	ParticipantID int64
	Balance       int64
	UpdatedAt     time.Time
}

// Repository reads broker wallets.
//
// Declared here, in the package that consumes it, and satisfied by the
// repository package — so this package depends on a behaviour, not on a
// database handle.
type Repository interface {
	LoadWallets(ctx context.Context) ([]market.Wallet, error)
	ListWallets(ctx context.Context, participantID *int64, limit, offset int) ([]Record, error)
	CountWallets(ctx context.Context, participantID *int64) (int, error)
	FindWallet(ctx context.Context, participantID int64) (Record, error)
}
