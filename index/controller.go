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
	c.current(w, r)
}

// AdminCurrent handles GET /api/admin/index.
//
//	@Summary		Read the composite index (admin)
//	@Description	Identical to the participant view: the index is one number the whole
//	@Description	exchange shares, and there is no broker-specific version of it. Offered
//	@Description	on the admin tier so an operator can read it with the key it already
//	@Description	holds, rather than having to issue itself a broker key.
//	@Tags			admin
//	@ID				getAdminIndex
//	@Produce		json
//	@Success		200	{object}	index.IndexView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		503	{object}	httpx.ErrorResponse	"Index has not been computed yet"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/index [get]
func (c *Controller) AdminCurrent(w http.ResponseWriter, r *http.Request) {
	c.current(w, r)
}

// current serves the index level. Both tiers return the same payload — the index
// is a single exchange-wide number, not a per-broker one — so the handler is
// shared and only the guarding middleware differs. Keeping one body means the
// two views cannot drift apart as fields are added.
func (c *Controller) current(w http.ResponseWriter, r *http.Request) {
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
	c.history(w, r)
}

// AdminHistory handles GET /api/admin/index/history.
//
//	@Summary		Read the composite index history (admin)
//	@Description	Identical to the participant view — the same series, offered on the
//	@Description	admin tier so an operator can read it with the key it already holds.
//	@Tags			admin
//	@ID				getAdminIndexHistory
//	@Produce		json
//	@Param			from	query		string	false	"Start of range, RFC 3339"	example(2026-07-30T00:00:00Z)
//	@Param			to		query		string	false	"End of range, RFC 3339"	example(2026-07-31T00:00:00Z)
//	@Param			page	query		int		false	"Page number, from 1"		default(1)
//	@Param			limit	query		int		false	"Rows per page, max 100"	default(10)
//	@Success		200		{object}	httpx.Page[index.SnapshotView]
//	@Failure		400		{object}	httpx.ErrorResponse	"from or to is not RFC 3339"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/index/history [get]
func (c *Controller) AdminHistory(w http.ResponseWriter, r *http.Request) {
	c.history(w, r)
}

// history serves the index series. Shared between both tiers for the same reason
// as current: one index, one payload, only the guarding middleware differs.
func (c *Controller) history(w http.ResponseWriter, r *http.Request) {
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

// Recompute handles POST /api/admin/index/recompute.
//
//	@Summary		Recompute the index now
//	@Description	Revalues the whole market immediately and refreshes the published level,
//	@Description	instead of waiting for the next trade to trigger it.
//	@Description
//	@Description	This exists for the case where the level is stale through no fault of the
//	@Description	market: a price read failed, or an instrument was listed while the index
//	@Description	could not reach the database. Ordinary operation needs no such call — the
//	@Description	index recomputes on every execution by itself.
//	@Description
//	@Description	Admin-only because it is an exchange operation, and because valuing the
//	@Description	entire market is real work that no broker should be able to schedule at
//	@Description	will.
//	@Tags			admin
//	@ID				recomputeIndex
//	@Produce		json
//	@Success		200	{object}	index.IndexView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		500	{object}	httpx.ErrorResponse	"Recompute failed"
//	@Failure		503	{object}	httpx.ErrorResponse	"Nothing could be priced"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/index/recompute [post]
func (c *Controller) Recompute(w http.ResponseWriter, r *http.Request) {
	if err := c.svc.Recompute(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "index recompute failed")
		return
	}

	// Recompute leaves the previous level standing when nothing could be priced,
	// so a 200 here would report success while publishing a stale number. Current
	// is what distinguishes the two.
	l, err := c.svc.Current()
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "index not computed yet")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToIndexView(l))
}

// Capture handles POST /api/admin/index/capture.
//
//	@Summary		Record a history point now
//	@Description	Writes the current level into the index history immediately, rather than
//	@Description	waiting for the next automatic capture.
//	@Description
//	@Description	Useful for marking a specific moment — the end of a session, or the state
//	@Description	just before an intervention — without depending on where the periodic
//	@Description	capture happens to fall.
//	@Description
//	@Description	Returns the level that was recorded. If nothing has been computed yet,
//	@Description	nothing is written and 503 is returned rather than an empty success.
//	@Tags			admin
//	@ID				captureIndex
//	@Produce		json
//	@Success		201	{object}	index.IndexView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Failure		500	{object}	httpx.ErrorResponse	"Capture failed"
//	@Failure		503	{object}	httpx.ErrorResponse	"Index has not been computed yet"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/index/capture [post]
func (c *Controller) Capture(w http.ResponseWriter, r *http.Request) {
	// Checked before the write so an uncomputed index is a clean 503 rather than
	// a 201 reporting a capture that Capture silently skipped.
	l, err := c.svc.Current()
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, "index not computed yet")
		return
	}

	if err := c.svc.Capture(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, "index capture failed")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ToIndexView(l))
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
