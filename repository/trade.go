package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bekasi-automatic-trading-system/order"
)

// insertTrades writes executed trade rows.
//
// One pgx.Batch, one round trip. Updating the resting orders' remaining and status
// is applyFills' job, in the same transaction.
func insertTrades(ctx context.Context, tx pgx.Tx, trades []order.TradeRecord) error {
	if len(trades) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, t := range trades {
		batch.Queue(`
			INSERT INTO trades (emiten_id, buy_order_id, sell_order_id, price, qty, seq)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			t.EmitenID, t.BuyOrderID, t.SellOrderID, t.Price, t.Qty, t.Seq)
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("repository: insert trades: %w", err)
	}
	return nil
}
