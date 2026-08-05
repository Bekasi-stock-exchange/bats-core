package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/postgres"
)

func (r *Master) LoadEmiten(ctx context.Context) ([]market.Emiten, error) {
	return postgres.QueryAll(ctx, r.pool, "emiten",
		`SELECT id, kode, nama, listed_shares, unlisted_shares, is_active,
		        ipo_price, reference_price
		 FROM emiten ORDER BY kode`,
		func(rows pgx.Rows) (market.Emiten, error) {
			var e market.Emiten
			err := rows.Scan(&e.ID, &e.Kode, &e.Nama, &e.ListedShares, &e.UnlistedShares,
				&e.IsActive, &e.IPOPrice, &e.SessionReference)
			return e, err
		})
}

// Returns the assigned id so the caller can register it in the live directory
// and registry.
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

// Opens a dormant instrument for trading at its offering price.
//
// is_active and ipo_price move together because they are one event: an instrument
// becomes tradeable *at* a price, and a row that is active with no price would
// have nothing to quote against.
//
// The WHERE clause carries `NOT is_active`, so this is the database's own guard
// against a second offering over a live instrument — the service checks the same
// thing against the directory, but only this check sees concurrent requests. An
// affected-row count of zero therefore means "already active", not "missing": the
// caller resolved the id from the directory before getting here.
//
// reference_price is seeded from the same offering price, because on an
// instrument's first trading day the price band is measured from what it was
// sold at. It is set here rather than left to the first session roll so the band
// applies from the opening bell — otherwise a freshly listed instrument would
// trade its entire first day with no limits at all, which is the day it is least
// able to absorb a mispriced order.
func (r *Master) ActivateEmiten(ctx context.Context, emitenID, ipoPrice int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE emiten
		SET is_active = true, ipo_price = $2, reference_price = $2
		WHERE id = $1 AND NOT is_active`,
		emitenID, ipoPrice)
	if err != nil {
		return fmt.Errorf("repository: activate emiten %d: %w", emitenID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("repository: activate emiten %d: %w", emitenID, ErrAlreadyActive)
	}
	return nil
}

// ErrAlreadyActive reports an activation of an instrument that is already
// trading, so the caller can answer 400 rather than 500.
var ErrAlreadyActive = errors.New("repository: emiten is already active")

// ErrDuplicate reports a unique-constraint violation, so callers can map it to a
// 409 without importing pgx error codes.
var ErrDuplicate = errors.New("repository: already exists")

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
