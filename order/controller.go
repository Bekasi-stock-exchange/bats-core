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
//	@Description
//	@Description	**Auto-rejection.** A limit order priced outside the instrument's session
//	@Description	band is rejected and never reaches the book, so the price it asked for
//	@Description	never becomes a level anything else can match against. The band is
//	@Description	`reference_price ± emiten_halt_bps` (30% by default) and is anchored to the
//	@Description	session reference, not to the last trade — an anchor that moved with the
//	@Description	market would let the price walk out of the band one legal step at a time.
//	@Description	The rejection message quotes the permitted range.
//	@Description
//	@Description	**Circuit breaker.** A trade that prints at either edge of the band halts
//	@Description	the instrument for halt_duration_seconds (2 minutes by default). Orders
//	@Description	submitted during a halt are rejected with the resume time; the book stays
//	@Description	readable and resting orders keep their time priority. Trading reopens by
//	@Description	itself — no request is needed.
//	@Tags			participant
//	@ID				submitOrder
//	@Accept			json
//	@Produce		json
//	@Param			body	body		order.SubmitOrderRequest	true	"Order to submit"
//	@Success		200		{object}	order.SubmitOrderResponse	"Order processed: the resulting order state and any trades"
//	@Failure		400		{object}	httpx.ErrorResponse			"Unknown emiten/participant, bad side/type, qty <= 0, price <= 0 for a limit order, insufficient shares, an inactive emiten, a price outside the permitted band, or a halted instrument"
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
		var invalid ValidationError
		if !errors.As(err, &invalid) {
			slog.ErrorContext(r.Context(), "order submit failed", "error", err,
				"participant", req.Participant, "emiten", req.Emiten)
		}
		writeSubmitError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ToSubmitOrderResponse(res))
}

// Cancel handles POST /api/participant/orders/cancel.
//
//	@Summary		Cancel a resting order
//	@Description	Withdraws an order that is still resting in the book and releases
//	@Description	what it had reserved — shares for a sell, cash for a buy — so the
//	@Description	broker can use them again immediately. The quantity that already
//	@Description	filled stays filled; only the remainder is withdrawn.
//	@Description
//	@Description	Only the broker that placed the order may cancel it. An order that
//	@Description	has already filled or been cancelled is not resting, so it cannot be
//	@Description	withdrawn and the request is rejected.
//	@Description
//	@Description	The book row and the stored order are cancelled in one step, and the
//	@Description	updated book is published to subscribers. A cancel produces no trade
//	@Description	and therefore moves no price.
//	@Description
//	@Description	Cancelling is allowed while the instrument is halted: a halt stops new
//	@Description	orders from arriving, it does not trap the ones already resting.
//	@Tags			participant
//	@ID				cancelOrder
//	@Accept			json
//	@Produce		json
//	@Param			body	body		order.CancelOrderRequest	true	"Order to cancel"
//	@Success		200		{object}	order.CancelOrderResponse	"Order cancelled: its final state and the quantity released"
//	@Failure		400		{object}	httpx.ErrorResponse			"Unknown emiten/participant, order_id <= 0, an order that is not resting, or an order belonging to another participant"
//	@Failure		401		{object}	httpx.ErrorResponse			"Missing or wrong X-Participant-Key"
//	@Failure		500		{object}	httpx.ErrorResponse			"Persistence failure"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/orders/cancel [post]
func (c *Controller) Cancel(w http.ResponseWriter, r *http.Request) {
	var req CancelOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	// Same audit trail as Submit: the cancellation is attributed to the
	// participant named in the body, which need not be the broker that
	// authenticated. The registry still refuses to cancel an order the named
	// participant does not own, so this logs the gap rather than closing it.
	if caller, ok := participant.FromContext(r.Context()); ok && caller.Kode != req.Participant {
		slog.WarnContext(r.Context(), "cancel attributed to another participant",
			"authenticated", caller.Kode, "asserted", req.Participant,
			"emiten", req.Emiten, "order", req.OrderID)
	}

	res, err := c.svc.Cancel(r.Context(), CancelCommand{
		Emiten:      req.Emiten,
		Participant: req.Participant,
		OrderID:     req.OrderID,
	})
	if err != nil {
		var invalid ValidationError
		if !errors.As(err, &invalid) {
			slog.ErrorContext(r.Context(), "order cancel failed", "error", err,
				"participant", req.Participant, "emiten", req.Emiten, "order", req.OrderID)
		}
		writeCancelError(w, err)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ToCancelOrderResponse(res))
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

// writeCancelError is writeSubmitError for the cancel path. Same split — a
// broken business rule is the client's, anything else is ours — with a message
// that names what actually failed rather than a persist the caller never asked
// for.
func writeCancelError(w http.ResponseWriter, err error) {
	var invalid ValidationError
	if errors.As(err, &invalid) {
		httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
		return
	}
	httpx.WriteError(w, http.StatusInternalServerError, "cancel order failed")
}
