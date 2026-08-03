package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/corporateaction"
	"bekasi-automatic-trading-system/platform/postgres"
)

// CorporateAction reads and writes aksi korporasi and the ledger movements they
// cause. It satisfies corporateaction.Repository.
type CorporateAction struct {
	db
}

// NewCorporateAction returns a corporate action repository backed by pool.
func NewCorporateAction(pool *pgxpool.Pool) *CorporateAction {
	return &CorporateAction{db{pool: pool}}
}

var _ corporateaction.Repository = (*CorporateAction)(nil)

// actionColumns is the select list every action read shares, so the scan
// function below can serve all of them.
const actionColumns = `
	id, emiten_id, jenis, status, ratio_from, ratio_to, amount,
	record_date, keterangan, created_at, executed_at`

func scanAction(rows pgx.Rows) (corporateaction.Record, error) {
	var rec corporateaction.Record
	err := rows.Scan(&rec.ID, &rec.EmitenID, &rec.Jenis, &rec.Status,
		&rec.RatioFrom, &rec.RatioTo, &rec.Amount,
		&rec.RecordDate, &rec.Keterangan, &rec.CreatedAt, &rec.ExecutedAt)
	return rec, err
}

// CreateAction records an announced action and returns it with its assigned id.
func (r *CorporateAction) CreateAction(ctx context.Context, rec corporateaction.Record) (corporateaction.Record, error) {
	rows, err := r.pool.Query(ctx, `
		INSERT INTO corporate_action
		    (emiten_id, jenis, status, ratio_from, ratio_to, amount, record_date, keterangan)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING`+actionColumns,
		rec.EmitenID, string(rec.Jenis), string(rec.Status),
		rec.RatioFrom, rec.RatioTo, rec.Amount, rec.RecordDate, rec.Keterangan)
	if err != nil {
		return corporateaction.Record{}, fmt.Errorf("repository: insert corporate action: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return corporateaction.Record{}, fmt.Errorf("repository: insert corporate action: %w", err)
		}
		return corporateaction.Record{}, fmt.Errorf("repository: insert corporate action: no row returned")
	}
	return scanAction(rows)
}

// ListActions returns one page of actions, newest first. A nil emitenID means
// every instrument.
//
// Newest first because an operator asking what is going on wants the pending and
// recent actions, not the ones settled a year ago. The id tiebreak keeps the
// order total, so the same page always reads back identically — created_at alone
// would leave two actions announced in the same transaction free to swap places
// between requests.
func (r *CorporateAction) ListActions(ctx context.Context, emitenID *int64, limit, offset int) ([]corporateaction.Record, error) {
	return postgres.QueryAll(ctx, r.pool, "corporate actions", `
		SELECT`+actionColumns+`
		FROM corporate_action
		WHERE ($1::bigint IS NULL OR emiten_id = $1)
		ORDER BY created_at DESC, id DESC
		LIMIT $2 OFFSET $3`,
		scanAction, emitenID, limit, offset)
}

// CountActions returns the total matching the same filter, for the pagination
// envelope.
func (r *CorporateAction) CountActions(ctx context.Context, emitenID *int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `
		SELECT count(*) FROM corporate_action
		WHERE ($1::bigint IS NULL OR emiten_id = $1)`, emitenID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("repository: count corporate actions: %w", err)
	}
	return n, nil
}

// FindAction returns one action, or corporateaction.ErrNotFound — so the service
// can answer 404 rather than 500.
func (r *CorporateAction) FindAction(ctx context.Context, id int64) (corporateaction.Record, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT`+actionColumns+`
		FROM corporate_action WHERE id = $1`, id)
	if err != nil {
		return corporateaction.Record{}, fmt.Errorf("repository: find corporate action %d: %w", id, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return corporateaction.Record{}, fmt.Errorf("repository: find corporate action %d: %w", id, err)
		}
		return corporateaction.Record{}, corporateaction.ErrNotFound
	}

	rec, err := scanAction(rows)
	if err != nil {
		return corporateaction.Record{}, fmt.Errorf("repository: find corporate action %d: %w", id, err)
	}
	return rec, nil
}

// EntriesByAction returns what each broker received, joined to the participant
// code and name a reader wants. Ordered by code so the same action always reads
// back identically.
func (r *CorporateAction) EntriesByAction(ctx context.Context, actionID int64) ([]corporateaction.Entry, error) {
	return postgres.QueryAll(ctx, r.pool, "corporate action entries", `
		SELECT e.participant_id, p.kode, p.nama,
		       e.shares_before, e.shares_after, e.cash_amount
		FROM corporate_action_entry e
		JOIN participant p ON p.id = e.participant_id
		WHERE e.action_id = $1
		ORDER BY p.kode`,
		func(rows pgx.Rows) (corporateaction.Entry, error) {
			var en corporateaction.Entry
			err := rows.Scan(&en.ParticipantID, &en.ParticipantKode, &en.ParticipantNama,
				&en.SharesBefore, &en.SharesAfter, &en.CashAmount)
			return en, err
		}, actionID)
}

// HoldersOf returns every broker with a non-zero position in this instrument.
//
// Zero rows are excluded rather than distributed to: a broker holding nothing
// receives nothing from any of the four kinds, and an entry recording that would
// be noise in the audit trail. Ordered by participant so the write order below is
// deterministic, which is what keeps concurrent transactions taking row locks in
// the same sequence.
func (r *CorporateAction) HoldersOf(ctx context.Context, emitenID int64) ([]corporateaction.Holding, error) {
	return postgres.QueryAll(ctx, r.pool, "corporate action holders", `
		SELECT participant_id, amount_shared
		FROM broker_assets_list
		WHERE emiten_id = $1 AND amount_shared > 0
		ORDER BY participant_id`,
		func(rows pgx.Rows) (corporateaction.Holding, error) {
			var h corporateaction.Holding
			err := rows.Scan(&h.ParticipantID, &h.Shares)
			return h, err
		}, emitenID)
}

// CancelAction moves an announced action to CANCELLED.
//
// The status check is in the WHERE clause, not in the service. Only the database
// sees two concurrent requests, and a check in the process would let a cancel and
// an execute both pass before either wrote — leaving an action that is cancelled
// and yet has moved every holder's ledger.
//
// An affected-row count of zero means the action is not announced, not that it is
// missing: the service reads it before calling this.
func (r *CorporateAction) CancelAction(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE corporate_action
		SET status = 'CANCELLED'
		WHERE id = $1 AND status = 'ANNOUNCED'`, id)
	if err != nil {
		return fmt.Errorf("repository: cancel corporate action %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return corporateaction.ErrNotAnnounced
	}
	return nil
}

// ExecuteShareAction applies a split, reverse split, or bonus in one transaction.
//
// Four things move together: the action's status, every holder's share count, the
// audit entries, and the instrument's own listed_shares and band reference. A
// partial write here is unrecoverable in a way an ordinary failed request is not
// — holdings restated without the listed_shares to match would leave the
// instrument's share count disagreeing with the sum of its holders' positions,
// with no record of which brokers were already converted.
//
// The status UPDATE runs first and gates the rest: if it affects no rows, the
// action is not announced and the transaction rolls back before a single holding
// has been touched. That is what makes a double execution impossible rather than
// merely unlikely — two concurrent requests serialize on that row, and the second
// finds it already EXECUTED.
//
// Holdings are written with an absolute SET rather than a delta, because the
// value was computed from the shares_before this transaction is also recording:
// a delta would be applied to whatever the row says now, and the entry would
// document a restatement that did not happen.
func (r *CorporateAction) ExecuteShareAction(ctx context.Context, id, emitenID, listedShares int64, reference *int64, entries []corporateaction.Entry) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if err := claimAction(ctx, tx, id); err != nil {
			return err
		}

		batch := &pgx.Batch{}
		for _, en := range entries {
			batch.Queue(`
				UPDATE broker_assets_list
				SET amount_shared = $3, updated_at = now()
				WHERE participant_id = $1 AND emiten_id = $2`,
				en.ParticipantID, emitenID, en.SharesAfter)

			batch.Queue(`
				INSERT INTO corporate_action_entry
				    (action_id, participant_id, shares_before, shares_after, cash_amount)
				VALUES ($1, $2, $3, $4, 0)`,
				id, en.ParticipantID, en.SharesBefore, en.SharesAfter)
		}

		// The instrument's own count, restated by the same ratio. reference_price
		// moves with it: the instrument is worth the same immediately after a split
		// as immediately before, so an anchor left alone would auto-reject every
		// order at the new fair value. COALESCE leaves it untouched when the caller
		// passes nil, which is an instrument that has no anchor to restate.
		batch.Queue(`
			UPDATE emiten
			SET listed_shares   = $2,
			    reference_price = COALESCE($3, reference_price)
			WHERE id = $1`,
			emitenID, listedShares, reference)

		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: entries for corporate action %d", ErrDuplicate, id)
			}
			return fmt.Errorf("repository: execute share action %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, corporateaction.ErrNotAnnounced) {
			return err
		}
		return fmt.Errorf("repository: execute share action %d: %w", id, err)
	}
	return nil
}

// ExecuteDividend applies a dividend in one transaction: the wallet credits, the
// audit entries, and the action's status.
//
// Same reasoning as ExecuteShareAction — brokers paid with no record of payment
// cannot be told apart from brokers an operator funded, and the status claim
// gates the rest so a dividend cannot pay out twice.
//
// The wallet row may not exist: a broker that holds shares from an IPO allocation
// but has never been funded or traded has none. Hence the ensure-row insert
// before the update, the same shape AdjustWallet uses and for the same reason —
// Postgres evaluates CHECK constraints on the proposed insert tuple before
// conflict arbitration, so folding the amount into the insert would have the
// constraint judge the wrong number.
func (r *CorporateAction) ExecuteDividend(ctx context.Context, id int64, entries []corporateaction.Entry) error {
	err := pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		if err := claimAction(ctx, tx, id); err != nil {
			return err
		}

		batch := &pgx.Batch{}
		for _, en := range entries {
			if en.CashAmount > 0 {
				batch.Queue(`
					INSERT INTO broker_wallet (participant_id, balance)
					VALUES ($1, 0)
					ON CONFLICT (participant_id) DO NOTHING`,
					en.ParticipantID)
				batch.Queue(`
					UPDATE broker_wallet
					SET balance = balance + $2, updated_at = now()
					WHERE participant_id = $1`,
					en.ParticipantID, en.CashAmount)
			}

			batch.Queue(`
				INSERT INTO corporate_action_entry
				    (action_id, participant_id, shares_before, shares_after, cash_amount)
				VALUES ($1, $2, $3, $4, $5)`,
				id, en.ParticipantID, en.SharesBefore, en.SharesAfter, en.CashAmount)
		}

		if err := tx.SendBatch(ctx, batch).Close(); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("%w: entries for corporate action %d", ErrDuplicate, id)
			}
			return fmt.Errorf("repository: execute dividend %d: %w", id, err)
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, corporateaction.ErrNotAnnounced) {
			return err
		}
		return fmt.Errorf("repository: execute dividend %d: %w", id, err)
	}
	return nil
}

// claimAction marks an announced action EXECUTED, returning ErrNotAnnounced if it
// is not announced.
//
// It runs first inside both execution transactions, and everything else is
// conditional on it. That ordering is the whole concurrency guarantee: two
// requests to execute the same action serialize on this row, and the loser rolls
// back before touching a ledger. Checking the status in the service instead would
// leave a window where both passed.
func claimAction(ctx context.Context, tx pgx.Tx, id int64) error {
	tag, err := tx.Exec(ctx, `
		UPDATE corporate_action
		SET status = 'EXECUTED', executed_at = now()
		WHERE id = $1 AND status = 'ANNOUNCED'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return corporateaction.ErrNotAnnounced
	}
	return nil
}
