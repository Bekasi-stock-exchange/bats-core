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

func NewWSController(svc *Service) *WSController {
	return &WSController{svc: svc}
}

// The stream is outbound-only: on connect it sends a full snapshot, then a fresh
// full snapshot every time the book for this emiten changes. Orders never arrive
// over WebSocket — they come only through POST /api/orders — so there is a single,
// synchronized entry path into the engine. Full snapshots each time are
// sufficient; delta updates are a deliberate non-goal for now.
//
//	@Summary		Stream order book updates (WebSocket, broker)
//	@Description	Upgrades to a WebSocket connection. On connect the server sends a full
//	@Description	snapshot — the same shape as GET /api/participant/orderbook/{kode}, tagged
//	@Description	with type "update" — then a fresh full snapshot every time this emiten's
//	@Description	book changes. The stream is outbound-only; orders are never accepted over it.
//	@Description
//	@Description	The identical stream is available to admin at /ws/admin/orderbook/{kode};
//	@Description	only the credential differs.
//	@Description
//	@Description	Not exercisable from Swagger UI, because it is not plain HTTP and browsers
//	@Description	cannot set headers on a WS handshake. Connect with a WebSocket client at
//	@Description	ws://localhost:8080/ws/participant/orderbook/BBCA.
//	@Tags			participant
//	@ID				streamOrderBookParticipant
//	@Produce		json
//	@Param			kode	path		string					true	"Emiten code"	example(BBCA)
//	@Success		101		{object}	orderbook.BookSnapshot	"Switching Protocols; thereafter each message is a book snapshot"
//	@Failure		401		{object}	httpx.ErrorResponse		"Missing or wrong X-Participant-Key"
//	@Failure		404		{object}	httpx.ErrorResponse		"Unknown emiten"
//	@Security		ParticipantKeyAuth
//	@Router			/ws/participant/orderbook/{kode} [get]
//
//	@Summary		Stream order book updates (WebSocket, admin)
//	@Description	The same outbound-only book stream as the participant route, reached with
//	@Description	the static admin key instead of a broker key. One controller serves both,
//	@Description	so the payload is identical.
//	@Tags			admin
//	@ID				streamOrderBookAdmin
//	@Produce		json
//	@Param			kode	path		string					true	"Emiten code"	example(BBCA)
//	@Success		101		{object}	orderbook.BookSnapshot	"Switching Protocols; thereafter each message is a book snapshot"
//	@Failure		401		{object}	httpx.ErrorResponse		"Missing or wrong X-API-Key"
//	@Failure		404		{object}	httpx.ErrorResponse		"Unknown emiten"
//	@Security		ApiKeyAuth
//	@Router			/ws/admin/orderbook/{kode} [get]
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
