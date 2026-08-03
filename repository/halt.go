package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/platform/postgres"
)

// Halt records and clears trading halts. It satisfies the persistence half of
// the circuit breaker: the registry holds the live state, this holds the copy
// that survives a restart.
type Halt struct {
	db
}

// NewHalt returns a halt repository backed by pool.
func NewHalt(pool *pgxpool.Pool) *Halt {
	return &Halt{db{pool: pool}}
}

// ActiveHalt is a halt still in force, as read back at startup.
type ActiveHalt struct {
	EmitenID  int64
	ResumesAt time.Time
}

// SaveHalt records a halt, replacing any existing one for the same emiten.
//
// An upsert rather than an insert: an instrument that halts, resumes, and halts
// again within a session must not fail on the primary key. The row is the
// current halt, not a log of past ones — the audit trail belongs in its own
// table if it is ever wanted, and conflating the two would make "is this halted
// right now" a query over history rather than a primary-key lookup.
func (r *Halt) SaveHalt(ctx context.Context, emitenID, price, reference int64, until time.Time) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO trading_halt (
			emiten_id, halted_at, resumes_at, trigger_price, reference_price)
		VALUES ($1, now(), $2, $3, $4)
		ON CONFLICT (emiten_id) DO UPDATE
		   SET halted_at       = now(),
		       resumes_at      = EXCLUDED.resumes_at,
		       trigger_price   = EXCLUDED.trigger_price,
		       reference_price = EXCLUDED.reference_price`,
		emitenID, until, price, reference)
	if err != nil {
		return fmt.Errorf("repository: save halt for emiten %d: %w", emitenID, err)
	}
	return nil
}

// ClearHalt removes an emiten's halt. Clearing one that does not exist is not an
// error: halts expire on a timer that may run twice over the same deadline, and
// the second pass finding nothing to do is ordinary.
func (r *Halt) ClearHalt(ctx context.Context, emitenID int64) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM trading_halt WHERE emiten_id = $1`, emitenID)
	if err != nil {
		return fmt.Errorf("repository: clear halt for emiten %d: %w", emitenID, err)
	}
	return nil
}

// LoadActiveHalts returns every halt that has not yet expired, for restoring
// live state at startup.
//
// Filtered by resumes_at rather than loaded wholesale, so a halt that expired
// while the process was down is not reinstated — it would otherwise close an
// instrument that should have reopened minutes or hours ago. The expired rows
// are left for PurgeExpiredHalts rather than deleted here, keeping this a read.
func (r *Halt) LoadActiveHalts(ctx context.Context) ([]ActiveHalt, error) {
	return postgres.QueryAll(ctx, r.pool, "trading_halt",
		`SELECT emiten_id, resumes_at
		   FROM trading_halt
		  WHERE resumes_at > now()
		  ORDER BY emiten_id`,
		func(rows pgx.Rows) (ActiveHalt, error) {
			var h ActiveHalt
			err := rows.Scan(&h.EmitenID, &h.ResumesAt)
			return h, err
		})
}

// PurgeExpiredHalts deletes halts whose deadline has passed, and reports how
// many rows it removed.
//
// Run once at startup to clear halts that expired while the process was down.
// Without it those rows accumulate, and every later LoadActiveHalts pays to
// filter past them.
func (r *Halt) PurgeExpiredHalts(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM trading_halt WHERE resumes_at <= now()`)
	if err != nil {
		return 0, fmt.Errorf("repository: purge expired halts: %w", err)
	}
	return tag.RowsAffected(), nil
}

// SetReferencePrice updates the session anchor an emiten's price band is
// measured from.
func (r *Halt) SetReferencePrice(ctx context.Context, emitenID, price int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE emiten SET reference_price = $2 WHERE id = $1`, emitenID, price)
	if err != nil {
		return fmt.Errorf("repository: set reference price for emiten %d: %w", emitenID, err)
	}
	return nil
}
