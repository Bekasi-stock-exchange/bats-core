# JAST Core

A minimal **matching engine** modeled on JATS (Jakarta Automated Trading System) of
Bursa Efek Indonesia. It performs continuous, price-time-priority matching of buy and
sell orders in memory, and persists master data and trade history to PostgreSQL.

This is an **exchange engine**, not a brokerage application. It knows only brokers
(*participants*), not individual investors, and its responsibility ends at **trade
execution** — clearing (KPEI) and settlement (KSEI) are out of scope.

```
Order → [ JATS: matching ] → Trade → [ KPEI: clearing ] → [ KSEI: settlement ]
         ^^^^^^^^^^^^^^^^^^^^^^^^^
         this project
```

## Features

- Continuous matching — orders are matched the moment they arrive
- Order types: **limit** and **market**
- **Price-time priority** (FIFO within a price level)
- Partial fills
- In-memory order book, one per emiten
- PostgreSQL persistence for master data and trade history
- REST API to submit orders and view the book
- WebSocket streaming of order-book updates
- Two-tier auth: a static admin key, and per-broker keys stored hashed in the database
- Share holdings per broker, written atomically with the match that moves them
- Price history (raw executions and OHLC candles) and instrument detail
- OpenAPI 3.0 documentation generated from the code and served from the binary

**Deliberately out of scope:** call auction / pre-opening, other order types (stop-loss,
iceberg, FOK, GTD), customer accounts / balances / portfolios, clearing & settlement,
corporate actions, index free-float adjustment, microservices, message brokers,
and any frontend.

## Architecture

A modular monolith organised **by domain**, not by layer. Each domain is one package
containing its own controller, service and transformer:

| Layer | Owns | Never does |
|---|---|---|
| **Controller** | Decode the request, call the service, write the transformed result | Business rules, SQL |
| **Service** | Validation, orchestration, transaction boundary | HTTP, SQL strings |
| **Transformer** | Domain/engine types → JSON DTOs | Anything else |
| **Repository** | Every SQL statement in the application | Validation, matching |

Dependencies run one way:

```
order       →  engine, market, platform
orderbook   →  market, platform
market      →  engine
repository  →  order, market        (implements the interfaces THEY declare)
main        →  everything           (composition root)

engine imports nothing outside itself
order and orderbook never import each other
```

`engine` sits at the root rather than under `market` because `engine.Order` and
`engine.Trade` are domain types the order package speaks directly — it is a shared kernel
in its own right, not a private detail of `market`.

Two rules make that hold:

- **`engine` is pure.** No HTTP library, no database driver, no `encoding/json`, and
  its types carry no struct tags — so the matching logic stays extractable into a
  standalone service.
- **Interfaces are declared by the consumer.** `order.Repository` and
  `market.MasterRepository` are defined in the packages that use them, and `repository`
  imports those packages to satisfy them. So no domain package depends on a database type.

`market` exists so the two domains never touch each other: it owns the order books, the
single mutex that serializes matching, the master-data directory, and the WebSocket
fan-out hub. The hub carries tag-free `market.BookState`, which is why the order service
can publish a book update without importing the orderbook package's DTOs.

`viper` is confined to `platform/config`; every other package receives plain values.

### Layout

```
engine/            order.go  orderbook.go  matching.go  engine_test.go  # pure matching core
market/            registry.go  directory.go  positions.go  book.go
                   hub.go  ports.go                       # books, THE lock, share ledger, master data
order/             controller.go  service.go  transformer.go  dto.go  ports.go
orderbook/         controller.go  ws_controller.go  service.go  transformer.go  dto.go
participant/       controller.go  service.go  middleware.go  context.go
                   transformer.go  dto.go  ports.go       # broker identity + key auth
emiten/            controller.go  service.go  transformer.go  dto.go  ports.go
assets/            controller.go  service.go  transformer.go  dto.go  ports.go
trade/             controller.go  service.go  transformer.go  dto.go  ports.go
repository/        repository.go  master.go  emiten.go  participant.go
                   order.go  trade.go  asset.go                         # ALL SQL lives here
platform/config/   config.go                                            # viper env management
platform/postgres/ pool.go                                              # pgx pool + QueryAll helper
platform/httpx/    respond.go  pagination.go  middleware.go             # JSON, paging, auth
platform/docs/     handler.go  swagger.yaml  swagger.json               # Swagger UI + generated spec
platform/server/   router.go                                            # the route table
cmd/migrate/       main.go                                              # migration runner
cmd/gendocs/       main.go                                              # OpenAPI generation
migrations/        001_emiten.sql .. 007_auth_assets_unlisted.sql       # schema + seed
main.go                                                                 # composition root
```

**All SQL lives in `repository/`, one file per feature.** No other package contains a
query. The order and trade queries are separate files but share one transaction, so a
matching outcome is written atomically.

## Requirements

- Go **1.22+** (developed on 1.26)
- PostgreSQL (any recent version)

### Dependencies

| Purpose | Library |
|---|---|
| HTTP server & routing | `net/http` (stdlib, method-based patterns) |
| PostgreSQL driver | `github.com/jackc/pgx/v5` |
| WebSocket | `github.com/coder/websocket` |
| Config (env only) | `github.com/spf13/viper` |
| Logging | `log/slog` (stdlib) |
| Testing | `testing` (stdlib) |
| OpenAPI spec generation (build-time CLI) | `github.com/swaggo/swag/v2` |
| Swagger UI serving | `github.com/swaggo/http-swagger/v2` |

## Configuration

Configuration is read from the environment (or a local `.env` file at the repo root).
Copy `.env.example` to `.env` and adjust:

| Variable | Default | Notes |
|---|---|---|
| `DB_DSN` | — | PostgreSQL DSN. **Required** — the app fails fast at startup if unset. |
| `API_KEY` | — | **Admin** key, sent as `X-API-Key`. **Required** — the app fails fast if unset. Broker keys are per-participant and live in the database, not here. |
| `HTTP_PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `DISABLE_DOCS` | `false` | Set `true` to unregister `/docs` and `/openapi.yaml` |

Variable names are **not** prefixed — the same spelling works in the shell, in `.env`, and
in `docker-compose.yml`.

Example `DB_DSN`: `postgres://postgres:postgres@localhost:5432/jast?sslmode=disable`

## Getting started

```bash
# 1. Configure
cp .env.example .env      # then edit DB_DSN and API_KEY (both required)

# 2. Create schema + seed data (idempotent — safe to re-run)
go run ./cmd/migrate

# 3. Start the server
go run .                  # listens on :8080
```

> Use `go run .` (compiles the whole package), **not** `go run main.go`.

The migration runner and the server both locate `.env` and `migrations/` by searching
upward from the working directory, so they work from any subdirectory.

### Using Docker

You can easily spin up the entire application—including the PostgreSQL database, automatic migrations, and the API server—using Docker Compose:

```bash
docker compose up --build
```

The Docker setup is configured so that the **database** and **app** start automatically.

To run the database migrations manually using Docker, run this command:
```bash
docker compose run --rm migrate
```

## API

Interactive documentation: **http://localhost:8080/docs** (Swagger UI).
Raw spec: **http://localhost:8080/openapi.yaml**.

The spec is **generated from the code** by [swag](https://github.com/swaggo/swag) — see
[Documentation](#documentation). Only the two documentation routes are unauthenticated.

### Two authentication tiers

| Tier | Header | Credential | Routes |
|---|---|---|---|
| **Admin** | `X-API-Key` | `API_KEY` from config | `/api/admin/*`, `/ws/admin/*` |
| **Participant** | `X-Participant-Key` | Per-broker key, stored hashed in the database | `/api/participant/*`, `/ws/participant/*` |

The tiers never mix: an admin key on a participant route is a 401, and vice versa.

Broker keys are **SHA-256 hashed**; only the hash and a short non-secret prefix are stored.
A key is returned exactly twice in the API's lifetime — when the broker is created, and when
the key is re-issued. **It cannot be retrieved afterwards**, because a hash does not reverse.
A lost key is replaced, not recovered. `GET /api/admin/participants` therefore shows
`api_key_prefix` and `has_api_key`, never the key.

Because authentication reads the database on every request, revocation takes effect on the
very next call rather than after a cache expires.

> **Order attribution is client-asserted.** `POST /api/participant/orders` still takes
> `participant` in the body and trusts it, so an authenticated broker can submit an order —
> and move share holdings — under another broker's code. The key proves the caller is *a*
> known broker, not *which* one. This is a deliberate decision, recorded here so it is
> visible; the authenticated identity is logged alongside the asserted one on every submit.

### Onboarding a broker

```bash
# Create a broker; the key comes back once and only once.
curl -X POST localhost:8080/api/admin/participants \
  -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' \
  -d '{"kode":"BB","nama":"Broker B"}'
# -> {"kode":"BB","nama":"Broker B","api_key":"jast_BB_9xQ2m..."}

# Re-issue (invalidates the old key) or revoke. The target travels in the body, never the
# path, so no identifier lands in access logs.
curl -X POST   localhost:8080/api/admin/participants/apikey \
  -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -d '{"participant":"BB"}'
curl -X DELETE localhost:8080/api/admin/participants/apikey \
  -H "X-API-Key: $API_KEY" -H 'Content-Type: application/json' -d '{"participant":"BB"}'
```

### Participant routes

| Route | Purpose |
|---|---|
| `POST /api/participant/orders` | Submit an order |
| `GET /api/participant/orderbook` | Every book, paginated |
| `GET /api/participant/orderbook/{kode}` | One book, aggregated by price level |
| `GET /api/participant/assets` | Own share holdings, with market value |
| `GET /api/participant/transactions` | Own fill history |
| `GET /api/participant/emiten/{kode}` | Instrument detail: price, free float, market cap |
| `GET /api/participant/emiten/{kode}/prices` | Price history, raw executions |
| `GET /api/participant/emiten/{kode}/candles` | Price history, OHLC (`1m`, `5m`, `1h`, `1d`) |
| `GET /ws/participant/orderbook/{kode}` | Book stream (WebSocket) |

```bash
curl -X POST localhost:8080/api/participant/orders \
  -H "X-Participant-Key: $KEY" -H 'Content-Type: application/json' \
  -d '{"emiten":"BBCA","participant":"YP","side":"buy","type":"limit","price":8000,"qty":100}'
```

```json
{
  "order":  { "id": 1, "status": "filled", "remaining": 0 },
  "trades": [ { "price": 8000, "qty": 100, "buy_order_id": 1, "sell_order_id": 7 } ]
}
```

`assets` and `transactions` scope to the caller **from the key** and accept no `participant`
parameter, so one broker cannot read another's positions or fills.

### Admin routes

| Route | Purpose |
|---|---|
| `GET`/`POST /api/admin/participants` | List or create brokers |
| `POST`/`DELETE /api/admin/participants/apikey` | Issue or revoke a broker key |
| `GET`/`POST /api/admin/emiten` | List or list-a-new instrument |
| `GET /api/admin/orders` | Order history |
| `GET /api/admin/trades` | Execution log |
| `GET /api/admin/transactions` | Any broker's fill history (`?participant=`) |
| `GET /api/admin/assets` | Holdings across brokers |
| `GET /ws/admin/orderbook/{kode}` | Book stream (WebSocket) |

A newly created emiten is **tradeable immediately** — it is registered with an empty book in
the live registry, with no restart.

### WebSocket

Outbound-only. On connect the server sends a full snapshot, then a fresh snapshot each time
the book changes. Orders are **never** accepted over WebSocket. Both tiers receive the
identical payload from the same controller; only the credential differs.

```
ws://localhost:8080/ws/participant/orderbook/BBCA
ws://localhost:8080/ws/admin/orderbook/BBCA
```

Neither is under `/api`.

## Matching rules

- A **buy** matches the cheapest ask when `buy_price >= ask_price`.
- A **sell** matches the highest bid when `sell_price <= bid_price`.
- **Execution price is the passive (resting) order's price**, not the incoming order's.
- On each match, `qty = min(remaining_incoming, remaining_passive)`; a fully consumed
  passive order leaves the book as `filled`.
- A **limit** order with leftover quantity rests in the book (`open`).
- A **market** order has no price limit, never rests in the book, and any unfillable
  remainder is `cancelled`.

`Seq` is the time-priority key: monotonic, never reused. It is issued by a single shared
sequencer, seeded at startup from the highest values already in the database, so it stays
unique across emiten and across restarts.

## Share holdings

`broker_assets_list` records what each broker holds of each emiten. It is written **inside the
same transaction as the match that moves it**, so a position can never disagree with the
trades behind it.

A sell is rejected before matching if the broker cannot cover it. Availability is
`holdings − reserved`, where *reserved* is the quantity already committed to that broker's
resting sell orders — without that, a broker holding 100 could rest two sells of 100 each
(both passing a naive balance check) and go negative when both filled, violating
`CHECK (amount_shared >= 0)` at commit, *after* matching had already moved the book.

Both figures live in the market kernel under the **same mutex as matching**, so the check and
the commitment it authorises are one atomic step. Holdings are seeded from the database at
startup; reservations start empty, which is consistent with the book itself starting empty.

**Market value is derived, never stored.** `value = last_traded_price × shares`, computed on
read for both holdings and emiten market cap. A stored column would need updating for *every*
holder of an instrument on *every* trade in it — or it would silently go stale for every
broker that did not trade. It is `null`, not `0`, for an instrument that has never traded.

> The in-memory book is the source of truth for matching. The `orders` table is history /
> audit; on restart the book starts empty (book recovery is not implemented).

## Concurrency

All matching is serialized: only one goroutine touches the order books at a time, so
matching is sequential and deterministic and price-time priority is never violated. There
is no per-order locking and no parallel matching.

The lock lives inside `market.Registry` and is private to it. Every operation that touches
a book is a method on the Registry, so no caller can forget to take it — `Submit` matches
and snapshots under a single acquisition, which is also what guarantees the book state
returned to a caller is exactly the book that produced its trades.

## Development

```bash
make test          # all tests
make test-engine   # engine tests only (no DB required) — the critical gate
make vet
make build
make run           # go run .
make migrate       # go run ./cmd/migrate
make check         # vet + build + test
make docs          # regenerate the OpenAPI spec (= go run ./cmd/gendocs)
```

There is no `make` on some Windows setups; every target above has a plain `go` equivalent,
and doc generation is `go run ./cmd/gendocs`.

The engine test suite (`go test ./engine/...`) covers ordered insert,
simple/partial/multi-level matching, market orders, time-priority tie-breaks, and the
reference validation scenario — all in memory, no database needed.

### Documentation

Nothing about the docs is written by hand. `platform/docs/swagger.yaml` and `swagger.json`
are **generated** from the annotation comments on `main.go` (general API info) and on the
controller methods, plus the struct tags on the DTOs. The UI is the real Swagger UI shipped
by `swaggo/files` — there is no hand-maintained HTML page and no CDN.

```bash
# once: install the swag CLI (pinned; v2 is still a release candidate)
go install github.com/swaggo/swag/v2/cmd/swag@v2.0.0-rc5

# after changing a route, a DTO, or an annotation
go run ./cmd/gendocs
```

Commit the regenerated files: the binary embeds `swagger.yaml` with `go:embed`, so the
Docker build never needs the swag binary. Generation uses `--ot yaml,json`, which skips
`docs.go` — swag itself is a build-time tool only.

Three details worth knowing before changing this setup:

- **The spec is served as OpenAPI 3.0.3.** swag emits only Swagger 2.0 or OpenAPI 3.1 —
  there is no 3.0 option — but the Swagger UI bundled with `swaggo/files` cannot render
  3.1 (*"does not specify a valid version field"*). The generated document uses no
  3.1-only construct, so `cmd/gendocs` retags it to 3.0.3, and **fails loudly** if swag
  ever starts emitting one rather than shipping a spec that misstates its own version.
- **Generation is a Go program, not a Makefile recipe.** `cmd/gendocs` runs anywhere a Go
  toolchain does — no `make`, `bash`, or `sed` needed, which matters on Windows.
- **Swagger UI is pointed at `/openapi.yaml`, not at swag's registered `doc.json`.**
  `http-swagger` reads that registry through swag **v1**, while the spec is produced by
  swag **v2** — separate registries, so the UI would fail to load the definition. Serving
  the embedded document ourselves avoids the mismatch.

## Data derived from trades

Once trades exist, two things follow as derivations (not separate features):

- **Stock price** = aggregation of the `trades` table. Last price = latest trade price;
  OHLC = aggregation per time interval.
- **Index (IHSG)** = market-cap-weighted from stock prices
  (`market_cap = last_price × listed_shares`). The initial version omits free-float and
  divisor adjustments.
