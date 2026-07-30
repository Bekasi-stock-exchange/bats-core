package participant

import (
	"encoding/json"
	"errors"
	"net/http"

	"bekasi-automatic-trading-system/platform/httpx"
)

// Controller is the admin surface for broker identity: create brokers, list them,
// and issue or revoke their keys.
type Controller struct {
	svc *Service
	// duplicate reports whether an error is a unique-constraint violation. It is
	// injected so this package needs no dependency on the repository or on pgx
	// error codes.
	duplicate func(error) bool
}

// NewController returns a controller backed by svc. isDuplicate maps a storage
// conflict onto 409.
func NewController(svc *Service, isDuplicate func(error) bool) *Controller {
	return &Controller{svc: svc, duplicate: isDuplicate}
}

// Create handles POST /api/admin/participants.
//
//	@Summary		Create a broker
//	@Description	Registers an exchange participant and issues its first API key.
//	@Description	The key is returned **once, in this response only** — it is stored as a
//	@Description	SHA-256 hash and cannot be retrieved again. A lost key is re-issued, not
//	@Description	recovered.
//	@Tags			admin
//	@ID				createParticipant
//	@Accept			json
//	@Produce		json
//	@Param			body	body		participant.CreateRequest	true	"Broker to create"
//	@Success		201		{object}	participant.IssuedKeyResponse	"Created; contains the only copy of the key"
//	@Failure		400		{object}	httpx.ErrorResponse			"Missing kode or nama"
//	@Failure		401		{object}	httpx.ErrorResponse			"Missing or wrong X-API-Key"
//	@Failure		409		{object}	httpx.ErrorResponse			"Broker code already exists"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/participants [post]
func (c *Controller) Create(w http.ResponseWriter, r *http.Request) {
	var req CreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	rec, key, err := c.svc.Create(r.Context(), req.Kode, req.Nama)
	if err != nil {
		c.writeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, ToIssuedKeyResponse(rec, key))
}

// List handles GET /api/admin/participants.
//
//	@Summary		List brokers
//	@Description	Every exchange participant, ordered by code. API keys are **not** included
//	@Description	and cannot be: only their hashes are stored. The prefix identifies which
//	@Description	key is in place without exposing it.
//	@Tags			admin
//	@ID				listParticipants
//	@Produce		json
//	@Success		200	{array}		participant.ParticipantView
//	@Failure		401	{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/participants [get]
func (c *Controller) List(w http.ResponseWriter, r *http.Request) {
	recs, err := c.svc.List(r.Context())
	if err != nil {
		c.writeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ToParticipantViews(recs))
}

// IssueKey handles POST /api/admin/participants/apikey.
//
//	@Summary		Re-issue a broker's API key
//	@Description	Mints a new key and invalidates the previous one immediately. The key is
//	@Description	returned once and never again. The target broker travels in the body, not
//	@Description	the path, so no identifier lands in access or proxy logs.
//	@Tags			admin
//	@ID				issueParticipantKey
//	@Accept			json
//	@Produce		json
//	@Param			body	body		participant.KeyRequest			true	"Target broker"
//	@Success		201		{object}	participant.IssuedKeyResponse	"The only copy of the new key"
//	@Failure		400		{object}	httpx.ErrorResponse				"Unknown participant"
//	@Failure		401		{object}	httpx.ErrorResponse				"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/participants/apikey [post]
func (c *Controller) IssueKey(w http.ResponseWriter, r *http.Request) {
	req, ok := c.decodeKeyRequest(w, r)
	if !ok {
		return
	}

	key, err := c.svc.Issue(r.Context(), req.Participant)
	if err != nil {
		c.writeError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, IssuedKeyResponse{Kode: req.Participant, APIKey: key})
}

// RevokeKey handles DELETE /api/admin/participants/apikey.
//
//	@Summary		Revoke a broker's API key
//	@Description	Removes the key. The next request carrying it is rejected immediately —
//	@Description	authentication reads the database rather than a cache, so revocation is
//	@Description	not delayed by an expiry.
//	@Tags			admin
//	@ID				revokeParticipantKey
//	@Accept			json
//	@Param			body	body	participant.KeyRequest	true	"Target broker"
//	@Success		204		"Revoked"
//	@Failure		400		{object}	httpx.ErrorResponse	"Unknown participant"
//	@Failure		401		{object}	httpx.ErrorResponse	"Missing or wrong X-API-Key"
//	@Security		ApiKeyAuth
//	@Router			/api/admin/participants/apikey [delete]
func (c *Controller) RevokeKey(w http.ResponseWriter, r *http.Request) {
	req, ok := c.decodeKeyRequest(w, r)
	if !ok {
		return
	}

	if err := c.svc.Revoke(r.Context(), req.Participant); err != nil {
		c.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *Controller) decodeKeyRequest(w http.ResponseWriter, r *http.Request) (KeyRequest, bool) {
	var req KeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid JSON body")
		return KeyRequest{}, false
	}
	if req.Participant == "" {
		httpx.WriteError(w, http.StatusBadRequest, "participant is required")
		return KeyRequest{}, false
	}
	return req, true
}

// writeError maps a service error onto the response: a broken rule is the
// client's, a conflict is a 409, anything else is ours and is reported without
// leaking internals.
func (c *Controller) writeError(w http.ResponseWriter, err error) {
	var invalid ValidationError
	switch {
	case errors.As(err, &invalid):
		httpx.WriteError(w, http.StatusBadRequest, invalid.Msg)
	case c.duplicate != nil && c.duplicate(err):
		httpx.WriteError(w, http.StatusConflict, "already exists")
	default:
		httpx.WriteError(w, http.StatusInternalServerError, "participant request failed")
	}
}
