package order

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"bekasi-automatic-trading-system/participant"
	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the HTTP surface of the write side. It decodes the request, calls
// the service, and writes the transformed result — no business rules live here.
type Controller struct {
	svc *Service
}

// NewController returns a controller backed by svc.
func NewController(svc *Service) *Controller {
	return &Controller{svc: svc}
}

// Submit handles POST /api/orders.
//
//	@Summary		Submit a new order
//	@Description	Runs the order through continuous matching and returns the trades it
//	@Description	produced. A limit order with leftover quantity rests in the book (open);
//	@Description	a market order with leftover quantity is cancelled and never booked.
//	@Description	The order, its trades and the fills against the resting orders it
//	@Description	consumed are persisted in a single transaction.
//	@Description
//	@Description	A sell is rejected if the broker does not hold enough shares once its
//	@Description	resting sell orders are counted, so a position can never go negative.
//	@Tags			participant
//	@ID				submitOrder
//	@Accept			json
//	@Produce		json
//	@Param			body	body		order.SubmitOrderRequest	true	"Order to submit"
//	@Success		200		{object}	order.SubmitOrderResponse	"Order processed: the resulting order state and any trades"
//	@Failure		400		{object}	httpx.ErrorResponse			"Unknown emiten/participant, bad side/type, qty <= 0, price <= 0 for a limit order, insufficient shares, or an inactive emiten"
//	@Failure		401		{object}	httpx.ErrorResponse			"Missing or wrong X-Participant-Key"
//	@Failure		500		{object}	httpx.ErrorResponse			"Persistence failure"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/orders [post]
func (c *Controller) Submit(w http.ResponseWriter, r *http.Request) {
	var req SubmitOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// The order is attributed to the participant named in the body, which is NOT
	// necessarily the broker that authenticated. Logging both is the audit trail
	// for that gap: if they ever diverge, this is where it shows.
	if caller, ok := participant.FromContext(r.Context()); ok && caller.Kode != req.Participant {
		slog.WarnContext(r.Context(), "order attributed to another participant",
			"authenticated", caller.Kode, "asserted", req.Participant, "emiten", req.Emiten)
	}

	res, err := c.svc.Submit(r.Context(), SubmitCommand{
		Emiten:      req.Emiten,
		Participant: req.Participant,
		Side:        req.Side,
		Type:        req.Type,
		Price:       req.Price,
		Qty:         req.Qty,
	})
	if err != nil {
		writeSubmitError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ToSubmitOrderResponse(res))
}

// List handles GET /api/admin/orders.
//
//	@Summary		List order history
//	@Description	Every order ever submitted, newest first, optionally filtered by emiten,
//	@Description	participant, or status. This is the audit trail; the live book is served
//	@Description	by the orderbook endpoints.
//	@Tags			admin
//	@ID				listOrders
//	@Produce		json
//	@Param			emiten		query		string	false	"Filter by emiten code"		example(BBCA)
//	@Param			participant	query		string	false	"Filter by broker code"		example(YP)
//	@Param			status		query		string	false	"Filter by status"			Enums(open, filled, cancelled)
//	@Param			page		query		int		false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit		query		int		false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200			{object}	httpx.Page[order.OrderHistoryView]
//	@Failure		400			{object}	httpx.ErrorResponse	"Unknown emiten/participant, or bad status"
//	@Failure		401			{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/orders [get]
func (c *Controller) List(w http.ResponseWriter, r *http.Request) {
	page, limit := httpx.ParsePagination(r)
	q := r.URL.Query()

	records, total, err := c.svc.History(r.Context(),
		q.Get("emiten"), q.Get("participant"), q.Get("status"), page, limit)
	if err != nil {
		writeSubmitError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK,
		httpx.NewPage(ToOrderHistoryViews(records, c.svc.Directory()), page, limit, total))
}

// writeSubmitError maps a service error onto the HTTP response. A broken business
// rule is the client's fault and carries its own message; anything else is ours
// and is reported without leaking internals.
func writeSubmitError(w http.ResponseWriter, err error) {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
		return
	}
	httpx.WriteError(w, http.StatusInternalServerError, "persist order failed")
}
