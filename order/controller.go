package order

import (
	"encoding/json"
	"errors"
	"net/http"

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
//	@Tags			orders
//	@ID				submitOrder
//	@Accept			json
//	@Produce		json
//	@Param			body	body		order.SubmitOrderRequest	true	"Order to submit"
//	@Success		200		{object}	order.SubmitOrderResponse	"Order processed: the resulting order state and any trades"
//	@Failure		400		{object}	httpx.ErrorResponse			"Unknown emiten/participant, bad side/type, qty <= 0, or price <= 0 for a limit order"
//	@Failure		401		{object}	httpx.ErrorResponse			"Missing or wrong X-API-Key"
//	@Failure		500		{object}	httpx.ErrorResponse			"Persistence failure"
//	@Security		ApiKeyAuth
//	@Router			/api/orders [post]
func (c *Controller) Submit(w http.ResponseWriter, r *http.Request) {
	var req SubmitOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
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
