// Package server builds the route table. It is the only place that knows which
// URL maps to which controller, and it holds no state of its own.
package server

import (
	"net/http"

	"bekasi-automatic-trading-system/assets"
	"bekasi-automatic-trading-system/corporateaction"
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
	Corporate   *corporateaction.Controller
	Config      *marketconfig.Controller
	Assets      *assets.Controller
	Wallet      *wallet.Controller
	Trade       *trade.Controller
	Docs        *docs.Controller
}

// Go 1.22+ method-based patterns; stdlib only.
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
	// A cancellation is a broker withdrawing its own liquidity, so it sits in the
	// participant tier beside the submission it undoes. POST rather than DELETE:
	// the order is not removed, it moves to a terminal state and stays in history.
	mux.HandleFunc("POST /api/participant/orders/cancel", broker(d.Order.Cancel))
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
	mux.HandleFunc("POST /api/admin/emiten/{kode}/ipo", admin(d.Underwriter.IPOExisting))
	mux.HandleFunc("GET /api/admin/underwriters", admin(d.Underwriter.List))
	mux.HandleFunc("POST /api/admin/underwriters", admin(d.Underwriter.Create))
	// Corporate actions are admin-only for the same reason an offering is: a
	// broker able to split its own holdings or pay itself a dividend could mint
	// shares and cash at will. Announce and execute are separate routes because
	// they are separate decisions — the first can still be taken back, the second
	// has moved every holder's ledger.
	mux.HandleFunc("GET /api/admin/corporate-actions", admin(d.Corporate.List))
	mux.HandleFunc("POST /api/admin/corporate-actions", admin(d.Corporate.Announce))
	mux.HandleFunc("GET /api/admin/corporate-actions/{id}", admin(d.Corporate.Detail))
	mux.HandleFunc("POST /api/admin/corporate-actions/{id}/execute", admin(d.Corporate.Execute))
	mux.HandleFunc("POST /api/admin/corporate-actions/{id}/cancel", admin(d.Corporate.Cancel))
	// Admin-only by design: an offering both lists an instrument and issues its
	// shares, so it is an exchange operation, never a broker one.
	mux.HandleFunc("POST /api/admin/ipo", admin(d.Underwriter.IPO))
	mux.HandleFunc("GET /api/admin/orders", admin(d.Order.List))
	mux.HandleFunc("GET /api/admin/trades", admin(d.Trade.ListTrades))
	mux.HandleFunc("GET /api/admin/transactions", admin(d.Trade.AdminTransactions))
	mux.HandleFunc("GET /api/admin/assets", admin(d.Assets.All))
	mux.HandleFunc("GET /api/admin/wallets", admin(d.Wallet.All))
	// Funding is admin-only for the same reason an offering is: a broker able to
	// credit its own wallet could buy without limit.
	mux.HandleFunc("POST /api/admin/wallets", admin(d.Wallet.Adjust))
	// The index reads are the same payload the participant tier serves — one
	// index, no per-broker version — offered here so an operator can use the key
	// it already holds. Recompute and capture are admin-only in substance:
	// revaluing the whole market and writing history are exchange operations, not
	// something a broker should be able to schedule.
	mux.HandleFunc("GET /api/admin/index", admin(d.Index.AdminCurrent))
	mux.HandleFunc("GET /api/admin/index/history", admin(d.Index.AdminHistory))
	mux.HandleFunc("POST /api/admin/index/recompute", admin(d.Index.Recompute))
	mux.HandleFunc("POST /api/admin/index/capture", admin(d.Index.Capture))

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
