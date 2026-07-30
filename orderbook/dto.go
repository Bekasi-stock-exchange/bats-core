// Package orderbook is the read side of the book: REST snapshots and the
// WebSocket stream. It is split into controller (HTTP), service (orchestration)
// and transformer (market types -> JSON DTOs).
package orderbook

// The example/format tags below exist only so the generated OpenAPI document
// describes these shapes properly; nothing reads them at runtime.

// PriceLevel is one aggregated level of the book: the total remaining quantity
// resting at a single price.
type PriceLevel struct {
	Price int64 `json:"price" format:"int64" example:"8050"`
	Qty   int64 `json:"qty" format:"int64" example:"30"`
}

// BookSnapshot is the shape returned by the orderbook endpoints and pushed over
// WebSocket.
//
// Field order matters: Type is first and omitempty, so a REST response starts at
// "emiten" while a WebSocket push leads with "type":"update".
type BookSnapshot struct {
	// Present as "update" on WebSocket pushes; omitted on REST responses.
	Type string `json:"type,omitempty" example:"update"`
	// Emiten code this book belongs to.
	Emiten string `json:"emiten" example:"BBCA"`
	// Buy side, highest price first.
	Bids []PriceLevel `json:"bids"`
	// Sell side, lowest price first.
	Asks []PriceLevel `json:"asks"`
}
