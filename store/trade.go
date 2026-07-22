package store

import (
	"context"
	"fmt"
)

// OrderRow is the persisted shape of an order (history/audit). The matching
// source of truth is the in-memory book, not this table.
type OrderRow struct {
	ID            int64
	EmitenID      int64
	ParticipantID int64
	Side          string
	Type          string
	Price         int64
	Qty           int64
	Remaining     int64
	Status        string
	Seq           int64
}

// TradeRow is the persisted shape of an executed trade.
type TradeRow struct {
	EmitenID    int64
	BuyOrderID  int64
	SellOrderID int64
	Price       int64
	Qty         int64
	Seq         int64
}

// InsertOrder writes a new order row and returns its DB-generated id. The id is
// assigned by PostgreSQL (GENERATED ALWAYS AS IDENTITY), so callers must insert
// before matching in order to obtain a stable order id for the trade FKs.
func (s *Store) InsertOrder(ctx context.Context, o OrderRow) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO orders (emiten_id, participant_id, side, type, price, qty, remaining, status, seq)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`,
		o.EmitenID, o.ParticipantID, o.Side, o.Type, o.Price, o.Qty, o.Remaining, o.Status, o.Seq,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("store: insert order: %w", err)
	}
	return id, nil
}

// UpdateOrderSeq stores the engine-assigned sequence number for an order. Seq is
// unknown at insert time (assigned by the engine during matching) so it is
// written back afterward.
func (s *Store) UpdateOrderSeq(ctx context.Context, id, seq int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE orders SET seq = $1 WHERE id = $2`, seq, id)
	if err != nil {
		return fmt.Errorf("store: update order seq %d: %w", id, err)
	}
	return nil
}

// UpdateOrderResult persists the post-matching state (remaining + status) of an
// order that already has a row.
func (s *Store) UpdateOrderResult(ctx context.Context, id, remaining int64, status string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE orders SET remaining = $1, status = $2 WHERE id = $3`,
		remaining, status, id)
	if err != nil {
		return fmt.Errorf("store: update order %d: %w", id, err)
	}
	return nil
}

// MaxSeqs returns the highest seq already used in orders and in trades (0 when a
// table is empty). Used at startup to seed the engine's sequencer so newly
// assigned seq values never collide with persisted ones across restarts.
func (s *Store) MaxSeqs(ctx context.Context) (maxOrderSeq, maxTradeSeq int64, err error) {
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) FROM orders`).Scan(&maxOrderSeq); err != nil {
		return 0, 0, fmt.Errorf("store: max order seq: %w", err)
	}
	if err = s.pool.QueryRow(ctx, `SELECT COALESCE(max(seq), 0) FROM trades`).Scan(&maxTradeSeq); err != nil {
		return 0, 0, fmt.Errorf("store: max trade seq: %w", err)
	}
	return maxOrderSeq, maxTradeSeq, nil
}

// ApplyFill decrements a passive order's remaining by qty and, if it reaches
// zero, marks it filled. Done in a single statement so the derived status stays
// consistent with the derived remaining.
func (s *Store) ApplyFill(ctx context.Context, id, qty int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE orders
		SET remaining = remaining - $1,
		    status    = CASE WHEN remaining - $1 = 0 THEN 'filled' ELSE status END
		WHERE id = $2`,
		qty, id)
	if err != nil {
		return fmt.Errorf("store: apply fill order %d: %w", id, err)
	}
	return nil
}

// InsertTrades writes executed trade rows. Updating the resting orders'
// remaining/status is the caller's responsibility via UpdateOrderResult.
func (s *Store) InsertTrades(ctx context.Context, trades []TradeRow) error {
	if len(trades) == 0 {
		return nil
	}
	batch := make([][]any, 0, len(trades))
	for _, t := range trades {
		batch = append(batch, []any{t.EmitenID, t.BuyOrderID, t.SellOrderID, t.Price, t.Qty, t.Seq})
	}
	for _, args := range batch {
		_, err := s.pool.Exec(ctx, `
			INSERT INTO trades (emiten_id, buy_order_id, sell_order_id, price, qty, seq)
			VALUES ($1, $2, $3, $4, $5, $6)`, args...)
		if err != nil {
			return fmt.Errorf("store: insert trade: %w", err)
		}
	}
	return nil
}
