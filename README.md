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
- OpenAPI 3.1 documentation generated from the code and served from the binary

**Deliberately out of scope:** call auction / pre-opening, other order types (stop-loss,
iceberg, FOK, GTD), customer accounts / balances / portfolios, clearing & settlement,
corporate actions, index free-float adjustment, microservices, message brokers, auth,
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
order       →  market, platform
orderbook   →  market, platform
market      →  market/engine
repository  →  order, market        (implements the interfaces THEY declare)
main        →  everything           (composition root)

market/engine imports nothing outside itself
order and orderbook never import each other
```

Two rules make that hold:

- **`market/engine` is pure.** No HTTP library, no database driver, no `encoding/json`, and
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
market/engine/     order.go  orderbook.go  matching.go  engine_test.go  # pure matching core
market/            registry.go  directory.go  book.go  hub.go  ports.go # books, THE lock, master data, fan-out
order/             controller.go  service.go  transformer.go  dto.go  ports.go
orderbook/         controller.go  ws_controller.go  service.go  transformer.go  dto.go
repository/        repository.go  master.go  emiten.go  participant.go
                   order.go  trade.go                                   # ALL SQL lives here
platform/config/   config.go                                            # viper env management
platform/postgres/ pool.go                                              # pgx pool + QueryAll helper
platform/httpx/    respond.go  pagination.go  middleware.go             # JSON, paging, auth
platform/docs/     handler.go  swagger.yaml  swagger.json               # Swagger UI + generated spec
platform/server/   router.go                                            # the route table
cmd/migrate/       main.go                                              # migration runner
migrations/        001_emiten.sql .. 006_orders_id_default.sql           # schema + seed
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
| `API_KEY` | — | Key clients send as `X-API-Key`. **Required** — the app fails fast if unset. |
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
[Documentation](#documentation). Every route below requires the `X-API-Key` header; only
the two documentation routes are open.

### `POST /api/orders`

Submit an order. Validation (emiten/participant exist, `qty > 0`, `price > 0` for limit)
happens in the **order service**, not the engine and not the controller.

The order, its trades, and the fills against the resting orders it consumed are written in
a **single transaction**, and the WebSocket broadcast happens only after that commit
succeeds.

```bash
curl -X POST localhost:8080/api/orders \
  -H "X-API-Key: $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"emiten":"BBCA","participant":"YP","side":"buy","type":"limit","price":8000,"qty":100}'
```

```json
{
  "order":  { "id": 1, "status": "filled", "remaining": 0 },
  "trades": [ { "price": 8000, "qty": 100, "buy_order_id": 1, "sell_order_id": 7 } ]
}
```

### `GET /api/orderbook/{kode}`

Current book for one emiten, aggregated by price level (bids high→low, asks low→high).

```bash
curl -H "X-API-Key: $API_KEY" localhost:8080/api/orderbook/BBCA
```

```json
{ "emiten": "BBCA", "bids": [{ "price": 8000, "qty": 150 }], "asks": [{ "price": 8050, "qty": 200 }] }
```

### `GET /api/orderbook`

Every book, paginated and ordered by emiten code. `page` defaults to 1; `limit` defaults
to 10 and is capped at 100.

```bash
curl -H "X-API-Key: $API_KEY" 'localhost:8080/api/orderbook?page=1&limit=10'
```

```json
{ "data": [ { "emiten": "BBCA", "bids": [], "asks": [] } ], "limit": 10, "page": 1, "total": 5 }
```

### `GET /ws/orderbook/{kode}` (WebSocket)

Outbound-only stream. On connect it sends a full snapshot, then a fresh full snapshot
each time the book changes. Orders are **not** accepted over WebSocket — only via
`POST /api/orders`.

Note this route is **not** under `/api`.

```
ws://localhost:8080/ws/orderbook/BBCA
```

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
make docs          # regenerate the OpenAPI spec from code
make docs-tool     # install the swag CLI (only needed for `make docs`)
```

The engine test suite (`go test ./market/engine/...`) covers ordered insert,
simple/partial/multi-level matching, market orders, time-priority tie-breaks, and the
reference validation scenario — all in memory, no database needed.

### Documentation

Nothing about the docs is written by hand. `platform/docs/swagger.yaml` and `swagger.json`
are **generated** from the annotation comments on `main.go` (general API info) and on the
controller methods, plus the struct tags on the DTOs. The UI is the real Swagger UI shipped
by `swaggo/files` — there is no hand-maintained HTML page and no CDN.

```bash
make docs-tool     # once: installs the swag v2 CLI (pinned; still a release candidate)
make docs          # after changing a route, a DTO, or an annotation
```

Commit the regenerated files: the binary embeds `swagger.yaml` with `go:embed`, so the
Docker build never needs the swag binary. Generation uses `--ot yaml,json`, which skips
`docs.go` — swag itself is a build-time tool only.

Two details worth knowing before changing this setup:

- **The spec is OpenAPI 3.1**, via swag's `--v3.1` flag. swag v2 emits only Swagger 2.0 or
  OpenAPI 3.1 — there is no 3.0 option.
- **Swagger UI is pointed at `/openapi.yaml`, not at swag's registered `doc.json`.**
  `http-swagger` reads that registry through swag **v1**, while a 3.1 spec is produced by
  swag **v2**, and the two registries are separate — the UI would fail to load the
  definition. Serving the embedded document ourselves avoids the mismatch and keeps the
  spec version our choice.

## Data derived from trades

Once trades exist, two things follow as derivations (not separate features):

- **Stock price** = aggregation of the `trades` table. Last price = latest trade price;
  OHLC = aggregation per time interval.
- **Index (IHSG)** = market-cap-weighted from stock prices
  (`market_cap = last_price × listed_shares`). The initial version omits free-float and
  divisor adjustments.
