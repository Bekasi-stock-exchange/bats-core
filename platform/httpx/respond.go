// Package httpx holds the transport plumbing shared by every controller: JSON
// response writing, the error envelope, pagination parsing, and middleware.
//
// It knows nothing about orders, books, or matching — controllers depend on it,
// it depends on no domain package. That one-way rule is what lets both the order
// and orderbook domains share it without coupling them to each other.
package httpx

import (
	"encoding/json"
	"net/http"
)

// ErrorResponse is the body of every failed request.
type ErrorResponse struct {
	Error string `json:"error"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, ErrorResponse{Error: msg})
}
