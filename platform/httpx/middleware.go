package httpx

import "net/http"

// Middleware wraps a handler with cross-cutting behaviour.
type Middleware func(http.HandlerFunc) http.HandlerFunc

// RequireAPIKey rejects any request whose X-API-Key header does not match key.
//
// Browsers cannot set custom headers on a native WebSocket handshake, so the WS
// route is not exercisable from Swagger UI; that is accepted, since this API is
// meant for server-side clients.
func RequireAPIKey(key string) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-API-Key") != key {
				// NOTE: deliberately kept byte-identical to the previous
				// implementation, which wrote this JSON body through
				// http.Error and therefore labelled it text/plain. Switching
				// to WriteError would change the Content-Type, so it is left
				// alone until that contract change is agreed.
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next(w, r)
		}
	}
}
