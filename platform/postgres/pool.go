// Package postgres owns the PostgreSQL connection pool and the small helpers
// shared by every repository. It contains no domain knowledge: each domain
// package declares the repository interface it needs and implements it against
// the pool handed to it here.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Verifies the pool with a ping so it fails fast: a bad or unreachable DSN
// returns an error at startup rather than later.
func New(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}
	return pool, nil
}

// Exists because the row-collecting loop (Query, defer Close, for Next, Scan,
// append, check rows.Err) was previously copy-pasted per table with only the
// scan target differing.
func QueryAll[T any](ctx context.Context, pool *pgxpool.Pool, what, sql string, scan func(pgx.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: query %s: %w", what, err)
	}
	defer rows.Close()

	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: scan %s: %w", what, err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterate %s: %w", what, err)
	}
	return out, nil
}
