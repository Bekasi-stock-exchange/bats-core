package orderbook

import (
	"errors"
	"net/http"

	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the HTTP surface of the read side. It decodes the request, calls
// the service, and writes the transformed result — nothing else.
type Controller struct {
	svc *Service
}

func NewController(svc *Service) *Controller {
	return &Controller{svc: svc}
}

//	@Summary		List order books
//	@Description	Returns a paginated list of the current order books, ordered by emiten code.
//	@Tags			participant
//	@ID				getOrderBooks
//	@Produce		json
//	@Param			page	query		int	false	"Page number, from 1"			default(1)	minimum(1)
//	@Param			limit	query		int	false	"Items per page, max 100"		default(10)	minimum(1)	maximum(100)
//	@Success		200		{object}	httpx.Page[orderbook.BookSnapshot]	"A page of order books"
//	@Failure		401		{object}	httpx.ErrorResponse					"Missing or wrong X-Participant-Key"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/orderbook [get]
func (c *Controller) List(w http.ResponseWriter, r *http.Request) {
	page, limit := httpx.ParsePagination(r)

	states, total := c.svc.Books(page, limit)
	httpx.WriteJSON(w, http.StatusOK, httpx.NewPage(ToBookSnapshots(states), page, limit, total))
}

//	@Summary		Get one order book
//	@Description	Returns the book aggregated by price level. Bids are highest price
//	@Description	first; asks are lowest price first.
//	@Tags			participant
//	@ID				getOrderBook
//	@Produce		json
//	@Param			kode	path		string					true	"Emiten code"	example(BBCA)
//	@Success		200		{object}	orderbook.BookSnapshot	"Current book state"
//	@Failure		401		{object}	httpx.ErrorResponse		"Missing or wrong X-Participant-Key"
//	@Failure		404		{object}	httpx.ErrorResponse		"Unknown emiten"
//	@Security		ParticipantKeyAuth
//	@Router			/api/participant/orderbook/{kode} [get]
func (c *Controller) Get(w http.ResponseWriter, r *http.Request) {
	kode := r.PathValue("kode")

	state, err := c.svc.Book(kode)
	if err != nil {
		writeBookError(w, err, kode)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToBookSnapshot(state))
}

func writeBookError(w http.ResponseWriter, err error, kode string) {
	if errors.Is(err, ErrEmitenNotFound) {
		httpx.WriteError(w, http.StatusNotFound, "unknown emiten: "+kode)
		return
	}
	httpx.WriteError(w, http.StatusInternalServerError, "order book unavailable")
}
