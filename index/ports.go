// Package index owns the composite index: the exchange-wide price level computed
// IHSG-style from every listed instrument's market capitalisation, and the
// participant-facing endpoints that read it.
//
// It sits above the market kernel and depends on nothing else in the application.
// Prices come from the directory's own reference-price rule and from a narrow
// repository interface declared here, so this package never reaches into the
// emiten or trade domains — all three are views over the same trades table, and
// coupling them would make that table's shape a shared secret.
//
// The computation is deliberately not a database query. Share counts live in
// market.Directory, the price fallback rule lives on market.Emiten, and both
// would have to be duplicated in SQL to compute this in one statement. Reading
// the last price per instrument and doing the arithmetic in Go keeps a single
// definition of what an instrument is worth.
package index

import (
	"context"
	"time"
)

// IHSG is the composite index seeded by migration 014. It is the only index the
// exchange currently publishes, and the code every endpoint resolves against.
const IHSG = "IHSG"

// Definition is a stored index: its identity, its base, and the divisor that
// keeps its series continuous across listings.
//
// Divisor and BaseValue are float64 rather than int64 because neither is money.
// The divisor is a ratio-adjusted quantity that is restated by a multiplication
// on every listing, and rounding it to a whole number each time would let the
// level drift permanently — the drift never cancels, it only accumulates.
type Definition struct {
	ID        int16
	Kode      string
	Nama      string
	BaseValue float64
	BaseDate  time.Time
	Divisor   float64
}

// Level is a computed index value together with the inputs that produced it.
//
// MarketCap and Divisor travel with the level so a reader can verify it rather
// than trust it, and Members reports how many instruments were priced in: a
// level computed over 4 of 5 instruments is not comparable to one over all 5,
// and without the count that difference is invisible.
type Level struct {
	Kode       string
	Nama       string
	Value      float64
	MarketCap  int64
	Divisor    float64
	Members    int
	Total      int
	CapturedAt time.Time
}

// Snapshot is one historical index level, as stored.
type Snapshot struct {
	Value      float64
	MarketCap  int64
	Divisor    float64
	Members    int
	CapturedAt time.Time
}

// Repository reads and writes stored index state.
//
// Declared here, in the package that consumes it, and satisfied by the
// repository package — so this package depends on a behaviour rather than on a
// database handle.
type Repository interface {
	// LoadIndex returns the stored definition for an index code.
	LoadIndex(ctx context.Context, kode string) (Definition, error)

	// SaveDivisor writes a restated divisor and returns the definition as
	// written, so the caller refreshes its cache from the database's own view
	// rather than from what it hoped it wrote.
	SaveDivisor(ctx context.Context, indexID int16, divisor float64) (Definition, error)

	// InsertSnapshot appends one computed level to the history.
	InsertSnapshot(ctx context.Context, indexID int64, s Snapshot) error

	// ListSnapshots returns one page of history, newest first.
	ListSnapshots(ctx context.Context, indexID int16, from, to *time.Time, limit, offset int) ([]Snapshot, error)

	// CountSnapshots totals the same filter, for the pagination envelope.
	CountSnapshots(ctx context.Context, indexID int16, from, to *time.Time) (int, error)
}

// PriceRepository reads the latest trade price for every instrument at once.
//
// One query for the whole market, not one per instrument: the index values every
// listed emiten on every computation, and a query per member would make the cost
// of the index grow with the size of the exchange.
//
// The map omits instruments that have never traded rather than mapping them to
// 0 — the same distinction market.Emiten.ReferencePrice draws, and for the same
// reason: 0 would claim the instrument is worth nothing.
type PriceRepository interface {
	LastPrices(ctx context.Context) (map[int64]int64, error)
}
