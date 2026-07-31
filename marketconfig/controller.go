package marketconfig

import (
	"encoding/json"
	"errors"
	"net/http"

	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the admin surface for the exchange's trading parameters.
type Controller struct {
	svc *Service
}

// NewController returns a controller backed by svc.
func NewController(svc *Service) *Controller { return &Controller{svc: svc} }

// Get handles GET /api/admin/config.
//
//	@Summary		Read trading parameters
//	@Description	The exchange-wide rules in force right now, as the order path is
//	@Description	actually enforcing them.
//	@Description
//	@Description	**min_price** is the lowest price a limit order may carry, in rupiah.
//	@Description	It defaults to **50**, matching BEI's own minimum quotable price. Without
//	@Description	it the only price rule is `price > 0`, which lets a seller quote 58, then
//	@Description	5, then 1 — each one accepted, and each one resting in the book as the
//	@Description	best ask for whatever arrives next.
//	@Description
//	@Description	The remaining fields configure the circuit breakers. **emiten_halt_bps**
//	@Description	(default **3000** = 30%) is how far one emiten may move from its reference
//	@Description	price before trading in it halts; **index_halt_bps** (default **1200** =
//	@Description	12%) is how far the index may fall from its opening value before trading
//	@Description	halts market-wide; **halt_duration_seconds** (default **120**) is how long
//	@Description	either halt lasts.
//	@Description
//	@Description	Thresholds are in basis points — 1/100th of a percent, so 3000 is 30% —
//	@Description	because a threshold compared against a rupiah price stays in integer
//	@Description	arithmetic that way, with no rounding at the boundary that decides whether
//	@Description	a breaker trips.
//	@Tags			admin
//	@ID				getMarketConfig
//	@Produce		json
//	@Success		200	{object}	marketconfig.ConfigView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/config [get]
func (c *Controller) Get(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, ToConfigView(c.svc.Current()))
}

// Update handles PUT /api/admin/config.
//
//	@Summary		Update trading parameters
//	@Description	Changes the exchange-wide trading rules. The new value is persisted and
//	@Description	takes effect on the very next order — no restart, and it survives one.
//	@Description
//	@Description	Fields left out of the body keep their current value, so one parameter can
//	@Description	be changed without restating the rest. min_price must be greater than 0: a
//	@Description	floor of 0 is not a policy, it is the absence of the rule.
//	@Description
//	@Description	The halt thresholds must be between 1 and 10000 basis points, and the halt
//	@Description	duration between 1 and 86400 seconds. Both ends are enforced. A threshold
//	@Description	of 0 does not disable a breaker, it arms one that trips on the first trade
//	@Description	of the session; a threshold above 100% can never trip, leaving a breaker
//	@Description	that reads as protection and provides none.
//	@Tags			admin
//	@ID				updateMarketConfig
//	@Accept			json
//	@Produce		json
//	@Param			body	body		marketconfig.UpdateRequest	true	"Parameters to change"
//	@Success		200		{object}	marketconfig.ConfigView
//	@Failure		400		{object}	httpx.ErrorResponse	"A parameter is outside its permitted range"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/config [put]
func (c *Controller) Update(w http.ResponseWriter, r *http.Request) {
	var req UpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	s, err := c.svc.Update(r.Context(), req)
	if err != nil {
		var invalid ValidationError
		if errors.As(err, &invalid) {
			httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "config update failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToConfigView(s))
}
