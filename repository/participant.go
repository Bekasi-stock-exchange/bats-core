package repository

import (
	"context"

	"github.com/jackc/pgx/v5"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/platform/postgres"
)

// LoadParticipant returns every broker (exchange participant), ordered by code.
func (r *Master) LoadParticipant(ctx context.Context) ([]market.Participant, error) {
	return postgres.QueryAll(ctx, r.pool, "participant",
		`SELECT id, kode, nama FROM participant ORDER BY kode`,
		func(rows pgx.Rows) (market.Participant, error) {
			var p market.Participant
			err := rows.Scan(&p.ID, &p.Kode, &p.Nama)
			return p, err
		})
}
