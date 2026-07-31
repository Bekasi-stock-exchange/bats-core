package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/index"
	"bekasi-automatic-trading-system/platform/postgres"
)

// Index reads and writes composite index state. It satisfies index.Repository
// and index.PriceRepository — the definition and its history on one side, the
// market-wide price read on the other.
type Index struct {
	db
}

// NewIndex returns an index repository backed by pool.
func NewIndex(pool *pgxpool.Pool) *Index {
	return &Index{db{pool: pool}}
}

var (
	_ index.Repository      = (*Index)(nil)
	_ index.PriceRepository = (*Index)(nil)
)

// LoadIndex returns the stored definition for an index code.
//
// The IHSG row is seeded by migration 014, so a missing row means the migration
// has not run — a deployment fault, reported as an error rather than papered
// over with a default. Starting the exchange with an invented divisor would
// publish a level no operator chose and nothing records.
func (r *Index) LoadIndex(ctx context.Context, kode string) (index.Definition, error) {
	var d index.Definition
	err := r.pool.QueryRow(ctx, `
		SELECT id, kode, nama, base_value, base_date, divisor
		FROM market_index WHERE kode = $1`, kode,
	).Scan(&d.ID, &d.Kode, &d.Nama, &d.BaseValue, &d.BaseDate, &d.Divisor)
	if err != nil {
		return index.Definition{}, fmt.Errorf("repository: load index %s: %w", kode, err)
	}
	return d, nil
}

// SaveDivisor writes a restated divisor and returns the definition as written.
//
// RETURNING gives the caller the database's own view of what landed, which is
// what the cache is then set from — the same discipline marketconfig follows, so
// the enforced value and the stored value cannot diverge.
func (r *Index) SaveDivisor(ctx context.Context, indexID int16, divisor float64) (index.Definition, error) {
	var d index.Definition
	err := r.pool.QueryRow(ctx, `
		UPDATE market_index
		   SET divisor = $2, updated_at = now()
		 WHERE id = $1
		RETURNING id, kode, nama, base_value, base_date, divisor`,
		indexID, divisor,
	).Scan(&d.ID, &d.Kode, &d.Nama, &d.BaseValue, &d.BaseDate, &d.Divisor)
	if err != nil {
		return index.Definition{}, fmt.Errorf("repository: save divisor %d: %w", indexID, err)
	}
	return d, nil
}

// InsertSnapshot appends one computed level to the history.
func (r *Index) InsertSnapshot(ctx context.Context, indexID int64, s index.Snapshot) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO index_snapshot (index_id, value, market_cap, divisor, members, captured_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		indexID, s.Value, s.MarketCap, s.Divisor, s.Members, s.CapturedAt)
	if err != nil {
		return fmt.Errorf("repository: insert index snapshot: %w", err)
	}
	return nil
}

// ListSnapshots returns one page of history, newest first.
//
// The NULL-or-match pattern on from/to keeps one query for all four filter
// combinations, matching how the trade repository handles its optional filters.
func (r *Index) ListSnapshots(ctx context.Context, indexID int16, from, to *time.Time, limit, offset int) ([]index.Snapshot, error) {
	return postgres.QueryAll(ctx, r.pool, "index history", `
		SELECT value, market_cap, divisor, members, captured_at
		FROM index_snapshot
		WHERE index_id = $1
		  AND ($2::timestamptz IS NULL OR captured_at >= $2)
		  AND ($3::timestamptz IS NULL OR captured_at <= $3)
		ORDER BY captured_at DESC
		LIMIT $4 OFFSET $5`,
		func(rows pgx.Rows) (index.Snapshot, error) {
			var s index.Snapshot
			err := rows.Scan(&s.Value, &s.MarketCap, &s.Divisor, &s.Members, &s.CapturedAt)
			return s, err
		}, indexID, from, to, limit, offset)
}

// CountSnapshots totals the same filter, for the pagination envelope.
func (r *Index) CountSnapshots(ctx context.Context, indexID int16, from, to *time.Time) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM index_snapshot
		WHERE index_id = $1
		  AND ($2::timestamptz IS NULL OR captured_at >= $2)
		  AND ($3::timestamptz IS NULL OR captured_at <= $3)`,
		indexID, from, to).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository: count index snapshots: %w", err)
	}
	return n, nil
}

// LastPrices returns the most recent trade price for every instrument that has
// traded, keyed by emiten id.
//
// One query for the whole market rather than one per instrument: the index
// values every emiten on each computation, and a query per member would make the
// cost of the index grow with the size of the exchange.
//
// DISTINCT ON with ORDER BY emiten_id, seq DESC is served directly by
// idx_trades_emiten_seq, so this is an index scan picking one row per
// instrument rather than an aggregate over the whole table.
//
// Ordered by seq, not executed_at, for the same reason PriceStats is: seq is the
// true execution order and survives two trades sharing a timestamp. An
// instrument that has never traded is simply absent from the map — the caller
// distinguishes that from a price of 0.
func (r *Index) LastPrices(ctx context.Context) (map[int64]int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT DISTINCT ON (emiten_id) emiten_id, price
		FROM trades
		ORDER BY emiten_id, seq DESC`)
	if err != nil {
		return nil, fmt.Errorf("repository: last prices: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]int64)
	for rows.Next() {
		var emitenID, price int64
		if err := rows.Scan(&emitenID, &price); err != nil {
			return nil, fmt.Errorf("repository: scan last price: %w", err)
		}
		out[emitenID] = price
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("repository: iterate last prices: %w", err)
	}
	return out, nil
}
