package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/platform/postgres"
	"bekasi-automatic-trading-system/wallet"
)

// Wallet reads broker cash balances. It satisfies wallet.Repository.
type Wallet struct {
	db
}

// NewWallet returns a wallet repository backed by pool.
func NewWallet(pool *pgxpool.Pool) *Wallet {
	return &Wallet{db{pool: pool}}
}

var _ wallet.Repository = (*Wallet)(nil)

// LoadWallets returns every broker wallet, to seed the in-memory cash ledger at
// startup.
func (r *Wallet) LoadWallets(ctx context.Context) ([]market.Wallet, error) {
	return postgres.QueryAll(ctx, r.pool, "broker wallets",
		`SELECT participant_id, balance FROM broker_wallet`,
		func(rows pgx.Rows) (market.Wallet, error) {
			var w market.Wallet
			err := rows.Scan(&w.ParticipantID, &w.Balance)
			return w, err
		})
}

func scanWallet(rows pgx.Rows) (wallet.Record, error) {
	var rec wallet.Record
	err := rows.Scan(&rec.ParticipantID, &rec.Balance, &rec.UpdatedAt)
	return rec, err
}

// ListWallets returns one page of wallets. A nil participantID means every
// broker; a non-nil one scopes to that broker alone.
func (r *Wallet) ListWallets(ctx context.Context, participantID *int64, limit, offset int) ([]wallet.Record, error) {
	return postgres.QueryAll(ctx, r.pool, "broker wallets page", `
		SELECT participant_id, balance, updated_at
		FROM broker_wallet
		WHERE ($1::bigint IS NULL OR participant_id = $1)
		ORDER BY participant_id
		LIMIT $2 OFFSET $3`,
		scanWallet, participantID, limit, offset)
}

// CountWallets returns the total number of wallets matching the same filter,
// for the pagination envelope.
func (r *Wallet) CountWallets(ctx context.Context, participantID *int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM broker_wallet
		WHERE ($1::bigint IS NULL OR participant_id = $1)`, participantID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository: count wallets: %w", err)
	}
	return n, nil
}

// FindWallet returns one broker's wallet.
func (r *Wallet) FindWallet(ctx context.Context, participantID int64) (wallet.Record, error) {
	row, err := r.pool.Query(ctx, `
		SELECT participant_id, balance, updated_at
		FROM broker_wallet
		WHERE participant_id = $1`, participantID)
	if err != nil {
		return wallet.Record{}, fmt.Errorf("repository: find wallet %d: %w", participantID, err)
	}
	defer row.Close()

	if !row.Next() {
		return wallet.Record{}, nil
	}
	return scanWallet(row)
}

// applyWalletDeltas moves cash between brokers as part of the trade that caused
// it, so a wallet can never disagree with the trades behind it.
//
// The caller nets and sorts the deltas, the same reasoning as
// applyAssetDeltas: netting keeps one batch from targeting the same participant
// twice, which Postgres rejects with "cannot affect row a second time", and the
// ordering makes concurrent transactions take row locks in the same sequence.
//
// The row may not exist yet — a broker's first trade creates it — hence the
// ensure-row insert. It cannot be a single ON CONFLICT DO UPDATE upsert carrying
// the delta: Postgres evaluates CHECK constraints on the proposed insert tuple
// *before* conflict arbitration, so a negative delta (every buyer) would violate
// CHECK (balance >= 0) even when the existing row easily covers it — see
// applyAssetDeltas. The CHECK is the last line of defence: the buy was already
// checked against available cash before matching, so a violation of the final
// balance means the in-memory ledger and this table have drifted, and failing
// the transaction is the right outcome.
func applyWalletDeltas(ctx context.Context, tx pgx.Tx, deltas []order.WalletDelta) error {
	if len(deltas) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, d := range deltas {
		batch.Queue(`
			INSERT INTO broker_wallet (participant_id, balance)
			VALUES ($1, 0)
			ON CONFLICT (participant_id) DO NOTHING`,
			d.ParticipantID)
		batch.Queue(`
			UPDATE broker_wallet
			SET balance    = balance + $2,
			    updated_at = now()
			WHERE participant_id = $1`,
			d.ParticipantID, d.Delta)
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("repository: apply wallet deltas: %w", err)
	}
	return nil
}
