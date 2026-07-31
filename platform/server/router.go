// Package server builds the route table. It is the only place that knows which
// URL maps to which controller, and it holds no state of its own.
package server

import (
	"net/http"

	"bekasi-automatic-trading-system/assets"
	"bekasi-automatic-trading-system/emiten"
	"bekasi-automatic-trading-system/index"
	"bekasi-automatic-trading-system/marketconfig"
	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/orderbook"
	"bekasi-automatic-trading-system/participant"
	"bekasi-automatic-trading-system/platform/docs"
	"bekasi-automatic-trading-system/platform/httpx"
	"bekasi-automatic-trading-system/trade"
	"bekasi-automatic-trading-system/underwriter"
	"bekasi-automatic-trading-system/wallet"
)

// Deps is everything the route table needs, assembled by the composition root.
type Deps struct {
	APIKey      string
	DisableDocs bool

	// ParticipantAuth authenticates a broker by its database-stored key.
	ParticipantAuth httpx.Middleware

	Order       *order.Controller
	OrderBook   *orderbook.Controller
	WS          *orderbook.WSController
	Participant *participant.Controller
	Emiten      *emiten.Controller
	Index       *index.Controller
	Underwriter *underwriter.Controller
	Config      *marketconfig.Controller
	Assets      *assets.Controller
	Wallet      *wallet.Controller
	Trade       *trade.Controller
	Docs        *docs.Controller
}

// Handler builds the route table. Go 1.22+ method-based patterns; stdlib only.
//
// Two authentication tiers, never mixed:
//
//   - /api/participant/* and /ws/participant/* take a per-broker key from the
//     database, so a key can be issued and revoked per broker.
//   - /api/admin/* and /ws/admin/* take the single static key from configuration.
//
// Both middlewares are built once here and applied per group, so a route cannot
// be registered unguarded by omission.
func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()

	admin := httpx.RequireStaticKey(d.APIKey)
	broker := d.ParticipantAuth

	// --- Participant tier: trading and market data -----------------------
	mux.HandleFunc("POST /api/participant/orders", broker(d.Order.Submit))
	mux.HandleFunc("GET /api/participant/orderbook", broker(d.OrderBook.List))
	mux.HandleFunc("GET /api/participant/orderbook/{kode}", broker(d.OrderBook.Get))
	mux.HandleFunc("GET /api/participant/assets", broker(d.Assets.Mine))
	mux.HandleFunc("GET /api/participant/wallet", broker(d.Wallet.Mine))
	mux.HandleFunc("GET /api/participant/transactions", broker(d.Trade.MyTransactions))
	mux.HandleFunc("GET /api/participant/emiten/{kode}", broker(d.Emiten.Detail))
	mux.HandleFunc("GET /api/participant/emiten/{kode}/prices", broker(d.Trade.Ticks))
	mux.HandleFunc("GET /api/participant/emiten/{kode}/candles", broker(d.Trade.Candles))
	// The composite index is market data, so it sits in the participant tier
	// alongside the order book: every broker reads the same level.
	mux.HandleFunc("GET /api/participant/index", broker(d.Index.Current))
	mux.HandleFunc("GET /api/participant/index/history", broker(d.Index.History))

	// --- Admin tier: management and oversight ----------------------------
	// Trading parameters are an exchange operation, never a broker one: a rule
	// every participant is held to cannot be editable by one of them.
	mux.HandleFunc("GET /api/admin/config", admin(d.Config.Get))
	mux.HandleFunc("PUT /api/admin/config", admin(d.Config.Update))
	mux.HandleFunc("GET /api/admin/participants", admin(d.Participant.List))
	mux.HandleFunc("POST /api/admin/participants", admin(d.Participant.Create))
	mux.HandleFunc("POST /api/admin/participants/apikey", admin(d.Participant.IssueKey))
	mux.HandleFunc("DELETE /api/admin/participants/apikey", admin(d.Participant.RevokeKey))
	mux.HandleFunc("GET /api/admin/emiten", admin(d.Emiten.List))
	mux.HandleFunc("POST /api/admin/emiten", admin(d.Emiten.Create))
	mux.HandleFunc("GET /api/admin/emiten/{kode}", admin(d.Emiten.AdminDetail))
	mux.HandleFunc("GET /api/admin/underwriters", admin(d.Underwriter.List))
	mux.HandleFunc("POST /api/admin/underwriters", admin(d.Underwriter.Create))
	// Admin-only by design: an offering both lists an instrument and issues its
	// shares, so it is an exchange operation, never a broker one.
	mux.HandleFunc("POST /api/admin/ipo", admin(d.Underwriter.IPO))
	mux.HandleFunc("GET /api/admin/orders", admin(d.Order.List))
	mux.HandleFunc("GET /api/admin/trades", admin(d.Trade.ListTrades))
	mux.HandleFunc("GET /api/admin/transactions", admin(d.Trade.AdminTransactions))
	mux.HandleFunc("GET /api/admin/assets", admin(d.Assets.All))
	mux.HandleFunc("GET /api/admin/wallets", admin(d.Wallet.All))

	// --- WebSocket: same stream, one door per tier -----------------------
	// One controller registered twice. The payload is identical; only the
	// credential differs, so there is no duplicated streaming logic.
	mux.HandleFunc("GET /ws/participant/orderbook/{kode}", broker(d.WS.Stream))
	mux.HandleFunc("GET /ws/admin/orderbook/{kode}", admin(d.WS.Stream))

	if !d.DisableDocs {
		// Documentation is intentionally unauthenticated: it is a static spec and
		// a viewer page, and requiring a header would make Swagger UI unusable.
		//
		// The UI is a subtree ("/docs/") because Swagger UI fetches its own assets
		// beneath that prefix. Go's ServeMux redirects a bare /docs to /docs/.
		mux.HandleFunc("GET /docs/", d.Docs.UI)
		mux.HandleFunc("GET "+docs.SpecPath, d.Docs.Spec)
	}
	return mux
}
