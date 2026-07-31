package index

import (
	"errors"
	"net/http"
	"time"

	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the participant-facing surface for the composite index.
type Controller struct {
	svc *Service
}

// NewController returns a controller backed by svc.
func NewController(svc *Service) *Controller { return &Controller{svc: svc} }

// Current handles GET /api/participant/index.
//
//	@Summary		Read the composite index
//	@Description	The exchange-wide price level, computed IHSG-style: the summed
//	@Description	free-float market capitalisation of every listed instrument, divided by
//	@Description	a divisor and scaled to a base of 100.
//	@Description
//	@Description	Each instrument is valued at its last traded price, falling back to its
//	@Description	IPO price until it has traded — the same reference price the emiten
//	@Description	detail endpoint reports. An instrument with neither is excluded rather
//	@Description	than counted as zero, which is why **members** may be lower than
//	@Description	**total**.
//	@Description
//	@Description	Weighting is by listed (free-float) shares, not total shares outstanding,
//	@Description	matching BEI's own methodology since 2021.
//	@Description
//	@Description	**divisor** is restated whenever an instrument lists, so the level does
//	@Description	not jump on an event where no price moved. It is returned so a client can
//	@Description	verify the level rather than trust it.
//	@Tags			participant
//	@ID				getIndex
//	@Produce		json
//	@Success		200	{object}	index.IndexView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Failure		503	{object}	httpx.ErrorResponse	"Index has not been computed yet"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/index [get]
func (c *Controller) Current(w http.ResponseWriter, r *http.Request) {
	l, err := c.svc.Current()
	if err != nil {
		if errors.Is(err, ErrNoLevel) {
			// 503, not 404: the index exists, it just has no value yet. A client
			// should retry rather than conclude the resource is absent.
			httpx.WriteError(w, http.StatusServiceUnavailable, "index not computed yet")
			return
		}
		httpx.WriteError(w, http.StatusInternalServerError, "index read failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToIndexView(l))
}

// History handles GET /api/participant/index/history.
//
//	@Summary		Read the composite index history
//	@Description	Past index levels, newest first. Each point carries the market
//	@Description	capitalisation and divisor that produced it, so an old level stays
//	@Description	verifiable even after the divisor has since been restated.
//	@Description
//	@Description	Optional **from** and **to** bound the range, both RFC 3339 (for example
//	@Description	`2026-07-30T00:00:00Z`). An unparseable value is rejected with 400 rather
//	@Description	than silently ignored, since a filter that quietly does nothing returns a
//	@Description	page the caller will misread as the whole range.
//	@Tags			participant
//	@ID				getIndexHistory
//	@Produce		json
//	@Param			from	query		string	false	"Start of range, RFC 3339"	example(2026-07-30T00:00:00Z)
//	@Param			to		query		string	false	"End of range, RFC 3339"	example(2026-07-31T00:00:00Z)
//	@Param			page	query		int		false	"Page number, from 1"		default(1)
//	@Param			limit	query		int		false	"Rows per page, max 100"	default(10)
//	@Success		200		{object}	httpx.Page[index.SnapshotView]
//	@Failure		400		{object}	httpx.ErrorResponse	"from or to is not RFC 3339"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-Participant-Key"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/index/history [get]
func (c *Controller) History(w http.ResponseWriter, r *http.Request) {
	from, err := parseTime(r, "from")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "from must be RFC 3339")
		return
	}
	to, err := parseTime(r, "to")
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "to must be RFC 3339")
		return
	}
	if from != nil && to != nil && to.Before(*from) {
		httpx.WriteError(w, http.StatusBadRequest, "to must not be before from")
		return
	}

	page, limit := httpx.ParsePagination(r)

	snaps, total, err := c.svc.History(r.Context(), from, to, page, limit)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "index history failed")
		return
	}
	httpx.WriteJSON(w, http.StatusOK,
		httpx.NewPage(ToSnapshotViews(snaps), page, limit, total))
}

// parseTime reads an optional RFC 3339 query parameter. An absent parameter is
// nil with no error; a present but malformed one is an error, so a typo becomes
// a 400 rather than a silently unfiltered page.
func parseTime(r *http.Request, key string) (*time.Time, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
