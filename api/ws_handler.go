package api

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// handleWS handles GET /ws/orderbook/{kode}.
//
// The stream is outbound-only: on connect it sends a full snapshot, then a fresh
// full snapshot every time the book for this emiten changes. Orders never arrive
// over WebSocket — they come only through POST /orders — so there is a single,
// synchronized entry path into the engine. Full snapshots each time are
// sufficient; delta updates are a deliberate non-goal for now.
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	kode := r.PathValue("kode")
	em, ok := s.emitenByKode[kode]
	if !ok {
		writeError(w, http.StatusNotFound, "unknown emiten: "+kode)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return // Accept already wrote the response
	}
	defer conn.Close(websocket.StatusInternalError, "closing")

	ctx := conn.CloseRead(r.Context()) // detect client close; we only write

	// Subscribe before taking the initial snapshot so no update is missed in the
	// gap between snapshot and subscription.
	sub := s.hub.subscribe(em.ID)
	defer s.hub.unsubscribe(em.ID, sub)

	s.mu.Lock()
	initial := updateSnapshot(snapshotBook(em.Kode, s.engineFor(em.ID).Book()))
	s.mu.Unlock()

	if err := writeSnapshot(ctx, conn, initial); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case snap := <-sub.ch:
			if err := writeSnapshot(ctx, conn, snap); err != nil {
				return
			}
		}
	}
}

func writeSnapshot(ctx context.Context, conn *websocket.Conn, snap bookSnapshot) error {
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(wctx, conn, snap)
}
