package wallet

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"bekasi-automatic-trading-system/participant"
	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the HTTP surface for broker cash balances.
type Controller struct {
	svc   *Service
	codes Codes
}

// NewController returns a controller backed by svc.
func NewController(svc *Service, codes Codes) *Controller {
	return &Controller{svc: svc, codes: codes}
}

// Mine handles GET /api/participant/wallet.
//
//	@Summary		Own cash balance
//	@Description	The authenticated broker's current cash balance.
//	@Description
//	@Description	The broker is taken from the API key and this endpoint accepts no
//	@Description	`participant` parameter, so one broker cannot read another's balance.
//	@Tags			participant
//	@ID				myWallet
//	@Produce		json
//	@Success		200	{object}	wallet.WalletView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/wallet [get]
func (c *Controller) Mine(w http.ResponseWriter, r *http.Request) {
	id, ok := participant.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	rec, err := c.svc.Mine(r.Context(), id.ID)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "wallet request failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c.codes.ToWalletView(rec))
}

// All handles GET /api/admin/wallets.
//
//	@Summary		Cash balances across brokers
//	@Description	Every broker's cash balance, or one broker's when `participant` is given.
//	@Tags			admin
//	@ID				listWallets
//	@Produce		json
//	@Param			participant	query		string	false	"Filter by broker code"		example(YP)
//	@Param			page		query		int		false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit		query		int		false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200			{object}	httpx.Page[wallet.WalletView]
//	@Failure		401			{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404			{object}	httpx.ErrorResponse	"Unknown participant"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/wallets [get]
func (c *Controller) All(w http.ResponseWriter, r *http.Request) {
	var participantID *int64

	if kode := strings.TrimSpace(r.URL.Query().Get("participant")); kode != "" {
		id, err := c.svc.ResolveParticipant(kode)
		if err != nil {
			httpx.WriteError(w, http.StatusNotFound, "unknown participant: "+kode)
			return
		}
		participantID = &id
	}

	page, limit := httpx.ParsePagination(r)

	records, total, err := c.svc.List(r.Context(), participantID, page, limit)
	if err != nil {
		if errors.Is(err, ErrParticipantNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "unknown participant")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "wallet request failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK,
		httpx.NewPage(c.codes.ToWalletViews(records), page, limit, total))
}

// Adjust handles POST /api/admin/wallets.
//
//	@Summary		Fund or debit a broker's wallet
//	@Description	Moves cash into or out of one broker's balance and returns the balance it
//	@Description	settles at. A positive `amount` credits the broker, a negative one debits
//	@Description	it; `0` is rejected.
//	@Description
//	@Description	Admin-only by design: a broker that could credit its own wallet could buy
//	@Description	without limit, so funding is an exchange operation.
//	@Description
//	@Description	A broker registered but never funded has no wallet row yet. This endpoint
//	@Description	creates it, so it opens a wallet and fills it in one call.
//	@Description
//	@Description	A debit is checked against **available** cash, not the stored balance:
//	@Description	cash already committed to resting buy orders cannot be withdrawn, or
//	@Description	those orders would be left unfunded when they fill. The rejection names
//	@Description	the most that may be withdrawn.
//	@Tags			admin
//	@ID				adjustWallet
//	@Accept			json
//	@Produce		json
//	@Param			body	body		wallet.AdjustRequest	true	"Adjustment to apply"
//	@Success		200		{object}	wallet.WalletView		"The balance after the adjustment"
//	@Failure		400		{object}	httpx.ErrorResponse		"Missing participant, amount of 0, or a debit beyond available cash"
//	@Failure		401		{object}	httpx.ErrorResponse		"Missing or wrong X-API-Key"
//	@Failure		404		{object}	httpx.ErrorResponse		"Unknown participant"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/wallets [post]
func (c *Controller) Adjust(w http.ResponseWriter, r *http.Request) {
	var req AdjustRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	rec, err := c.svc.Adjust(r.Context(), req)
	if err != nil {
		var invalid ValidationError
		switch {
		case errors.Is(err, ErrParticipantNotFound):
			httpx.WriteError(w, http.StatusNotFound,
				"unknown participant: "+strings.TrimSpace(req.Participant))
		case errors.As(err, &invalid):
			httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
		default:
			httpx.WriteError(w, http.StatusInternalServerError, "wallet request failed")
		}
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c.codes.ToWalletView(rec))
}
