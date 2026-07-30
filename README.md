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
- Swagger / OpenAPI documentation served from the binary

**Deliberately out of scope:** call auction / pre-opening, other order types (stop-loss,
iceberg, FOK, GTD), customer accounts / balances / portfolios, clearing & settlement,
corporate actions, index free-float adjustment, microservices, message brokers, auth,
and any frontend.

## Architecture

A modular monolith with a strict, one-way dependency rule:

```
api  →  engine
api  →  store
main →  everything (wiring)

engine imports NOTHING from api/ or store/
```

The **`engine` package is pure**: it imports no HTTP library, no database driver, and no
`encoding/json`, and its types carry no struct tags. This keeps the matching logic
extractable into a standalone service later without change. JSON DTOs and DB row structs
live in `api/` and `store/` respectively, which convert to and from the engine types.

`viper` is confined to `config/`; every other package receives plain values as parameters.

### Layout

```
engine/     order.go  orderbook.go  matching.go  engine_test.go   # pure matching core
store/      postgres.go  emiten.go  trade.go                      # PostgreSQL (pgx)
api/        router.go  dto.go  order_handler.go  orderbook_handler.go
            ws_handler.go  hub.go  docs_handler.go  openapi.yaml  # transport (net/http)
config/     config.go                                             # viper env management
cmd/migrate/main.go                                               # migration runner
migrations/ 001_emiten.sql .. 005_seed.sql                        # schema + seed
main.go                                                           # server entry point
```

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

## Configuration

Configuration is read from the environment (or a local `.env` file at the repo root).
Copy `.env.example` to `.env` and adjust:

| Variable | Default | Notes |
|---|---|---|
| `DB_DSN` | — | PostgreSQL DSN. **Required** — the app fails fast at startup if unset. |
| `HTTP_PORT` | `8080` | HTTP server port |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |

Example `DB_DSN`: `postgres://postgres:postgres@localhost:5432/jast?sslmode=disable`

## Getting started

```bash
# 1. Configure
cp .env.example .env      # then edit DB_DSN

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

### `POST /orders`

Submit an order. Validation (emiten/participant exist, `qty > 0`, `price > 0` for limit)
happens in the API layer, not the engine.

```bash
curl -X POST localhost:8080/orders \
  -H 'Content-Type: application/json' \
  -d '{"emiten":"BBCA","participant":"YP","side":"buy","type":"limit","price":8000,"qty":100}'
```

```json
{
  "order":  { "id": 1, "status": "filled", "remaining": 0 },
  "trades": [ { "price": 8000, "qty": 100, "buy_order_id": 1, "sell_order_id": 7 } ]
}
```

### `GET /orderbook/{kode}`

Current book for one emiten, aggregated by price level (bids high→low, asks low→high).

```bash
curl localhost:8080/orderbook/BBCA
```

```json
{ "emiten": "BBCA", "bids": [{ "price": 8000, "qty": 150 }], "asks": [{ "price": 8050, "qty": 200 }] }
```

### `GET /ws/orderbook/{kode}` (WebSocket)

Outbound-only stream. On connect it sends a full snapshot, then a fresh full snapshot
each time the book changes. Orders are **not** accepted over WebSocket — only via
`POST /orders`.

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

## Development

```bash
make test          # all tests
make test-engine   # engine tests only (no DB required) — the critical gate
make vet
make build
make run           # go run .
make migrate       # go run ./cmd/migrate
make check         # vet + build + test
```

The engine test suite (`go test ./engine/...`) covers ordered insert, simple/partial/
multi-level matching, market orders, time-priority tie-breaks, and the reference
validation scenario — all in memory, no database needed.

## Data derived from trades

Once trades exist, two things follow as derivations (not separate features):

- **Stock price** = aggregation of the `trades` table. Last price = latest trade price;
  OHLC = aggregation per time interval.
- **Index (IHSG)** = market-cap-weighted from stock prices
  (`market_cap = last_price × listed_shares`). The initial version omits free-float and
  divisor adjustments.
