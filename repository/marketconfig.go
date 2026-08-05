package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/marketconfig"
)

// MarketConfig reads and writes the exchange's trading parameters. It satisfies
// marketconfig.Repository.
type MarketConfig struct {
	db
}

func NewMarketConfig(pool *pgxpool.Pool) *MarketConfig {
	return &MarketConfig{db{pool: pool}}
}

var _ marketconfig.Repository = (*MarketConfig)(nil)

// The row is seeded by migration 013 and pinned to id = 1 by a CHECK, so a
// missing row means the migration has not run — a deployment fault, reported as
// an error rather than papered over with defaults. Silently substituting them
// would start the exchange enforcing a rule that no operator chose and that
// nothing records.
func (r *MarketConfig) LoadSettings(ctx context.Context) (marketconfig.Settings, error) {
	var (
		s        marketconfig.Settings
		haltSecs int64
	)
	err := r.pool.QueryRow(ctx, `
		SELECT min_price, emiten_halt_bps, index_halt_bps, halt_duration_seconds
		  FROM market_config
		 WHERE id = 1`,
	).Scan(&s.MinPrice, &s.EmitenHaltBPS, &s.IndexHaltBPS, &haltSecs)
	if err != nil {
		return marketconfig.Settings{}, fmt.Errorf("repository: load market config: %w", err)
	}
	s.HaltDuration = time.Duration(haltSecs) * time.Second
	return s, nil
}

// An upsert rather than an update: it makes the write independent of whether the
// seed row exists, so the endpoint behaves the same on a database that has been
// migrated from scratch and one where the row was removed by hand. RETURNING
// gives the caller the database's own view of what landed, which is what the
// cache is then set from.
func (r *MarketConfig) SaveSettings(ctx context.Context, s marketconfig.Settings) (marketconfig.Settings, error) {
	var (
		out      marketconfig.Settings
		haltSecs int64
	)
	err := r.pool.QueryRow(ctx, `
		INSERT INTO market_config (
			id, min_price, emiten_halt_bps, index_halt_bps,
			halt_duration_seconds, updated_at)
		VALUES (1, $1, $2, $3, $4, now())
		ON CONFLICT (id) DO UPDATE
		   SET min_price             = EXCLUDED.min_price,
		       emiten_halt_bps       = EXCLUDED.emiten_halt_bps,
		       index_halt_bps        = EXCLUDED.index_halt_bps,
		       halt_duration_seconds = EXCLUDED.halt_duration_seconds,
		       updated_at            = now()
		RETURNING min_price, emiten_halt_bps, index_halt_bps, halt_duration_seconds`,
		s.MinPrice, s.EmitenHaltBPS, s.IndexHaltBPS, int64(s.HaltDuration/time.Second),
	).Scan(&out.MinPrice, &out.EmitenHaltBPS, &out.IndexHaltBPS, &haltSecs)
	if err != nil {
		return marketconfig.Settings{}, fmt.Errorf("repository: save market config: %w", err)
	}
	out.HaltDuration = time.Duration(haltSecs) * time.Second
	return out, nil
}
