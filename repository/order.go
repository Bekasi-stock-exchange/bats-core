package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/platform/postgres"
)

// Order persists orders and trades. It satisfies order.Repository.
type Order struct {
	db
}

// NewOrder returns an order repository backed by pool.
func NewOrder(pool *pgxpool.Pool) *Order {
	return &Order{db{pool: pool}}
}

// compile-time check that Order satisfies the interface the order package declares.
var _ order.Repository = (*Order)(nil)

// NextOrderID reserves the next order id from the table's identity sequence.
//
// The id is handed out before the row exists because trades reference it as a
// foreign key, while the row itself can only be written after matching has decided
// its sequence number and final state.
func (r *Order) NextOrderID(ctx context.Context) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`SELECT nextval(pg_get_serial_sequence('orders', 'id'))`).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("repository: next order id: %w", err)
	}
	return id, nil
}

// SaveExecution writes a whole matching outcome in one transaction: the incoming
// order, the trades it produced, and the fills against the resting orders it
// consumed.
//
// All of it commits or none of it does. Previously these were separate autocommit
// statements, so a failure part-way left the database holding a partial outcome
// that could never be reconciled with the in-memory book.
func (r *Order) SaveExecution(ctx context.Context, ex order.Execution) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository: begin: %w", err)
	}
	// Rolls back unless the commit below already succeeded.
	defer func() { _ = tx.Rollback(ctx) }()

	if err := insertOrder(ctx, tx, ex.Order); err != nil {
		return err
	}
	if err := insertTrades(ctx, tx, ex.Trades); err != nil {
		return err
	}
	if err := applyFills(ctx, tx, ex.Fills); err != nil {
		return err
	}
	if err := applyAssetDeltas(ctx, tx, ex.Assets); err != nil {
		return err
	}
	if err := applyWalletDeltas(ctx, tx, ex.Wallets); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository: commit execution: %w", err)
	}
	return nil
}

// ListOrders returns one page of order history, newest first.
func (r *Order) ListOrders(ctx context.Context, f order.OrderFilter, limit, offset int) ([]order.OrderRecord, error) {
	return postgres.QueryAll(ctx, r.pool, "orders page", `
		SELECT id, emiten_id, participant_id, side, type, price, qty, remaining, status, seq
		FROM orders
		WHERE ($1::bigint IS NULL OR emiten_id = $1)
		  AND ($2::bigint IS NULL OR participant_id = $2)
		  AND ($3::text   IS NULL OR status = $3)
		ORDER BY seq DESC
		LIMIT $4 OFFSET $5`,
		func(rows pgx.Rows) (order.OrderRecord, error) {
			var o order.OrderRecord
			err := rows.Scan(&o.ID, &o.EmitenID, &o.ParticipantID, &o.Side, &o.Type,
				&o.Price, &o.Qty, &o.Remaining, &o.Status, &o.Seq)
			return o, err
		}, f.EmitenID, f.ParticipantID, f.Status, limit, offset)
}

// CountOrders totals the same filter, for the pagination envelope.
func (r *Order) CountOrders(ctx context.Context, f order.OrderFilter) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM orders
		WHERE ($1::bigint IS NULL OR emiten_id = $1)
		  AND ($2::bigint IS NULL OR participant_id = $2)
		  AND ($3::text   IS NULL OR status = $3)`,
		f.EmitenID, f.ParticipantID, f.Status).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository: count orders: %w", err)
	}
	return n, nil
}

// MaxSeqs returns the highest seq already used in orders and in trades.
func (r *Order) MaxSeqs(ctx context.Context) (maxOrderSeq, maxTradeSeq int64, err error) {
	if err = r.pool.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) FROM orders`).Scan(&maxOrderSeq); err != nil {
		return 0, 0, fmt.Errorf("repository: max order seq: %w", err)
	}
	if err = r.pool.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) FROM trades`).Scan(&maxTradeSeq); err != nil {
		return 0, 0, fmt.Errorf("repository: max trade seq: %w", err)
	}
	return maxOrderSeq, maxTradeSeq, nil
}

// insertOrder writes the order row at its final post-matching state, with the id
// reserved earlier and the sequence number the engine assigned.
func insertOrder(ctx context.Context, tx pgx.Tx, o order.OrderRecord) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO orders (id, emiten_id, participant_id, side, type, price, qty, remaining, status, seq)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		o.ID, o.EmitenID, o.ParticipantID, o.Side, o.Type, o.Price, o.Qty, o.Remaining, o.Status, o.Seq,
	)
	if err != nil {
		return fmt.Errorf("repository: insert order %d: %w", o.ID, err)
	}
	return nil
}

// applyFills decrements each resting order's remaining by the quantity it traded
// and marks it filled when it reaches zero. Derived status and derived remaining
// are set in one statement so they cannot disagree.
func applyFills(ctx context.Context, tx pgx.Tx, fills []order.Fill) error {
	if len(fills) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, f := range fills {
		batch.Queue(`
			UPDATE orders
			SET remaining = remaining - $1,
			    status    = CASE WHEN remaining - $1 = 0 THEN 'filled' ELSE status END
			WHERE id = $2`, f.Qty, f.OrderID)
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("repository: apply fills: %w", err)
	}
	return nil
}
