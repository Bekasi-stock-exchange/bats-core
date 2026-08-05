// Package trade is the read side of executions: the admin trade log, per-broker
// transaction history, and the price history an instrument's chart is built from.
//
// One package over one table, so every way of reading trades shares a single
// repository file.
package trade

import (
	"context"
	"errors"
	"time"
)

// ErrInvalidInterval rejects a candle interval outside the allowlist.
var ErrInvalidInterval = errors.New("trade: unsupported interval")

// Record is one execution as stored.
type Record struct {
	ID                int64
	Seq               int64
	EmitenID          int64
	BuyOrderID        int64
	SellOrderID       int64
	BuyParticipantID  int64
	SellParticipantID int64
	Price             int64
	Qty               int64
	ExecutedAt        time.Time
}

// Tick is one execution reduced to what a price series needs.
type Tick struct {
	Seq        int64
	Price      int64
	Qty        int64
	ExecutedAt time.Time
}

// Candle is one aggregated interval of price action.
type Candle struct {
	Time   time.Time
	Open   int64
	High   int64
	Low    int64
	Close  int64
	Volume int64
}

// Side and CounterpartyID are relative to the broker being queried: the same
// execution reads "buy"/PD for YP and "sell"/YP for PD.
type Transaction struct {
	TradeID        int64
	Seq            int64
	EmitenID       int64
	Side           string
	CounterpartyID int64
	Price          int64
	Qty            int64
	ExecutedAt     time.Time
}

// Filter narrows a listing. A nil field means "no constraint".
type Filter struct {
	EmitenID      *int64
	ParticipantID *int64
}

// Declared here and satisfied by the repository package, so this package depends
// on a behaviour rather than on a database handle.
type Repository interface {
	ListTrades(ctx context.Context, f Filter, limit, offset int) ([]Record, error)
	CountTrades(ctx context.Context, f Filter) (int, error)

	ListTransactions(ctx context.Context, participantID int64, emitenID *int64, limit, offset int) ([]Transaction, error)
	CountTransactions(ctx context.Context, participantID int64, emitenID *int64) (int, error)

	ListTicks(ctx context.Context, emitenID int64, limit, offset int) ([]Tick, error)
	CountTicks(ctx context.Context, emitenID int64) (int, error)

	// intervalSeconds is the bucket width; the caller validates it against a
	// fixed allowlist first.
	ListCandles(ctx context.Context, emitenID, intervalSeconds int64, limit int) ([]Candle, error)
}
