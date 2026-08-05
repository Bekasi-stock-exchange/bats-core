package repository

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"bekasi-automatic-trading-system/market"
)

// Master reads master data (emiten, participant). It satisfies
// market.MasterRepository; the queries themselves live in emiten.go and
// participant.go.
type Master struct {
	db
}

func NewMaster(pool *pgxpool.Pool) *Master {
	return &Master{db{pool: pool}}
}

// compile-time check that Master satisfies the interface market declares.
var _ market.MasterRepository = (*Master)(nil)
