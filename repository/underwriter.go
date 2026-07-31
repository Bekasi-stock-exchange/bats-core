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

// NewUnderwriter returns an underwriter repository backed by pool.
func NewUnderwriter(pool *pgxpool.Pool) *Underwriter {
	return &Underwriter{db{pool: pool}}
}

var _ underwriter.Repository = (*Underwriter)(nil)

// ListUnderwriters returns every registered underwriter, ordered by code.
func (r *Underwriter) ListUnderwriters(ctx context.Context) ([]underwriter.Record, error) {
	return postgres.QueryAll(ctx, r.pool, "underwriters",
		`SELECT id, kode, nama, jenis, participant_id, is_active
		 FROM underwriter ORDER BY kode`,
		scanUnderwriter)
}

// UnderwriterByKode looks up one underwriter, returning underwriter.ErrNotFound
// when the code is not registered so the service can answer 400 rather than 500.
func (r *Underwriter) UnderwriterByKode(ctx context.Context, kode string) (underwriter.Record, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, kode, nama, jenis, participant_id, is_active
		 FROM underwriter WHERE kode = $1`, kode)
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
	err := rows.Scan(&rec.ID, &rec.Kode, &rec.Nama, &rec.Jenis, &rec.ParticipantID, &rec.IsActive)
	return rec, err
}

// CreateUnderwriter inserts a new underwriter and returns it with its assigned id.
//
// A duplicate kode surfaces as ErrDuplicate rather than a raw driver error, so the
// controller can answer 409 instead of 500.
func (r *Underwriter) CreateUnderwriter(ctx context.Context, u underwriter.Record) (underwriter.Record, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO underwriter (kode, nama, jenis, participant_id, is_active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		u.Kode, u.Nama, u.Jenis, u.ParticipantID, u.IsActive,
	).Scan(&u.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return underwriter.Record{}, fmt.Errorf("%w: underwriter %s", ErrDuplicate, u.Kode)
		}
		return underwriter.Record{}, fmt.Errorf("repository: insert underwriter %s: %w", u.Kode, err)
	}
	return u, nil
}

// AllocateIPO writes an offering's allocations: the ipo_allocation audit rows and
// the broker_assets_list credits that actually move the shares.
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
			    (emiten_id, underwriter_id, participant_id, jenis, shares, price)
			VALUES ($1, $2, $3, $4, $5, $6)`,
			emitenID, a.UnderwriterID, a.ParticipantID, a.Jenis, a.Shares, price)

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

// AllocationsByEmiten returns an offering's syndicate, lead first and then the
// supporters largest-first — the order the syndicate is actually structured in.
func (r *Underwriter) AllocationsByEmiten(ctx context.Context, emitenID int64) ([]underwriter.AllocationRecord, error) {
	return postgres.QueryAll(ctx, r.pool, "ipo allocations",
		`SELECT u.kode, u.nama, p.kode, a.jenis, a.shares, a.price
		 FROM ipo_allocation a
		 JOIN underwriter u ON u.id = a.underwriter_id
		 JOIN participant  p ON p.id = a.participant_id
		 WHERE a.emiten_id = $1
		 ORDER BY (a.jenis = 'utama') DESC, a.shares DESC, u.kode`,
		func(rows pgx.Rows) (underwriter.AllocationRecord, error) {
			var rec underwriter.AllocationRecord
			err := rows.Scan(&rec.UnderwriterKode, &rec.UnderwriterNama, &rec.ParticipantKode,
				&rec.Jenis, &rec.Shares, &rec.Price)
			return rec, err
		}, emitenID)
}
