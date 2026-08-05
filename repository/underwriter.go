package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/platform/postgres"
	"bekasi-automatic-trading-system/underwriter"
)

// Underwriter reads and writes underwriters and their IPO allocations. It
// satisfies underwriter.Repository.
type Underwriter struct {
	db
}

func NewUnderwriter(pool *pgxpool.Pool) *Underwriter {
	return &Underwriter{db{pool: pool}}
}

var _ underwriter.Repository = (*Underwriter)(nil)

// Ordered by broker code.
//
// The join is for the ordering only: an underwriter has no code of its own, and
// ordering by participant_id would sort by insertion order rather than by anything
// a reader recognises. The code and name themselves are resolved from the
// in-memory directory by the transformer, not carried on the row.
func (r *Underwriter) ListUnderwriters(ctx context.Context) ([]underwriter.Record, error) {
	return postgres.QueryAll(ctx, r.pool, "underwriters",
		`SELECT u.id, u.participant_id, u.is_active
		 FROM underwriter u
		 JOIN participant p ON p.id = u.participant_id
		 ORDER BY p.kode`,
		scanUnderwriter)
}

// Returns underwriter.ErrNotFound when that broker is not registered as one — so
// the service can answer 400 rather than 500.
func (r *Underwriter) UnderwriterByParticipant(ctx context.Context, kode string) (underwriter.Record, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT u.id, u.participant_id, u.is_active
		 FROM underwriter u
		 JOIN participant p ON p.id = u.participant_id
		 WHERE p.kode = $1`, kode)
	if err != nil {
		return underwriter.Record{}, fmt.Errorf("repository: underwriter %s: %w", kode, err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return underwriter.Record{}, fmt.Errorf("repository: underwriter %s: %w", kode, err)
		}
		return underwriter.Record{}, underwriter.ErrNotFound
	}

	rec, err := scanUnderwriter(rows)
	if err != nil {
		return underwriter.Record{}, fmt.Errorf("repository: underwriter %s: %w", kode, err)
	}
	return rec, nil
}

func scanUnderwriter(rows pgx.Rows) (underwriter.Record, error) {
	var rec underwriter.Record
	err := rows.Scan(&rec.ID, &rec.ParticipantID, &rec.IsActive)
	return rec, err
}

// Registering the same broker twice violates the unique index on participant_id
// and surfaces as ErrDuplicate rather than a raw driver error, so the controller
// can answer 409 instead of 500.
func (r *Underwriter) CreateUnderwriter(ctx context.Context, u underwriter.Record) (underwriter.Record, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO underwriter (participant_id, is_active)
		VALUES ($1, $2)
		RETURNING id`,
		u.ParticipantID, u.IsActive,
	).Scan(&u.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return underwriter.Record{}, fmt.Errorf("%w: underwriter for participant %d",
				ErrDuplicate, u.ParticipantID)
		}
		return underwriter.Record{}, fmt.Errorf("repository: insert underwriter for participant %d: %w",
			u.ParticipantID, err)
	}
	return u, nil
}

// Writes the ipo_allocation audit rows and the broker_assets_list credits that
// actually move the shares.
//
// One transaction, because the two halves are the same fact. Credits without audit
// rows are shares nobody can explain; audit rows without credits are an offering
// whose shares never arrived.
//
// The credit is an ensure-row insert followed by an UPDATE rather than a single
// ON CONFLICT DO UPDATE, for the same reason applyAssetDeltas does it: Postgres
// evaluates CHECK constraints on the proposed insert tuple before conflict
// arbitration, so folding the amount into the insert would have the constraint
// judge the wrong number.
func (r *Underwriter) AllocateIPO(ctx context.Context, emitenID, price int64, allocs []underwriter.Allocation) error {
	if len(allocs) == 0 {
		return nil
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("repository: begin: %w", err)
	}
	// Rolls back unless the commit below already succeeded.
	defer func() { _ = tx.Rollback(ctx) }()

	batch := &pgx.Batch{}
	for _, a := range allocs {
		batch.Queue(`
			INSERT INTO ipo_allocation
			    (emiten_id, underwriter_id, participant_id, shares, price)
			VALUES ($1, $2, $3, $4, $5)`,
			emitenID, a.UnderwriterID, a.ParticipantID, a.Shares, price)

		batch.Queue(`
			INSERT INTO broker_assets_list (participant_id, emiten_id, amount_shared)
			VALUES ($1, $2, 0)
			ON CONFLICT (participant_id, emiten_id) DO NOTHING`,
			a.ParticipantID, emitenID)
		batch.Queue(`
			UPDATE broker_assets_list
			SET amount_shared = amount_shared + $3,
			    updated_at    = now()
			WHERE participant_id = $1 AND emiten_id = $2`,
			a.ParticipantID, emitenID, a.Shares)
	}

	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: allocation for emiten %d", ErrDuplicate, emitenID)
		}
		return fmt.Errorf("repository: allocate ipo for emiten %d: %w", emitenID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("repository: commit ipo allocation: %w", err)
	}
	return nil
}

// Largest tranche first — the order the syndicate is actually weighted in. Ties
// break on the broker code
// so the ordering is total, and the same offering always reads back identically.
//
// It joins participant only: the underwriter row carries no name, and the shares
// were credited to the participant anyway.
func (r *Underwriter) AllocationsByEmiten(ctx context.Context, emitenID int64) ([]underwriter.AllocationRecord, error) {
	return postgres.QueryAll(ctx, r.pool, "ipo allocations",
		`SELECT p.kode, p.nama, a.shares, a.price
		 FROM ipo_allocation a
		 JOIN participant p ON p.id = a.participant_id
		 WHERE a.emiten_id = $1
		 ORDER BY a.shares DESC, p.kode`,
		func(rows pgx.Rows) (underwriter.AllocationRecord, error) {
			var rec underwriter.AllocationRecord
			err := rows.Scan(&rec.ParticipantKode, &rec.ParticipantNama, &rec.Shares, &rec.Price)
			return rec, err
		}, emitenID)
}
