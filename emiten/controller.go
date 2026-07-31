package emiten

import (
	"encoding/json"
	"errors"
	"net/http"

	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the HTTP surface for listed instruments. Admin lists and creates;
// participants read the detail view.
type Controller struct {
	svc *Service
	// duplicate reports whether an error is a unique-constraint violation,
	// injected so this package needs no dependency on pgx error codes.
	duplicate func(error) bool
}

// NewController returns a controller backed by svc.
func NewController(svc *Service, isDuplicate func(error) bool) *Controller {
	return &Controller{svc: svc, duplicate: isDuplicate}
}

// List handles GET /api/admin/emiten.
//
//	@Summary		List listed instruments
//	@Description	Master data for every emiten, ordered by code. Price statistics are
//	@Description	deliberately excluded — computing them per row would be a query per
//	@Description	instrument. Use the detail endpoint for those.
//	@Tags			admin
//	@ID				listEmiten
//	@Produce		json
//	@Param			page	query		int	false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit	query		int	false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200		{object}	httpx.Page[emiten.EmitenView]
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/emiten [get]
func (c *Controller) List(w http.ResponseWriter, r *http.Request) {
	page, limit := httpx.ParsePagination(r)

	emitens, total := c.svc.List(page, limit)
	httpx.WriteJSON(w, http.StatusOK, httpx.NewPage(ToEmitenViews(emitens), page, limit, total))
}

// Create handles POST /api/admin/emiten.
//
//	@Summary		List a new instrument
//	@Description	Creates an emiten and registers it with an empty order book, so it is
//	@Description	tradeable immediately — no restart required. It starts active.
//	@Tags			admin
//	@ID				createEmiten
//	@Accept			json
//	@Produce		json
//	@Param			body	body		emiten.CreateRequest	true	"Instrument to list"
//	@Success		201		{object}	emiten.EmitenView
//	@Failure		400		{object}	httpx.ErrorResponse	"Missing kode/nama, or listed_shares <= 0"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		409		{object}	httpx.ErrorResponse	"Instrument code already exists"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/emiten [post]
func (c *Controller) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	e, err := c.svc.Create(r.Context(), req)
	if err != nil {
		c.writeError(w, err, req.Kode)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ToEmitenView(e))
}

// Detail handles GET /api/participant/emiten/{kode}.
//
//	@Summary		Get instrument detail
//	@Description	Master data plus all-time price statistics and derived valuations.
//	@Description
//	@Description	current_price, highest_price, lowest_price and value are **null**, not 0,
//	@Description	for an instrument that has never traded — no price exists yet, which is
//	@Description	not the same as a price of zero.
//	@Description
//	@Description	value is market capitalisation (current price × total shares), derived on
//	@Description	read rather than stored, so it always reflects the latest trade.
//	@Description	free_float_percentage is the publicly tradeable share of the total and
//	@Description	always sums to 100 with unlisted_percentage.
//	@Tags			participant
//	@ID				getEmitenDetail
//	@Produce		json
//	@Param			kode	path		string	true	"Emiten code"	example(BBCA)
//	@Success		200		{object}	emiten.EmitenDetail
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Failure		404		{object}	httpx.ErrorResponse	"Unknown emiten"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/emiten/{kode} [get]
func (c *Controller) Detail(w http.ResponseWriter, r *http.Request) {
	c.detail(w, r)
}

// AdminDetail handles GET /api/admin/emiten/{kode}.
//
// The same view as the participant detail, behind the admin key: the admin list
// omits price statistics because computing them per row costs a query per
// instrument, so this is where an operator reads them for one instrument.
//
//	@Summary		Get instrument detail (admin)
//	@Description	The same instrument detail the participant tier reads, on the admin key —
//	@Description	so oversight does not require holding a broker's credential. The admin list
//	@Description	omits price statistics — computing them per row would be a query per
//	@Description	instrument — so this endpoint is where they are read.
//	@Description
//	@Description	current_price, highest_price and lowest_price are strictly trade-derived and
//	@Description	are **null**, not 0, for an instrument that has never traded. reference_price
//	@Description	is what the instrument is valued at and falls back to ipo_price until the
//	@Description	first trade; price_source names which of the two it came from.
//	@Description
//	@Description	value is market capitalisation (reference price × total shares), derived on
//	@Description	read rather than stored, so it always reflects the latest trade.
//	@Description	free_float_percentage is the publicly tradeable share of the total and
//	@Description	always sums to 100 with unlisted_percentage.
//	@Tags			admin
//	@ID				getAdminEmitenDetail
//	@Produce		json
//	@Param			kode	path		string	true	"Emiten code"	example(BBCA)
//	@Success		200		{object}	emiten.EmitenDetail
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404		{object}	httpx.ErrorResponse	"Unknown emiten"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/emiten/{kode} [get]
func (c *Controller) AdminDetail(w http.ResponseWriter, r *http.Request) {
	c.detail(w, r)
}

// detail serves the instrument detail view. Both tiers return the same payload —
// master data is not confidential and the price statistics are the same numbers —
// so the handler is shared and only the guarding middleware differs. Keeping one
// body means the two views cannot drift apart as fields are added.
func (c *Controller) detail(w http.ResponseWriter, r *http.Request) {
	kode := r.PathValue("kode")

	e, stats, err := c.svc.Detail(r.Context(), kode)
	if err != nil {
		c.writeError(w, err, kode)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToEmitenDetail(e, stats))
}

// writeError maps a service error onto the response.
func (c *Controller) writeError(w http.ResponseWriter, err error, kode string) {
	var invalid ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "unknown emiten: "+kode)
	case errors.As(err, &invalid):
		httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
	case c.duplicate != nil && c.duplicate(err):
		httpx.WriteError(w, http.StatusConflict, "emiten already exists: "+kode)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "emiten request failed")
	}
}
