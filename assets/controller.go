package assets

import (
	"errors"
	"net/http"
	"strings"

	"bekasi-automatic-trading-system/participant"
	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the HTTP surface for broker holdings.
type Controller struct {
	svc   *Service
	codes Codes
}

// NewController returns a controller backed by svc.
func NewController(svc *Service, codes Codes) *Controller {
	return &Controller{svc: svc, codes: codes}
}

// Mine handles GET /api/participant/assets.
//
//	@Summary		Own share holdings
//	@Description	Every instrument the authenticated broker holds, with the current market
//	@Description	value of each.
//	@Description
//	@Description	The broker is taken from the API key and this endpoint accepts no
//	@Description	`participant` parameter, so one broker cannot read another's positions.
//	@Description
//	@Description	value is derived from the latest trade price rather than stored, so it is
//	@Description	current even when this broker has not traded. It is null when the
//	@Description	instrument has never traded.
//	@Tags			participant
//	@ID				myAssets
//	@Produce		json
//	@Param			page	query		int	false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit	query		int	false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200		{object}	httpx.Page[assets.HoldingView]
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/assets [get]
func (c *Controller) Mine(w http.ResponseWriter, r *http.Request) {
	id, ok := participant.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	c.write(w, r, &id.ID)
}

// All handles GET /api/admin/assets.
//
//	@Summary		Share holdings across brokers
//	@Description	Every broker's holdings, or one broker's when `participant` is given.
//	@Tags			admin
//	@ID				listAssets
//	@Produce		json
//	@Param			participant	query		string	false	"Filter by broker code"		example(YP)
//	@Param			page		query		int		false	"Page number, from 1"		default(1)	minimum(1)
//	@Param			limit		query		int		false	"Items per page, max 100"	default(10)	minimum(1)	maximum(100)
//	@Success		200			{object}	httpx.Page[assets.HoldingView]
//	@Failure		401			{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		404			{object}	httpx.ErrorResponse	"Unknown participant"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/assets [get]
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
	c.write(w, r, participantID)
}

func (c *Controller) write(w http.ResponseWriter, r *http.Request, participantID *int64) {
	page, limit := httpx.ParsePagination(r)

	records, total, err := c.svc.List(r.Context(), participantID, page, limit)
	if err != nil {
		if errors.Is(err, ErrParticipantNotFound) {
			httpx.WriteError(w, http.StatusNotFound, "unknown participant")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "assets request failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK,
		httpx.NewPage(c.codes.ToHoldingViews(records), page, limit, total))
}
