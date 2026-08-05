package participant

import "context"

// Identity is the broker behind an authenticated request, resolved from its API
// key by the middleware.
type Identity struct {
	ID   int64
	Kode string
	Nama string
}

// ctxKey is unexported so no other package can plant or read an identity — the
// only way one enters the context is through the middleware, which means anything
// reading it can trust it came from a verified key.
type ctxKey struct{}

func NewContext(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the authenticated broker, if the request passed through the
// participant middleware.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}
