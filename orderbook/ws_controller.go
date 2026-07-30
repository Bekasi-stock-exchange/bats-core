package orderbook

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// wsWriteTimeout bounds a single frame write so one stalled client cannot pin the
// connection open indefinitely.
const wsWriteTimeout = 5 * time.Second

// WSController streams book updates over WebSocket.
type WSController struct {
	svc *Service
}

// NewWSController returns a WebSocket controller backed by svc.
func NewWSController(svc *Service) *WSController {
	return &WSController{svc: svc}
}

// Stream handles GET /ws/orderbook/{kode}.
//
// The stream is outbound-only: on connect it sends a full snapshot, then a fresh
// full snapshot every time the book for this emiten changes. Orders never arrive
// over WebSocket — they come only through POST /api/orders — so there is a single,
// synchronized entry path into the engine. Full snapshots each time are
// sufficient; delta updates are a deliberate non-goal for now.
//
//	@Summary		Stream order book updates (WebSocket)
//	@Description	Upgrades to a WebSocket connection. On connect the server sends a full
//	@Description	snapshot — the same shape as GET /api/orderbook/{kode}, tagged with
//	@Description	type "update" — then a fresh full snapshot every time this emiten's book
//	@Description	changes. The stream is outbound-only; orders are never accepted over it.
//	@Description
//	@Description	This endpoint cannot be exercised from Swagger UI, because it is not plain
//	@Description	HTTP. Connect with a WebSocket client at ws://localhost:8080/ws/orderbook/BBCA.
//	@Description	Note it is deliberately not under the /api prefix.
//	@Tags			orderbook
//	@ID				streamOrderBook
//	@Produce		json
//	@Param			kode	path		string					true	"Emiten code"	example(BBCA)
//	@Success		101		{object}	orderbook.BookSnapshot	"Switching Protocols; thereafter each message is a book snapshot"
//	@Failure		401		{object}	httpx.ErrorResponse		"Missing or wrong X-API-Key"
//	@Failure		404		{object}	httpx.ErrorResponse		"Unknown emiten"
//	@Security		ApiKeyAuth
//	@Router			/ws/orderbook/{kode} [get]
func (c *WSController) Stream(w http.ResponseWriter, r *http.Request) {
	kode := r.PathValue("kode")

	em, err := c.svc.Emiten(kode)
	if err != nil {
		// Written as an HTTP response because the upgrade has not happened yet.
		writeBookError(w, err, kode)
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
	sub := c.svc.Subscribe(em.ID)
	defer c.svc.Unsubscribe(em.ID, sub)

	state, err := c.svc.Snapshot(em.ID)
	if err != nil {
		return
	}
	if err := writeSnapshot(ctx, conn, ToBookUpdate(state)); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			conn.Close(websocket.StatusNormalClosure, "")
			return
		case update := <-sub.Updates():
			if err := writeSnapshot(ctx, conn, ToBookUpdate(update)); err != nil {
				return
			}
		}
	}
}

func writeSnapshot(ctx context.Context, conn *websocket.Conn, snap BookSnapshot) error {
	wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
	defer cancel()
	return wsjson.Write(wctx, conn, snap)
}
