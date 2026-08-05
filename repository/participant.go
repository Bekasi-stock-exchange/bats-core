package repository

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/participant"
	"bekasi-automatic-trading-system/platform/postgres"
)

func (r *Master) LoadParticipant(ctx context.Context) ([]market.Participant, error) {
	return postgres.QueryAll(ctx, r.pool, "participant",
		`SELECT id, kode, nama FROM participant ORDER BY kode`,
		func(rows pgx.Rows) (market.Participant, error) {
			var p market.Participant
			err := rows.Scan(&p.ID, &p.Kode, &p.Nama)
			return p, err
		})
}

// Participant persists brokers and their API key hashes. It satisfies
// participant.Repository.
type Participant struct {
	db
}

func NewParticipant(pool *pgxpool.Pool) *Participant {
	return &Participant{db{pool: pool}}
}

var _ participant.Repository = (*Participant)(nil)

// participantColumns is the projection every read below shares. The plaintext key
// is absent because it is never stored.
const participantColumns = `id, kode, nama, api_key_prefix, api_key_issued_at`

func scanParticipant(rows pgx.Rows) (participant.Record, error) {
	var rec participant.Record
	err := rows.Scan(&rec.ID, &rec.Kode, &rec.Nama, &rec.APIKeyPrefix, &rec.APIKeyIssuedAt)
	return rec, err
}

// A duplicate kode becomes ErrDuplicate so the caller can answer 409 rather than
// 500.
func (r *Participant) Create(ctx context.Context, kode, nama string) (participant.Record, error) {
	rec := participant.Record{Kode: kode, Nama: nama}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO participant (kode, nama) VALUES ($1, $2) RETURNING id`,
		kode, nama).Scan(&rec.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return participant.Record{}, fmt.Errorf("%w: participant %s", ErrDuplicate, kode)
		}
		return participant.Record{}, fmt.Errorf("repository: insert participant %s: %w", kode, err)
	}
	return rec, nil
}

// This runs on every authenticated participant request, which is why
// idx_participant_api_key_hash exists: it makes the lookup an index hit rather
// than a scan, and lets revocation take effect immediately without a cache.
func (r *Participant) FindByAPIKeyHash(ctx context.Context, hash string) (participant.Record, error) {
	row, err := r.pool.Query(ctx,
		`SELECT `+participantColumns+` FROM participant WHERE api_key_hash = $1`, hash)
	if err != nil {
		return participant.Record{}, fmt.Errorf("repository: find participant by key: %w", err)
	}
	defer row.Close()

	if !row.Next() {
		return participant.Record{}, participant.ErrNotFound
	}
	return scanParticipant(row)
}

func (r *Participant) FindByKode(ctx context.Context, kode string) (participant.Record, error) {
	row, err := r.pool.Query(ctx,
		`SELECT `+participantColumns+` FROM participant WHERE kode = $1`, kode)
	if err != nil {
		return participant.Record{}, fmt.Errorf("repository: find participant %s: %w", kode, err)
	}
	defer row.Close()

	if !row.Next() {
		return participant.Record{}, participant.ErrNotFound
	}
	return scanParticipant(row)
}

func (r *Participant) List(ctx context.Context) ([]participant.Record, error) {
	return postgres.QueryAll(ctx, r.pool, "participant list",
		`SELECT `+participantColumns+` FROM participant ORDER BY kode`, scanParticipant)
}

// Replaces any previous key.
func (r *Participant) SetAPIKey(ctx context.Context, kode, hash, prefix string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE participant
		SET api_key_hash = $1, api_key_prefix = $2, api_key_issued_at = now()
		WHERE kode = $3`, hash, prefix, kode)
	if err != nil {
		return fmt.Errorf("repository: set api key for %s: %w", kode, err)
	}
	if tag.RowsAffected() == 0 {
		return participant.ErrNotFound
	}
	return nil
}

func (r *Participant) ClearAPIKey(ctx context.Context, kode string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE participant
		SET api_key_hash = NULL, api_key_prefix = NULL, api_key_issued_at = NULL
		WHERE kode = $1`, kode)
	if err != nil {
		return fmt.Errorf("repository: clear api key for %s: %w", kode, err)
	}
	if tag.RowsAffected() == 0 {
		return participant.ErrNotFound
	}
	return nil
}
