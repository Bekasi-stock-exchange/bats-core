package underwriter

import (
	"encoding/json"
	"errors"
	"net/http"

	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the admin surface for underwriters and offerings.
type Controller struct {
	svc   *Service
	codes Codes
	// duplicate reports whether an error is a unique-constraint violation,
	// injected so this package needs no dependency on pgx error codes.
	duplicate func(error) bool
}

// NewController returns a controller backed by svc.
func NewController(svc *Service, codes Codes, isDuplicate func(error) bool) *Controller {
	return &Controller{svc: svc, codes: codes, duplicate: isDuplicate}
}

// List handles GET /api/admin/underwriters.
//
//	@Summary		List underwriters
//	@Description	Every registered penjamin emisi, ordered by code.
//	@Description
//	@Description	jenis is the role: **utama** is the lead underwriter that guarantees an
//	@Description	offering, **pendukung** a supporting syndicate member taking a smaller
//	@Description	tranche. participant is the broker the firm trades through — allocated
//	@Description	shares land in that broker's holdings, because only a participant can
//	@Description	trade them.
//	@Tags			admin
//	@ID				listUnderwriters
//	@Produce		json
//	@Success		200	{array}		underwriter.UnderwriterView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/underwriters [get]
func (c *Controller) List(w http.ResponseWriter, r *http.Request) {
	recs, err := c.svc.List(r.Context())
	if err != nil {
		c.writeError(w, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c.codes.ToUnderwriterViews(recs))
}

// Create handles POST /api/admin/underwriters.
//
//	@Summary		Register an underwriter
//	@Description	Registers a penjamin emisi against an existing broker. The broker is its
//	@Description	trading identity: IPO allocations credit that participant's holdings, so an
//	@Description	underwriter without one could be handed shares it could never sell.
//	@Tags			admin
//	@ID				createUnderwriter
//	@Accept			json
//	@Produce		json
//	@Param			body	body		underwriter.CreateRequest	true	"Underwriter to register"
//	@Success		201		{object}	underwriter.UnderwriterView
//	@Failure		400		{object}	httpx.ErrorResponse	"Missing field, bad jenis, or unknown participant"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		409		{object}	httpx.ErrorResponse	"Underwriter code already exists"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/underwriters [post]
func (c *Controller) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	rec, err := c.svc.Create(r.Context(), req)
	if err != nil {
		c.writeError(w, err, req.Kode)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c.codes.ToUnderwriterView(rec))
}

// IPO handles POST /api/admin/ipo.
//
//	@Summary		Run an IPO
//	@Description	Lists an instrument and hands its shares to the underwriting syndicate in
//	@Description	one step — an instrument whose shares sit nowhere is not yet an offering.
//	@Description
//	@Description	The syndicate rules encode what the two roles mean:
//	@Description	- exactly **one** underwriter with jenis `utama` (the lead that guarantees it)
//	@Description	- the lead's tranche must be the **largest**; `pendukung` means a smaller portion
//	@Description	- the tranches must sum to **exactly** listed_shares — a short sum leaves
//	@Description	shares in nobody's hands, an over-sum conjures shares that were never issued
//	@Description
//	@Description	ipo_price becomes the instrument's reference price until it first trades, so
//	@Description	it has a valuation and something to quote against from the moment it lists.
//	@Description	The allocation is one transaction: the audit rows and the share credits move
//	@Description	together or not at all.
//	@Tags			admin
//	@ID				runIPO
//	@Accept			json
//	@Produce		json
//	@Param			body	body		underwriter.IPORequest	true	"Offering to run"
//	@Success		201		{object}	underwriter.IPOResponse
//	@Failure		400		{object}	httpx.ErrorResponse	"Bad syndicate, shares do not sum, or unknown underwriter"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		409		{object}	httpx.ErrorResponse	"Instrument code already exists"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/ipo [post]
func (c *Controller) IPO(w http.ResponseWriter, r *http.Request) {
	var req IPORequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	e, allocs, err := c.svc.IPO(r.Context(), req)
	if err != nil {
		c.writeError(w, err, req.Kode)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ToIPOResponse(e, allocs))
}

// writeError maps a service error onto the response.
func (c *Controller) writeError(w http.ResponseWriter, err error, kode string) {
	var invalid ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "unknown underwriter: "+kode)
	case errors.As(err, &invalid):
		httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
	case c.duplicate != nil && c.duplicate(err):
		httpx.WriteError(w, http.StatusConflict, "already exists: "+kode)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "underwriter request failed")
	}
}
