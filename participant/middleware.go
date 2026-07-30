package participant

import (
	"net/http"

	"bekasi-automatic-trading-system/platform/httpx"
)

// KeyHeader is the header a broker sends its API key in. It is distinct from the
// admin X-API-Key so a request sent to the wrong tier fails cleanly instead of
// being checked against the wrong secret.
const KeyHeader = "X-Participant-Key"

// RequireKey authenticates a broker by its API key and puts the resolved identity
// in the request context.
//
// Missing, malformed, and revoked keys all produce the same 401 with the same
// body. Distinguishing them would turn this into an oracle for probing which keys
// exist.
//
// Unlike the admin middleware, this performs a database lookup per request. That
// is deliberate: keys are issued and revoked at runtime, so a cache would keep a
// revoked key working until it expired.
func RequireKey(svc *Service) httpx.Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, err := svc.Authenticate(r.Context(), r.Header.Get(KeyHeader))
			if err != nil {
				httpx.WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next(w, r.WithContext(NewContext(r.Context(), id)))
		}
	}
}
