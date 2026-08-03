package corporateaction

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the admin surface for corporate actions.
type Controller struct {
	svc   *Service
	codes Codes
}

// NewController returns a controller backed by svc.
func NewController(svc *Service, codes Codes) *Controller {
	return &Controller{svc: svc, codes: codes}
}

// List handles GET /api/admin/corporate-actions.
//
//	@Summary		List corporate actions
//	@Description	Every announced, executed and cancelled aksi korporasi, newest first.
//	@Description
//	@Description	Cancelled actions are listed rather than hidden: participants were told
//	@Description	about them, so they are part of the instrument's history.
//	@Description
//	@Description	Pass `kode` to scope the listing to one instrument. The per-broker
//	@Description	movements an executed action caused are on the detail endpoint, not here —
//	@Description	they are a row per holder and would dominate a listing.
//	@Tags			admin
//	@ID				listCorporateActions
//	@Produce		json
//	@Param			kode	query		string	false	"Filter by emiten code"	example(BBCA)
//	@Param			page	query		int		false	"Page number, from 1"	default(1)
//	@Param			limit	query		int		false	"Rows per page, max 100"	default(10)
//	@Success		200		{object}	httpx.Page[corporateaction.ActionView]
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404		{object}	httpx.ErrorResponse	"Unknown emiten"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/corporate-actions [get]
func (c *Controller) List(w http.ResponseWriter, r *http.Request) {
	page, limit := httpx.ParsePagination(r)
	kode := r.URL.Query().Get("kode")

	recs, total, err := c.svc.List(r.Context(), kode, page, limit)
	if err != nil {
		c.writeError(w, err, kode)
		return
	}
	httpx.WriteJSON(w, http.StatusOK,
		httpx.NewPage(c.codes.ToActionViews(recs), page, limit, total))
}

// Detail handles GET /api/admin/corporate-actions/{id}.
//
//	@Summary		Corporate action detail
//	@Description	One aksi korporasi and every ledger movement it caused: which broker held
//	@Description	how many shares before it, how many after, and how much cash it received.
//	@Description
//	@Description	`entries` is empty for an action that has not executed. That is the honest
//	@Description	answer rather than an error — the action exists and is readable, nobody has
//	@Description	simply received anything from it yet.
//	@Description
//	@Description	The share totals are summed from the entries, not read off the instrument,
//	@Description	so this keeps reporting what *this* action did even after later actions have
//	@Description	moved the instrument on again.
//	@Tags			admin
//	@ID				getCorporateAction
//	@Produce		json
//	@Param			id	path		int	true	"Corporate action id"	example(1)
//	@Success		200	{object}	corporateaction.ActionDetailView
//	@Failure		400	{object}	httpx.ErrorResponse	"Malformed id"
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404	{object}	httpx.ErrorResponse	"Unknown corporate action"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/corporate-actions/{id} [get]
func (c *Controller) Detail(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	rec, entries, err := c.svc.Detail(r.Context(), id)
	if err != nil {
		c.writeError(w, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c.codes.ToDetailView(rec, entries))
}

// Announce handles POST /api/admin/corporate-actions.
//
//	@Summary		Announce a corporate action
//	@Description	Records an aksi korporasi that will take effect later. **Nothing moves
//	@Description	here** — no holding is restated and no wallet is credited until
//	@Description	POST /api/admin/corporate-actions/{id}/execute is called. That gap is the
//	@Description	point: an announced action can still be cancelled, and an executed one
//	@Description	cannot.
//	@Description
//	@Description	Four kinds, and the terms you send depend on which:
//	@Description	- **SPLIT** — `ratio_from`:`ratio_to`, e.g. 1:2 turns one share into two.
//	@Description	`ratio_to` must be greater than `ratio_from`.
//	@Description	- **REVERSE_SPLIT** — e.g. 2:1 turns two shares into one. `ratio_to` must
//	@Description	be *less* than `ratio_from`.
//	@Description	- **BONUS** — new shares issued free to holders. 2:3 hands the holder of
//	@Description	two shares a third.
//	@Description	- **DIVIDEND** — `amount` in rupiah per share held. No ratio.
//	@Description
//	@Description	Sending the wrong terms for the kind is rejected rather than ignored: a
//	@Description	dividend carrying a ratio means something was misunderstood, and accepting
//	@Description	it silently would carry that through to execution.
//	@Description
//	@Description	The instrument must already be trading. An action over a dormant one would
//	@Description	restate holdings nobody has.
//	@Tags			admin
//	@ID				announceCorporateAction
//	@Accept			json
//	@Produce		json
//	@Param			body	body		corporateaction.AnnounceRequest	true	"Action to announce"
//	@Success		201		{object}	corporateaction.ActionView
//	@Failure		400		{object}	httpx.ErrorResponse	"Bad terms for the kind, or emiten not trading"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404		{object}	httpx.ErrorResponse	"Unknown emiten"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/corporate-actions [post]
func (c *Controller) Announce(w http.ResponseWriter, r *http.Request) {
	var req AnnounceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	rec, err := c.svc.Announce(r.Context(), req)
	if err != nil {
		c.writeError(w, err, req.Kode)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c.codes.ToActionView(rec))
}

// Execute handles POST /api/admin/corporate-actions/{id}/execute.
//
//	@Summary		Execute a corporate action
//	@Description	Applies an announced action to the ledgers. **This cannot be undone.**
//	@Description
//	@Description	A SPLIT, REVERSE_SPLIT or BONUS restates every holder's position by the
//	@Description	ratio and the instrument's `listed_shares` with it, in one transaction —
//	@Description	an instrument whose share count disagreed with the sum of its holders'
//	@Description	positions would make every valuation derived from it wrong. The price band's
//	@Description	anchor is restated by the inverse ratio at the same time, because the
//	@Description	instrument is worth the same immediately after a split as immediately
//	@Description	before it; an anchor left alone would auto-reject every order at the new
//	@Description	fair value.
//	@Description
//	@Description	A DIVIDEND credits each holder `amount` × shares held, and restates
//	@Description	nothing.
//	@Description
//	@Description	Shares are indivisible, so a ratio that does not divide a holding evenly
//	@Description	truncates — a 2:3 bonus on an odd holding drops the half share rather than
//	@Description	issuing one the action never authorised. Holdings are read as they stand at
//	@Description	execution; `record_date` is informational, since the engine keeps no
//	@Description	end-of-day snapshot.
//	@Description
//	@Description	Only an ANNOUNCED action can be executed. A second attempt is a 409, which
//	@Description	is what stops an action doubling every holding twice.
//	@Tags			admin
//	@ID				executeCorporateAction
//	@Produce		json
//	@Param			id	path		int	true	"Corporate action id"	example(1)
//	@Success		200	{object}	corporateaction.ActionDetailView
//	@Failure		400	{object}	httpx.ErrorResponse	"Malformed id, or the instrument has no holders"
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404	{object}	httpx.ErrorResponse	"Unknown corporate action"
//	@Failure		409	{object}	httpx.ErrorResponse	"Already executed or cancelled"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/corporate-actions/{id}/execute [post]
func (c *Controller) Execute(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	rec, entries, err := c.svc.Execute(r.Context(), id)
	if err != nil {
		c.writeError(w, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c.codes.ToDetailView(rec, entries))
}

// Cancel handles POST /api/admin/corporate-actions/{id}/cancel.
//
//	@Summary		Cancel a corporate action
//	@Description	Abandons an announced action before it takes effect.
//	@Description
//	@Description	The row is kept, not deleted: participants were told about the action, so
//	@Description	its cancellation is part of the instrument's history, and removing it would
//	@Description	leave the record disagreeing with what the market was told.
//	@Description
//	@Description	Only an ANNOUNCED action can be cancelled — an executed one has already
//	@Description	moved every holder's ledger, and there is no undoing that from here.
//	@Tags			admin
//	@ID				cancelCorporateAction
//	@Produce		json
//	@Param			id	path		int	true	"Corporate action id"	example(1)
//	@Success		200	{object}	corporateaction.ActionView
//	@Failure		400	{object}	httpx.ErrorResponse	"Malformed id"
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404	{object}	httpx.ErrorResponse	"Unknown corporate action"
//	@Failure		409	{object}	httpx.ErrorResponse	"Already executed or cancelled"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/corporate-actions/{id}/cancel [post]
func (c *Controller) Cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}

	rec, err := c.svc.Cancel(r.Context(), id)
	if err != nil {
		c.writeError(w, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c.codes.ToActionView(rec))
}

// parseID reads the {id} path value, writing a 400 and reporting false when it is
// not a positive integer.
func parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, "id must be a positive integer")
		return 0, false
	}
	return id, true
}

// writeError maps a service error onto the response.
func (c *Controller) writeError(w http.ResponseWriter, err error, kode string) {
	var invalid ValidationError
	switch {
	case errors.Is(err, ErrEmitenNotFound):
		httpx.WriteError(w, http.StatusNotFound, "unknown emiten: "+kode)
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, "unknown corporate action")
	case errors.Is(err, ErrNotAnnounced):
		httpx.WriteError(w, http.StatusConflict,
			"corporate action is already executed or cancelled")
	case errors.As(err, &invalid):
		httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "corporate action request failed")
	}
}
