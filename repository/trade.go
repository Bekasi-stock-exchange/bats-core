package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/emiten"
	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/platform/postgres"
	"bekasi-automatic-trading-system/trade"
)

// Trade reads executions. It satisfies trade.Repository and
// emiten.PriceStatsRepository — both are views over this one table.
type Trade struct {
	db
}

func NewTrade(pool *pgxpool.Pool) *Trade {
	return &Trade{db{pool: pool}}
}

var (
	_ trade.Repository            = (*Trade)(nil)
	_ emiten.PriceStatsRepository = (*Trade)(nil)
)

// All-time high, low, and latest price.
//
// Every value is a pointer: an instrument that has never traded has no price, and
// reporting 0 would claim it is worth nothing. MAX/MIN over an empty set are NULL
// in SQL, which maps straight onto that.
func (r *Trade) PriceStats(ctx context.Context, emitenID int64) (emiten.PriceStats, error) {
	var stats emiten.PriceStats

	err := r.pool.QueryRow(ctx,
		`SELECT MAX(price), MIN(price) FROM trades WHERE emiten_id = $1`, emitenID).
		Scan(&stats.Highest, &stats.Lowest)
	if err != nil {
		return emiten.PriceStats{}, fmt.Errorf("repository: price stats %d: %w", emitenID, err)
	}

	// Latest by seq, not executed_at: seq is the monotonic execution order, and
	// two trades can share a timestamp. Served by idx_trades_emiten_seq.
	var current int64
	err = r.pool.QueryRow(ctx,
		`SELECT price FROM trades WHERE emiten_id = $1 ORDER BY seq DESC LIMIT 1`, emitenID).
		Scan(&current)
	switch {
	case err == nil:
		stats.Current = &current
	case errors.Is(err, pgx.ErrNoRows):
		// Never traded; Current stays nil.
	default:
		return emiten.PriceStats{}, fmt.Errorf("repository: last price %d: %w", emitenID, err)
	}
	return stats, nil
}

// One pgx.Batch, one round trip. Updating the resting orders' remaining and status
// is applyFills' job, in the same transaction.
func insertTrades(ctx context.Context, tx pgx.Tx, trades []order.TradeRecord) error {
	if len(trades) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, t := range trades {
		batch.Queue(`
			INSERT INTO trades (emiten_id, buy_order_id, sell_order_id,
			                    buy_participant_id, sell_participant_id, price, qty, seq)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			t.EmitenID, t.BuyOrderID, t.SellOrderID,
			t.BuyParticipantID, t.SellParticipantID, t.Price, t.Qty, t.Seq)
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("repository: insert trades: %w", err)
	}
	return nil
}

// The raw execution log, newest first.
func (r *Trade) ListTrades(ctx context.Context, f trade.Filter, limit, offset int) ([]trade.Record, error) {
	return postgres.QueryAll(ctx, r.pool, "trades page", `
		SELECT id, seq, emiten_id, buy_order_id, sell_order_id,
		       buy_participant_id, sell_participant_id, price, qty, executed_at
		FROM trades
		WHERE ($1::bigint IS NULL OR emiten_id = $1)
		  AND ($2::bigint IS NULL OR buy_participant_id = $2 OR sell_participant_id = $2)
		ORDER BY seq DESC
		LIMIT $3 OFFSET $4`,
		func(rows pgx.Rows) (trade.Record, error) {
			var t trade.Record
			err := rows.Scan(&t.ID, &t.Seq, &t.EmitenID, &t.BuyOrderID, &t.SellOrderID,
				&t.BuyParticipantID, &t.SellParticipantID, &t.Price, &t.Qty, &t.ExecutedAt)
			return t, err
		}, f.EmitenID, f.ParticipantID, limit, offset)
}

// Same filter as ListTrades, for the pagination envelope.
func (r *Trade) CountTrades(ctx context.Context, f trade.Filter) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM trades
		WHERE ($1::bigint IS NULL OR emiten_id = $1)
		  AND ($2::bigint IS NULL OR buy_participant_id = $2 OR sell_participant_id = $2)`,
		f.EmitenID, f.ParticipantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository: count trades: %w", err)
	}
	return n, nil
}

// Selects a broker's fills from its own point of view.
//
// A UNION ALL of the two sides rather than a single OR: each half is then a plain
// index lookup on idx_trades_buy_participant / idx_trades_sell_participant, where
// an OR across the two columns could not use either.
//
// A self-trade — a broker's order matching its own resting order — deliberately
// yields two rows, one per side. That is what makes buys minus sells reconcile
// against the holdings change.
const transactionsQuery = `
	SELECT id, seq, emiten_id, 'buy' AS side, sell_participant_id AS counterparty_id,
	       price, qty, executed_at
	FROM trades
	WHERE buy_participant_id = $1 AND ($2::bigint IS NULL OR emiten_id = $2)
	UNION ALL
	SELECT id, seq, emiten_id, 'sell' AS side, buy_participant_id AS counterparty_id,
	       price, qty, executed_at
	FROM trades
	WHERE sell_participant_id = $1 AND ($2::bigint IS NULL OR emiten_id = $2)
	ORDER BY seq DESC
	LIMIT $3 OFFSET $4`

func (r *Trade) ListTransactions(ctx context.Context, participantID int64, emitenID *int64, limit, offset int) ([]trade.Transaction, error) {
	return postgres.QueryAll(ctx, r.pool, "transactions page", transactionsQuery,
		func(rows pgx.Rows) (trade.Transaction, error) {
			var t trade.Transaction
			err := rows.Scan(&t.TradeID, &t.Seq, &t.EmitenID, &t.Side, &t.CounterpartyID,
				&t.Price, &t.Qty, &t.ExecutedAt)
			return t, err
		}, participantID, emitenID, limit, offset)
}

// Both sides are counted, so a self-trade contributes 2 — matching what
// ListTransactions returns.
func (r *Trade) CountTransactions(ctx context.Context, participantID int64, emitenID *int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT
		    (SELECT count(*) FROM trades
		      WHERE buy_participant_id = $1 AND ($2::bigint IS NULL OR emiten_id = $2))
		  + (SELECT count(*) FROM trades
		      WHERE sell_participant_id = $1 AND ($2::bigint IS NULL OR emiten_id = $2))`,
		participantID, emitenID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository: count transactions: %w", err)
	}
	return n, nil
}

func (r *Trade) ListTicks(ctx context.Context, emitenID int64, limit, offset int) ([]trade.Tick, error) {
	return postgres.QueryAll(ctx, r.pool, "ticks page", `
		SELECT seq, price, qty, executed_at
		FROM trades WHERE emiten_id = $1
		ORDER BY seq DESC
		LIMIT $2 OFFSET $3`,
		func(rows pgx.Rows) (trade.Tick, error) {
			var t trade.Tick
			err := rows.Scan(&t.Seq, &t.Price, &t.Qty, &t.ExecutedAt)
			return t, err
		}, emitenID, limit, offset)
}

func (r *Trade) CountTicks(ctx context.Context, emitenID int64) (int, error) {
	var n int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM trades WHERE emiten_id = $1`, emitenID).Scan(&n); err != nil {
		return 0, fmt.Errorf("repository: count ticks: %w", err)
	}
	return n, nil
}

// Aggregates executions into OHLC bars, newest bar first.
//
// Buckets are floored on the epoch rather than cut with date_trunc, because
// date_trunc has no 5-minute unit; flooring handles every width uniformly and
// takes the width as an integer parameter. Buckets are UTC-aligned.
//
// Open and close come from ordering by seq, not executed_at: seq is the true
// execution order and survives two trades sharing a timestamp.
func (r *Trade) ListCandles(ctx context.Context, emitenID, intervalSeconds int64, limit int) ([]trade.Candle, error) {
	return postgres.QueryAll(ctx, r.pool, "candles", `
		SELECT bucket, open, high, low, close, volume FROM (
		    SELECT to_timestamp(floor(extract(epoch FROM executed_at) / $2) * $2) AS bucket,
		           (array_agg(price ORDER BY seq ASC))[1]                         AS open,
		           max(price)                                                     AS high,
		           min(price)                                                     AS low,
		           (array_agg(price ORDER BY seq DESC))[1]                        AS close,
		           sum(qty)                                                        AS volume
		    FROM trades
		    WHERE emiten_id = $1
		    GROUP BY bucket
		) c
		ORDER BY bucket DESC
		LIMIT $3`,
		func(rows pgx.Rows) (trade.Candle, error) {
			var c trade.Candle
			err := rows.Scan(&c.Time, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume)
			return c, err
		}, emitenID, intervalSeconds, limit)
}
