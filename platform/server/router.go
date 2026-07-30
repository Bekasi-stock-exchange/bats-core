// Package server builds the route table. It is the only place that knows which
// URL maps to which controller, and it holds no state of its own.
package server

import (
	"net/http"

	"bekasi-automatic-trading-system/order"
	"bekasi-automatic-trading-system/orderbook"
	"bekasi-automatic-trading-system/platform/docs"
	"bekasi-automatic-trading-system/platform/httpx"
)

// Deps is everything the route table needs, assembled by the composition root.
type Deps struct {
	APIKey      string
	DisableDocs bool

	Order     *order.Controller
	OrderBook *orderbook.Controller
	WS        *orderbook.WSController
	Docs      *docs.Controller
}

// Handler builds the route table. Go 1.22+ method-based patterns; stdlib only.
func Handler(d Deps) http.Handler {
	mux := http.NewServeMux()
	auth := httpx.RequireAPIKey(d.APIKey)

	mux.HandleFunc("POST /api/orders", auth(d.Order.Submit))
	mux.HandleFunc("GET /api/orderbook", auth(d.OrderBook.List))
	mux.HandleFunc("GET /api/orderbook/{kode}", auth(d.OrderBook.Get))
	mux.HandleFunc("GET /ws/orderbook/{kode}", auth(d.WS.Stream))

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
