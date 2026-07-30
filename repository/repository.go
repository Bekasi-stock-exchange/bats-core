// Package repository is the single home for SQL. Every query in the application
// lives in this package, one file per feature: emiten.go, participant.go,
// order.go, trade.go.
//
// No other package contains SQL, and this package contains nothing but data
// access — no validation, no matching, no HTTP. Domain packages declare the
// repository interface they need (market.MasterRepository, order.Repository) and
// the types here satisfy them, so the dependency runs repository -> domain and
// never the other way.
package repository

import "github.com/jackc/pgx/v5/pgxpool"

// db is the connection pool shared by every repository type in this package.
type db struct {
	pool *pgxpool.Pool
}
