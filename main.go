// Command jast-core is the JAST matching engine server.
//
// This file is the composition root: it is the only place that knows how the
// layers fit together. Repositories are built on the pool, the market kernel is
// seeded from the database, services are wired to both, controllers to services,
// and the route table to the controllers. Nothing below this file constructs its
// own dependencies.
//
// The OpenAPI document served at /openapi.yaml is generated from the annotations
// below and on the controllers — run `make docs` after changing either.
//
//	@title			JAST Core API
//	@version		1.0.0
//	@description	REST surface for the JAST core matching engine (JATS-style continuous
//	@description	matching for Bursa Efek Indonesia). The engine knows only brokers
//	@description	(participants), not individual investors, and stops at trade execution.
//	@description
//	@description	Orders enter only through POST /api/orders. The order book can be polled
//	@description	via GET /api/orderbook/{kode} or streamed over WebSocket at
//	@description	GET /ws/orderbook/{kode} (documented, but not exercisable from Swagger UI).
//
//	@servers.url			http://localhost:8080
//	@servers.description	Local development
//
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						X-API-Key
//	@description				Shared API key. Every route except the documentation requires it.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"

	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/market/engine"
	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/orderbook"
	"bekasi-automatic-trading-system/platform/config"
	"bekasi-automatic-trading-system/platform/docs"
	"bekasi-automatic-trading-system/platform/postgres"
	"bekasi-automatic-trading-system/platform/server"
	"bekasi-automatic-trading-system/repository"
)

func main() {
	if err := runServer(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func runServer() error {
	cfg, err := config.Load()
	if err != nil {
		return err // fails fast when DB_DSN or API_KEY is unset
	}

	setupLogger(cfg.LogLevel)

	ctx := context.Background()
	pool, err := postgres.New(ctx, cfg.DBDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Repositories: the only things that talk SQL.
	masterRepo := repository.NewMaster(pool)
	orderRepo := repository.NewOrder(pool)

	// Market kernel, seeded from the database.
	kernel, err := newKernel(ctx, masterRepo, orderRepo)
	if err != nil {
		return err
	}

	// Services, then controllers.
	orderSvc := order.NewService(kernel.dir, kernel.reg, kernel.hub, orderRepo)
	bookSvc := orderbook.NewService(kernel.dir, kernel.reg, kernel.hub)

	handler := server.Handler(server.Deps{
		APIKey:      cfg.APIKey,
		DisableDocs: cfg.DisableDocs,
		Order:       order.NewController(orderSvc),
		OrderBook:   orderbook.NewController(bookSvc),
		WS:          orderbook.NewWSController(bookSvc),
		Docs:        docs.NewController(),
	})

	addr := ":" + strconv.Itoa(cfg.HTTPPort)
	slog.Info("starting JAST core", "addr", addr)
	return http.ListenAndServe(addr, handler)
}

// kernel is the shared market state both domains read and write.
type kernel struct {
	dir *market.Directory
	reg *market.Registry
	hub *market.Hub
}

// newKernel loads master data and builds one engine per emiten.
//
// The sequencer is seeded from the highest sequence numbers already persisted, so
// order and trade Seq continue past them and stay globally unique across restarts.
func newKernel(ctx context.Context, masterRepo market.MasterRepository, orderRepo order.Repository) (*kernel, error) {
	emitens, err := masterRepo.LoadEmiten(ctx)
	if err != nil {
		return nil, err
	}
	participants, err := masterRepo.LoadParticipant(ctx)
	if err != nil {
		return nil, err
	}
	maxOrderSeq, maxTradeSeq, err := orderRepo.MaxSeqs(ctx)
	if err != nil {
		return nil, err
	}

	seq := engine.NewSequencer(maxOrderSeq, maxTradeSeq)
	return &kernel{
		dir: market.NewDirectory(emitens, participants),
		reg: market.NewRegistry(emitens, seq),
		hub: market.NewHub(),
	}, nil
}

func setupLogger(level string) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}
