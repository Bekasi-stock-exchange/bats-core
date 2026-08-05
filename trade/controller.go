package trade

import (
	"errors"
	"net/http"
	"strings"

	"bekasi-automatic-trading-system/participant"
	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the HTTP surface for reading executions.
type Controller struct {
	svc   *Service
	codes Codes
}

func NewController(svc *Service, codes Codes) *Controller {
	return &Controller{svc: svc, codes: codes}
}

//	@Summary		List executions
//	@Description	The raw execution log, newest first, optionally filtered by emiten or
//	@Description	participant. Each row is one buy order matched against one sell order.
//	@Tags			admin
//	@ID				listTrades
//	@Produce		json
//	@Param			emiten		query		string	false	"Filter by emiten code"			example(BBCA)
//	@Param			participant	query		string	false	"Filter by broker code"			example(YP)
//	@Param			page		query		int		false	"Page number, from 1"			default(1)	minimum(1)
//	@Param			limit		query		int		false	"Items per page, max 100"		default(10)	minimum(1)	maximum(100)
//	@Success		200			{object}	httpx.Page[trade.TradeView]
//	@Failure		401			{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404			{object}	httpx.ErrorResponse	"Unknown emiten or participant filter"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/trades [get]
func (c *Controller) ListTrades(w http.ResponseWriter, r *http.Request) {
	page, limit := httpx.ParsePagination(r)
	emitenKode := r.URL.Query().Get("emiten")
	participantKode := r.URL.Query().Get("participant")

	records, total, err := c.svc.Trades(r.Context(), emitenKode, participantKode, page, limit)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK,
		httpx.NewPage(c.codes.ToTradeViews(records), page, limit, total))
}

//	@Summary		Transaction history for any broker
//	@Description	Fills seen from a broker's own side. Omit `participant` to include every
//	@Description	broker. A broker that matched its own resting order appears twice for
//	@Description	that execution, once per side.
//	@Tags			admin
//	@ID				adminTransactions
//	@Produce		json
//	@Param			participant	query		string	true	"Broker code"				example(YP)
//	@Param			emiten		query		string	false	"Filter by emiten code"		example(BBCA)
//	@Param			page		query		int		false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit		query		int		false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200			{object}	httpx.Page[trade.TransactionView]
//	@Failure		400			{object}	httpx.ErrorResponse	"participant is required"
//	@Failure		401			{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404			{object}	httpx.ErrorResponse	"Unknown participant or emiten"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/transactions [get]
func (c *Controller) AdminTransactions(w http.ResponseWriter, r *http.Request) {
	kode := strings.TrimSpace(r.URL.Query().Get("participant"))
	if kode == "" {
		httpx.WriteError(w, http.StatusBadRequest, "participant is required")
		return
	}

	id, ok := c.svc.ResolveParticipant(kode)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "unknown participant: "+kode)
		return
	}
	c.writeTransactions(w, r, id)
}

//	@Summary		Own transaction history
//	@Description	Every fill for the authenticated broker, newest first, seen from its own
//	@Description	side — so `side` and `counterparty` are relative to the caller.
//	@Description
//	@Description	The broker is taken from the API key. This endpoint accepts no
//	@Description	`participant` parameter, so one broker cannot read another's fills.
//	@Tags			participant
//	@ID				myTransactions
//	@Produce		json
//	@Param			emiten	query		string	false	"Filter by emiten code"		example(BBCA)
//	@Param			page	query		int		false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit	query		int		false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200		{object}	httpx.Page[trade.TransactionView]
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Failure		404		{object}	httpx.ErrorResponse	"Unknown emiten filter"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/transactions [get]
func (c *Controller) MyTransactions(w http.ResponseWriter, r *http.Request) {
	id, ok := participant.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.writeTransactions(w, r, id.ID)
}

func (c *Controller) writeTransactions(w http.ResponseWriter, r *http.Request, participantID int64) {
	page, limit := httpx.ParsePagination(r)

	txs, total, err := c.svc.Transactions(r.Context(), participantID, r.URL.Query().Get("emiten"), page, limit)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK,
		httpx.NewPage(c.codes.ToTransactionViews(txs), page, limit, total))
}

//	@Summary		Price history (raw executions)
//	@Description	Every execution for an instrument, newest first. Build candles from this
//	@Description	client-side, or use the candles endpoint.
//	@Tags			participant
//	@ID				emitenTicks
//	@Produce		json
//	@Param			kode	path		string	true	"Emiten code"				example(BBCA)
//	@Param			page	query		int		false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit	query		int		false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200		{object}	httpx.Page[trade.TickView]
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Failure		404		{object}	httpx.ErrorResponse	"Unknown emiten"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/emiten/{kode}/prices [get]
func (c *Controller) Ticks(w http.ResponseWriter, r *http.Request) {
	page, limit := httpx.ParsePagination(r)

	ticks, total, err := c.svc.Ticks(r.Context(), r.PathValue("kode"), page, limit)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, httpx.NewPage(ToTickViews(ticks), page, limit, total))
}

//	@Summary		Price history (OHLC candles)
//	@Description	Executions aggregated into open/high/low/close bars with volume, newest
//	@Description	bar first. Buckets are UTC-aligned. Open and close follow execution
//	@Description	order, so two trades sharing a timestamp still resolve correctly.
//	@Tags			participant
//	@ID				emitenCandles
//	@Produce		json
//	@Param			kode		path		string	true	"Emiten code"	example(BBCA)
//	@Param			interval	query		string	false	"Bucket width"	Enums(1m, 5m, 1h, 1d)	default(1h)
//	@Success		200			{array}		trade.CandleView
//	@Failure		400			{object}	httpx.ErrorResponse	"Unsupported interval"
//	@Failure		401			{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Failure		404			{object}	httpx.ErrorResponse	"Unknown emiten"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/emiten/{kode}/candles [get]
func (c *Controller) Candles(w http.ResponseWriter, r *http.Request) {
	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "1h"
	}

	candles, err := c.svc.Candles(r.Context(), r.PathValue("kode"), interval)
	if err != nil {
		writeTradeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToCandleViews(candles))
}

func writeTradeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrEmitenNotFound):
		httpx.WriteError(w, http.StatusNotFound, "unknown emiten or participant")
	case errors.Is(err, ErrInvalidInterval):
		httpx.WriteError(w, http.StatusBadRequest,
			"interval must be one of: "+strings.Join(Intervals(), ", "))
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "trade request failed")
	}
}
