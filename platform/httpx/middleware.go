package httpx

import "net/http"

// Middleware wraps a handler with cross-cutting behaviour.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// AdminKeyHeader is the header carrying the static administrative key.
const AdminKeyHeader = "X-API-Key"

// RequireStaticKey rejects any request whose X-API-Key header does not match key.
//
// This is the admin tier: one shared secret from configuration, compared in
// process. Brokers authenticate differently — see participant.RequireKey, which
// resolves a per-broker key against the database.
//
// Browsers cannot set custom headers on a native WebSocket handshake, so the WS
// routes are not exercisable from Swagger UI; that is accepted, since this API is
// meant for server-side clients.
func RequireStaticKey(key string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get(AdminKeyHeader) != key {
				WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			next(w, r)
		}
	}
}
