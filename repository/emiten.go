package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/postgres"
)

// LoadEmiten returns every listed instrument, ordered by code.
func (r *Master) LoadEmiten(ctx context.Context) ([]market.Emiten, error) {
	return postgres.QueryAll(ctx, r.pool, "emiten",
		`SELECT id, kode, nama, listed_shares, unlisted_shares, is_active, ipo_price
		 FROM emiten ORDER BY kode`,
		func(rows pgx.Rows) (market.Emiten, error) {
			var e market.Emiten
			err := rows.Scan(&e.ID, &e.Kode, &e.Nama, &e.ListedShares, &e.UnlistedShares,
				&e.IsActive, &e.IPOPrice)
			return e, err
		})
}

// CreateEmiten inserts a new listed instrument and returns it with its assigned
// id, so the caller can register it in the live directory and registry.
//
// A duplicate kode surfaces as ErrDuplicate rather than a raw driver error, so
// the controller can answer 409 instead of 500.
func (r *Master) CreateEmiten(ctx context.Context, e market.Emiten) (market.Emiten, error) {
	err := r.pool.QueryRow(ctx, `
		INSERT INTO emiten (kode, nama, listed_shares, unlisted_shares, is_active, ipo_price)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id`,
		e.Kode, e.Nama, e.ListedShares, e.UnlistedShares, e.IsActive, e.IPOPrice,
	).Scan(&e.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return market.Emiten{}, fmt.Errorf("%w: emiten %s", ErrDuplicate, e.Kode)
		}
		return market.Emiten{}, fmt.Errorf("repository: insert emiten %s: %w", e.Kode, err)
	}
	return e, nil
}

// ErrDuplicate reports a unique-constraint violation, so callers can map it to a
// 409 without importing pgx error codes.
var ErrDuplicate = errors.New("repository: already exists")

// IsDuplicate reports whether err is a unique-constraint violation.
//
// Exported so controllers can answer 409 without importing pgx or knowing what a
// SQLSTATE is: they receive this as a plain func(error) bool.
func IsDuplicate(err error) bool {
	if errors.Is(err, ErrDuplicate) {
		return true
	}
	return isUniqueViolation(err)
}

// isUniqueViolation reports whether err is a Postgres 23505.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
