package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/postgres"
)

// LoadEmiten returns every listed instrument, ordered by code.
func (r *Master) LoadEmiten(ctx context.Context) ([]market.Emiten, error) {
	return postgres.QueryAll(ctx, r.pool, "emiten",
		`SELECT id, kode, nama, listed_shares FROM emiten ORDER BY kode`,
		func(rows pgx.Rows) (market.Emiten, error) {
			var e market.Emiten
			err := rows.Scan(&e.ID, &e.Kode, &e.Nama, &e.ListedShares)
			return e, err
		})
}
