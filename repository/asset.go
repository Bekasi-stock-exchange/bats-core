package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/assets"
	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/platform/postgres"
)

// Asset reads broker share holdings. It satisfies assets.Repository.
type Asset struct {
	db
}

// NewAsset returns a holdings repository backed by pool.
func NewAsset(pool *pgxpool.Pool) *Asset {
	return &Asset{db{pool: pool}}
}

var _ assets.Repository = (*Asset)(nil)

// LoadHoldings returns every broker holding, to seed the in-memory ledger at
// startup.
func (r *Asset) LoadHoldings(ctx context.Context) ([]market.Holding, error) {
	return postgres.QueryAll(ctx, r.pool, "broker holdings",
		`SELECT participant_id, emiten_id, amount_shared FROM broker_assets_list`,
		func(rows pgx.Rows) (market.Holding, error) {
			var h market.Holding
			err := rows.Scan(&h.ParticipantID, &h.EmitenID, &h.AmountShared)
			return h, err
		})
}

// holdingsQuery selects holdings with their market value derived from the latest
// trade price.
//
// Value is computed here rather than stored because it depends on the last price:
// one trade in an emiten changes the value of every broker holding it, so a stored
// column would need a fan-out update on every match, or it would be stale for
// every broker that did not trade.
//
// LEFT JOIN LATERAL, not an inner join: an emiten that has never traded has no
// price, so value is NULL rather than 0 — a million shares are not worth nothing
// merely because the market has not opened.
const holdingsQuery = `
	SELECT b.participant_id, b.emiten_id, b.amount_shared, b.updated_at, lp.price
	FROM broker_assets_list b
	LEFT JOIN LATERAL (
	    SELECT price FROM trades
	    WHERE emiten_id = b.emiten_id
	    ORDER BY seq DESC
	    LIMIT 1
	) lp ON true
	WHERE ($1::bigint IS NULL OR b.participant_id = $1)
	ORDER BY b.participant_id, b.emiten_id
	LIMIT $2 OFFSET $3`

// scanHolding maps a holdings row, leaving Price nil when the emiten has never
// traded.
func scanHolding(rows pgx.Rows) (assets.Record, error) {
	var rec assets.Record
	err := rows.Scan(&rec.ParticipantID, &rec.EmitenID, &rec.AmountShared, &rec.UpdatedAt, &rec.LastPrice)
	return rec, err
}

// ListHoldings returns one page of holdings. A nil participantID means every
// broker; a non-nil one scopes to that broker alone.
func (r *Asset) ListHoldings(ctx context.Context, participantID *int64, limit, offset int) ([]assets.Record, error) {
	return postgres.QueryAll(ctx, r.pool, "broker holdings page",
		holdingsQuery, scanHolding, participantID, limit, offset)
}

// CountHoldings returns the total number of holdings matching the same filter, for
// the pagination envelope.
func (r *Asset) CountHoldings(ctx context.Context, participantID *int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM broker_assets_list
		WHERE ($1::bigint IS NULL OR participant_id = $1)`, participantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository: count holdings: %w", err)
	}
	return n, nil
}

// applyAssetDeltas moves shares between brokers as part of the match that caused
// them, so a holding can never disagree with the trades behind it.
//
// The caller nets and sorts the deltas: netting keeps one batch from targeting the
// same (participant, emiten) twice, which Postgres rejects with "cannot affect row
// a second time", and the ordering makes concurrent transactions take row locks in
// the same sequence.
//
// The row may not exist yet — a broker's first trade in an instrument creates it —
// hence the upsert. The CHECK (amount_shared >= 0) is the last line of defence: the
// sell was already checked against available shares before matching, so a violation
// here means the in-memory ledger and this table have drifted, and failing the
// transaction is the right outcome.
func applyAssetDeltas(ctx context.Context, tx pgx.Tx, deltas []order.AssetDelta) error {
	if len(deltas) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, d := range deltas {
		batch.Queue(`
			INSERT INTO broker_assets_list (participant_id, emiten_id, amount_shared)
			VALUES ($1, $2, $3)
			ON CONFLICT (participant_id, emiten_id) DO UPDATE
			SET amount_shared = broker_assets_list.amount_shared + EXCLUDED.amount_shared,
			    updated_at    = now()`,
			d.ParticipantID, d.EmitenID, d.Delta)
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("repository: apply asset deltas: %w", err)
	}
	return nil
}
