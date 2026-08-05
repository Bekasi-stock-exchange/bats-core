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
//	@description
//	@description	There are two authentication tiers, and they never mix:
//	@description
//	@description	- **Participant** (`/api/participant/*`, `/ws/participant/*`) — a broker
//	@description	authenticates with its own key, stored hashed in the database and sent as
//	@description	`X-Participant-Key`. Keys are issued and revoked per broker at runtime.
//	@description	- **Admin** (`/api/admin/*`, `/ws/admin/*`) — the single static key from
//	@description	configuration, sent as `X-API-Key`.
//	@description
//	@description	Orders enter only through POST /api/participant/orders. The order book can
//	@description	be polled or streamed over WebSocket (documented, but not exercisable from
//	@description	Swagger UI, since browsers cannot set headers on a WS handshake).
//
//	@servers.url			http://localhost:8080
//	@servers.description	Local development
//
//	@securityDefinitions.apikey	ApiKeyAuth
//	@in							header
//	@name						X-API-Key
//	@securityDefinitions.apikey	ParticipantKeyAuth
//	@in							header
//	@name						X-Participant-Key
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"bekasi-automatic-trading-system/assets"
	"bekasi-automatic-trading-system/breaker"
	"bekasi-automatic-trading-system/corporateaction"
	"bekasi-automatic-trading-system/emiten"
	"bekasi-automatic-trading-system/engine"
	"bekasi-automatic-trading-system/index"
	"bekasi-automatic-trading-system/market"
	"bekasi-automatic-trading-system/marketconfig"
	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/orderbook"
	"bekasi-automatic-trading-system/participant"
	"bekasi-automatic-trading-system/platform/config"
	"bekasi-automatic-trading-system/platform/docs"
	"bekasi-automatic-trading-system/platform/postgres"
	"bekasi-automatic-trading-system/platform/server"
	"bekasi-automatic-trading-system/repository"
	"bekasi-automatic-trading-system/trade"
	"bekasi-automatic-trading-system/underwriter"
	"bekasi-automatic-trading-system/wallet"
)

// shutdownTimeout bounds the drain of in-flight requests on SIGINT/SIGTERM. Past
// it, remaining connections are dropped so the process cannot hang forever — long
// enough for a matching pass and its transaction to finish, short enough to stay
// inside a typical container stop grace period.
const shutdownTimeout = 10 * time.Second

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

	// Cancelled on the first SIGINT/SIGTERM, which is what starts the drain below.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := postgres.New(ctx, cfg.DBDSN)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Repositories: the only things that talk SQL. The master and trade stores
	// each satisfy more than one domain's interface, which is why they appear
	// twice below rather than being constructed twice.
	master := repository.NewMaster(pool)
	trades := repository.NewTrade(pool)
	indexes := repository.NewIndex(pool)

	repos := repositories{
		master:       master,
		order:        repository.NewOrder(pool),
		asset:        repository.NewAsset(pool),
		wallet:       repository.NewWallet(pool),
		participant:  repository.NewParticipant(pool),
		trade:        trades,
		underwriter:  repository.NewUnderwriter(pool),
		corporate:    repository.NewCorporateAction(pool),
		config:       repository.NewMarketConfig(pool),
		halt:         repository.NewHalt(pool),
		index:        indexes,
		indexPrices:  indexes,
		emitenWriter: master,
		prices:       trades,
	}

	// Market kernel, seeded from the database.
	kernel, err := newKernel(ctx, repos)
	if err != nil {
		return err
	}

	// Trading parameters, loaded before anything can enforce them. The cache is
	// what the order path reads on every submission, so it is filled from the
	// database here rather than lazily: a first order arriving before the load
	// would otherwise be validated against the built-in default instead of the
	// operator's configuration.
	configCache := marketconfig.NewCache()
	configSvc := marketconfig.NewService(repos.config, configCache)
	if err := configSvc.Load(ctx); err != nil {
		return err
	}

	// The circuit breaker. The supervisor records a halt when one trips and ends
	// it when its time is up; the registry enforces both the price band and the
	// halt itself, reading the live configuration through the cache.
	//
	// Wired after the config load, because a breaker reading the built-in default
	// instead of the operator's threshold would halt on the wrong move.
	haltSupervisor := breaker.NewSupervisor(kernel.reg, repos.halt, kernel.hub, slog.Default())
	kernel.reg.WithBreaker(configCache, haltSupervisor)

	// Halts that were still running when the process last stopped. Without this a
	// restart reopens a halted instrument early — at exactly the moment an
	// operator is least likely to be watching. Rows whose deadline has already
	// passed are dropped rather than restored, so a halt never outlives its
	// duration just because the process was down for it.
	if _, err := repos.halt.PurgeExpiredHalts(ctx); err != nil {
		return err
	}
	activeHalts, err := repos.halt.LoadActiveHalts(ctx)
	if err != nil {
		return err
	}
	for _, h := range activeHalts {
		kernel.reg.HaltUntil(h.EmitenID, h.ResumesAt)
		slog.Info("restored trading halt", "emiten_id", h.EmitenID, "resumes_at", h.ResumesAt)
	}

	// The sweep that ends halts. WithoutCancel so a shutdown signal does not stop
	// it before the drain finishes — matching how the index notifier is run.
	go haltSupervisor.Run(context.WithoutCancel(ctx))

	// Services.
	orderSvc := order.NewService(kernel.dir, kernel.reg, kernel.hub, repos.order, configCache)
	bookSvc := orderbook.NewService(kernel.dir, kernel.reg, kernel.hub)
	partSvc := participant.NewService(repos.participant, kernel.dir)
	emitenSvc := emiten.NewService(kernel.dir, kernel.reg, repos.emitenWriter, repos.prices)
	assetSvc := assets.NewService(kernel.dir, repos.asset)
	walletSvc := wallet.NewService(kernel.dir, kernel.reg, repos.wallet)
	tradeSvc := trade.NewService(kernel.dir, repos.trade)
	// The underwriter domain drives emitenSvc for the listing half of an IPO, so it
	// is constructed after it.
	underwriterSvc := underwriter.NewService(repos.underwriter, emitenSvc, kernel.dir, kernel.reg)
	// Corporate actions drive emitenSvc too, for the share-count restatement a
	// split or bonus performs, so they are constructed after it for the same
	// reason.
	corporateSvc := corporateaction.NewService(repos.corporate, emitenSvc, kernel.dir, kernel.reg)

	// The composite index, loaded before the server accepts a request so the first
	// read reports a real level rather than a 503. Load also bootstraps the divisor
	// on a fresh database, where migration 014 could only seed a placeholder.
	indexCache := index.NewCache()
	indexSvc := index.NewService(kernel.dir, repos.index, repos.indexPrices, indexCache)
	if err := indexSvc.Load(ctx); err != nil {
		return err
	}

	// Recomputation runs on its own goroutine, not on the order path: valuing the
	// whole market inside the submit lock would add that cost to every order's
	// latency. The notifier is what connects the two without coupling them.
	indexNotifier := index.NewNotifier(indexSvc)
	orderSvc.Observe(indexNotifier)
	go indexNotifier.Run(context.WithoutCancel(ctx))
	defer indexNotifier.Close()

	// A new listing adds its whole market cap at once. The index service restates
	// its divisor to absorb that, so the level does not jump on an event where no
	// price moved. Registered on the emiten service, which both the admin listing
	// endpoint and the IPO path go through.
	emitenSvc.ObserveListings(indexSvc)

	handler := server.Handler(server.Deps{
		APIKey:      cfg.APIKey,
		DisableDocs: cfg.DisableDocs,

		// Brokers authenticate against the database so keys can be issued and
		// revoked at runtime; admin uses the single static key from config.
		ParticipantAuth: participant.RequireKey(partSvc),

		Order:       order.NewController(orderSvc),
		OrderBook:   orderbook.NewController(bookSvc),
		WS:          orderbook.NewWSController(bookSvc),
		Participant: participant.NewController(partSvc, repository.IsDuplicate),
		Emiten:      emiten.NewController(emitenSvc, repository.IsDuplicate),
		Index:       index.NewController(indexSvc),
		Underwriter: underwriter.NewController(underwriterSvc, underwriter.NewCodes(kernel.dir), repository.IsDuplicate),
		Corporate:   corporateaction.NewController(corporateSvc, corporateaction.NewCodes(kernel.dir)),
		Config:      marketconfig.NewController(configSvc),
		Assets:      assets.NewController(assetSvc, assets.NewCodes(kernel.dir)),
		Wallet:      wallet.NewController(walletSvc, wallet.NewCodes(kernel.dir)),
		Trade:       trade.NewController(tradeSvc, trade.NewCodes(kernel.dir)),
		Docs:        docs.NewController(),
	})

	return serve(ctx, ":"+strconv.Itoa(cfg.HTTPPort), handler)
}

// Runs until ctx is cancelled, then drains in-flight requests before returning.
//
// The listener runs on its own goroutine so this one can wait on both outcomes: a
// startup failure (port already taken) surfaces immediately via errc, while a
// signal falls through to Shutdown. Without that split, ListenAndServe would block
// past every deferred cleanup and the pool would never close.
func serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: handler}

	errc := make(chan error, 1)
	go func() {
		slog.Info("starting JAST core", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
			return
		}
		errc <- nil
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down", "timeout", shutdownTimeout)

	// A fresh context: ctx is already cancelled, so it cannot bound the drain.
	drainCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(drainCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// repositories groups the SQL-backed stores. They are passed as the interfaces the
// domains declare, not as concrete types, so this is the only place that knows a
// database is involved at all.
type repositories struct {
	master      market.MasterRepository
	order       order.Repository
	asset       assets.Repository
	wallet      wallet.Repository
	participant participant.Repository
	trade       trade.Repository
	underwriter underwriter.Repository
	corporate   corporateaction.Repository
	config      marketconfig.Repository

	// halt persists trading halts and the per-emiten reference price the band is
	// measured from, so both survive a restart. Concrete rather than an interface
	// because startup reads it directly (restoring live halts) as well as handing
	// it to the breaker supervisor as a breaker.Store.
	halt *repository.Halt

	// index and indexPrices are both satisfied by the index repository: one owns
	// the stored definition and its history, the other the market-wide price read
	// the computation needs. They are separate interfaces because the second is a
	// read over the trades table, and keeping it distinct is what lets the index
	// domain state that dependency without importing the trade domain.
	index       index.Repository
	indexPrices index.PriceRepository

	// emitenWriter and prices are narrower views the emiten domain declares:
	// one to list an instrument, one to read its price statistics. They are
	// satisfied by the master and trade repositories respectively.
	emitenWriter emiten.Repository
	prices       emiten.PriceStatsRepository
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
// Holdings and wallets seed the share and cash ledgers, which is what lets the
// registry reject a broker selling or buying more than it has. Orders still open
// at the last shutdown are then replayed into the book, so their reservations
// are re-committed against those ledgers too — without this, a restart would
// forget what every resting order still promises away, and a broker could
// oversell or overbuy past it until a database constraint caught the drift.
func newKernel(ctx context.Context, repos repositories) (*kernel, error) {
	emitens, err := repos.master.LoadEmiten(ctx)
	if err != nil {
		return nil, err
	}
	participants, err := repos.master.LoadParticipant(ctx)
	if err != nil {
		return nil, err
	}
	holdings, err := repos.asset.LoadHoldings(ctx)
	if err != nil {
		return nil, err
	}
	wallets, err := repos.wallet.LoadWallets(ctx)
	if err != nil {
		return nil, err
	}
	maxOrderSeq, maxTradeSeq, err := repos.order.MaxSeqs(ctx)
	if err != nil {
		return nil, err
	}
	openOrders, err := repos.order.LoadOpenOrders(ctx)
	if err != nil {
		return nil, err
	}

	seq := engine.NewSequencer(maxOrderSeq, maxTradeSeq)
	reg := market.NewRegistry(emitens, holdings, wallets, seq)
	reg.RestoreOpenOrders(openOrders)

	return &kernel{
		dir: market.NewDirectory(emitens, participants),
		reg: reg,
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
