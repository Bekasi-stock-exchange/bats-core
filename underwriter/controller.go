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

func NewController(svc *Service, codes Codes, isDuplicate func(error) bool) *Controller {
	return &Controller{svc: svc, codes: codes, duplicate: isDuplicate}
}

//	@Summary		List underwriters
//	@Description	Every broker permitted to underwrite offerings, ordered by code.
//	@Description
//	@Description	An underwriter is a participant with a permission, not a firm of its own,
//	@Description	so kode and nama are that participant's — `kode` and `participant` are
//	@Description	always the same value. Allocated shares land in that broker's holdings,
//	@Description	because only a participant can trade them.
//	@Description
//	@Description	Members carry no rank: every underwriter in a syndicate is on equal terms.
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

//	@Summary		Permit a broker to underwrite
//	@Description	Grants an existing broker the right to underwrite offerings. That is all an
//	@Description	underwriter is — a participant with a permission — so the request carries
//	@Description	only the broker code: its name and code are the participant's already, and
//	@Description	are joined on read rather than stored twice.
//	@Description
//	@Description	The broker is also its trading identity: IPO allocations credit that
//	@Description	participant's holdings, so an underwriter without one could be handed
//	@Description	shares it could never sell.
//	@Description
//	@Description	A broker may be registered once. Registering it again is a 409.
//	@Tags			admin
//	@ID				createUnderwriter
//	@Accept			json
//	@Produce		json
//	@Param			body	body		underwriter.CreateRequest	true	"Broker to permit"
//	@Success		201		{object}	underwriter.UnderwriterView
//	@Failure		400		{object}	httpx.ErrorResponse	"Missing participant, or unknown participant"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		409		{object}	httpx.ErrorResponse	"Broker is already an underwriter"
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
		c.writeError(w, err, req.Participant)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c.codes.ToUnderwriterView(rec))
}

//	@Summary		Run an IPO
//	@Description	Lists an instrument and hands its shares to the underwriting syndicate in
//	@Description	one step — an instrument whose shares sit nowhere is not yet an offering.
//	@Description
//	@Description	The syndicate is flat — every member takes a tranche on equal terms:
//	@Description	- each `underwriter` is a **broker code** that has been permitted to underwrite
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

//	@Summary		Run an IPO over a registered instrument
//	@Description	Takes an instrument that was registered through POST /api/admin/emiten —
//	@Description	and is therefore still **dormant** — public: it hands the shares to the
//	@Description	syndicate and opens the order book. This is the only way an instrument
//	@Description	becomes tradeable.
//	@Description
//	@Description	The body carries no share counts. listed_shares and unlisted_shares are the
//	@Description	instrument's own, fixed when it was registered — an offering decides who
//	@Description	underwrites it and at what price, not how many shares the company has. The
//	@Description	tranches must sum to exactly the listed_shares it already carries.
//	@Description
//	@Description	The syndicate rules are the same as POST /api/admin/ipo:
//	@Description	- each `underwriter` is a **broker code** that has been permitted to underwrite
//	@Description	- the tranches must sum to **exactly** the instrument's listed_shares
//	@Description	- every member is on equal terms; there is no lead
//	@Description
//	@Description	ipo_price becomes the instrument's reference price until it first trades. An
//	@Description	instrument that is already trading is rejected — a second offering would
//	@Description	issue its shares twice.
//	@Tags			admin
//	@ID				runIPOForEmiten
//	@Accept			json
//	@Produce		json
//	@Param			kode	path		string							true	"Emiten code"	example(BBNI)
//	@Param			body	body		underwriter.ExistingIPORequest	true	"Offering to run"
//	@Success		201		{object}	underwriter.IPOResponse
//	@Failure		400		{object}	httpx.ErrorResponse	"Bad syndicate, shares do not sum, unknown underwriter, or emiten already trading"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404		{object}	httpx.ErrorResponse	"Unknown emiten"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/emiten/{kode}/ipo [post]
func (c *Controller) IPOExisting(w http.ResponseWriter, r *http.Request) {
	kode := r.PathValue("kode")

	var req ExistingIPORequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	e, allocs, err := c.svc.IPOExisting(r.Context(), kode, req)
	if err != nil {
		c.writeError(w, err, kode)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ToIPOResponse(e, allocs))
}

func (c *Controller) writeError(w http.ResponseWriter, err error, kode string) {
	var invalid ValidationError
	switch {
	case errors.Is(err, ErrEmitenNotFound):
		httpx.WriteError(w, http.StatusNotFound, "unknown emiten: "+kode)
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
