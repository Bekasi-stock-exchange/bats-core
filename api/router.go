// Package api is the transport layer: REST over net/http and WebSocket streaming.
// It depends on engine and store; engine depends on neither. All JSON DTOs and
// conversions live here so engine types stay tag-free.
package api

import (
	"context"
	"net/http"
	"sync"

	"bekasi-automatic-trading-system/engine"
	"bekasi-automatic-trading-system/store"
)

// Server holds all shared state for the HTTP surface.
//
// Concurrency model (spec §6): only one goroutine may touch a given order book
// at a time and matching must be sequential/deterministic. We enforce this with
// a single mutex guarding every book access — matching, snapshotting, and
// broadcasting all take it. There is no per-order locking and no parallel
// matching, so price-time priority stays deterministic.
type Server struct {
	store *store.Store

	mu      sync.Mutex
	engines map[int64]*engine.Engine // per emiten

	// Master-data lookups, loaded once at startup (read-only afterward).
	emitenByKode map[string]store.Emiten
	partByKode   map[string]store.Participant

	hub *hub

	disableDocs bool
	apiKey      string
}

// NewServer loads master data and wires an HTTP server. It fails if master data
// cannot be read.
func NewServer(ctx context.Context, st *store.Store, apiKey string, disableDocs bool) (*Server, error) {
	emitens, err := st.LoadEmiten(ctx)
	if err != nil {
		return nil, err
	}
	parts, err := st.LoadParticipant(ctx)
	if err != nil {
		return nil, err
	}

	// Seed one shared sequencer from the DB so order/trade Seq continue past
	// values already persisted and stay globally unique across all emiten.
	maxOrderSeq, maxTradeSeq, err := st.MaxSeqs(ctx)
	if err != nil {
		return nil, err
	}
	seqr := engine.NewSequencer(maxOrderSeq, maxTradeSeq)

	s := &Server{
		store:        st,
		engines:      make(map[int64]*engine.Engine),
		emitenByKode: make(map[string]store.Emiten),
		partByKode:   make(map[string]store.Participant),
		hub:          newHub(),
		disableDocs:  disableDocs,
		apiKey:       apiKey,
	}
	for _, e := range emitens {
		s.emitenByKode[e.Kode] = e
		s.engines[e.ID] = engine.NewEngineWithSequencer(e.ID, seqr)
	}
	for _, p := range parts {
		s.partByKode[p.Kode] = p
	}
	return s, nil
}

// Handler builds the route table. Go 1.22+ method-based patterns; stdlib only.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /orders", s.requireAPIKey(s.handleSubmitOrder))
	mux.HandleFunc("GET /orderbook", s.requireAPIKey(s.handleOrderBooks))
	mux.HandleFunc("GET /orderbook/{kode}", s.requireAPIKey(s.handleOrderBook))
	mux.HandleFunc("GET /ws/orderbook/{kode}", s.requireAPIKey(s.handleWS))

	if !s.disableDocs {
		// API documentation (Swagger UI + the OpenAPI spec it renders).
		mux.HandleFunc("GET /docs", s.handleDocs)
		mux.HandleFunc("GET /openapi.yaml", s.handleOpenAPISpec)
	}
	return mux
}

func (s *Server) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		// Browsers might not send custom headers in WS requests natively.
		// For WS, if it's via a browser, they'd use a query param or a subprotocol,
		// but since this API specifies "Not exercisable from Swagger UI" and it's
		// meant for clients, checking headers is fine.
		if key != s.apiKey {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// engineFor returns the engine for an emiten id. Caller must hold s.mu.
func (s *Server) engineFor(emitenID int64) *engine.Engine {
	return s.engines[emitenID]
}
