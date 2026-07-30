// Command migrate applies every .sql file in migrations/ in filename order
// against DB_DSN. It is a plain runner (no versioning table) intended for
// local setup:
//
//	go run ./cmd/migrate
//
// Seed files use ON CONFLICT DO NOTHING so re-running them is safe; the DDL
// files are not idempotent, so run this once on a fresh database.
//
// The migrations/ path is resolved relative to the working directory, which is
// the repo root under `go run ./cmd/migrate`. Pass a directory argument to
// override: `go run ./cmd/migrate ./migrations`.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jackc/pgx/v5"

	"bekasi-automatic-trading-system/platform/config"
)

func main() {
	dir := ""
	if len(os.Args) > 1 {
		dir = os.Args[1] // explicit override
	}
	if dir == "" {
		// Locate migrations/ at the repo root regardless of the working
		// directory, so `go run ./cmd/migrate` works from anywhere.
		if found := config.FindUpwards("migrations"); found != "" {
			dir = found
		} else {
			dir = "migrations"
		}
	}
	if err := run(dir); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no migration files found in %s", dir)
	}
	sort.Strings(files)

	ctx := context.Background()
	conn, err := pgx.Connect(ctx, cfg.DBDSN)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer conn.Close(ctx)

	for _, f := range files {
		sql, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("read %s: %w", f, err)
		}
		if _, err := conn.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("exec %s: %w", f, err)
		}
		fmt.Println("applied", f)
	}
	return nil
}
