// Package order is the write side of the exchange: it accepts an order, validates
// it, runs it through matching, persists the outcome atomically, and publishes the
// resulting book state. It is split into controller (HTTP), service (validation +
// orchestration) and transformer (engine types -> JSON DTOs).
package order

// The example/enums/format/validate tags below exist only so the generated
// OpenAPI document describes these shapes properly. No validation library reads
// them — the rules they advertise are enforced by Service.resolve.

// SubmitOrderRequest is the body of POST /api/orders.
type SubmitOrderRequest struct {
	// Emiten (listed instrument) code.
	Emiten string `json:"emiten" example:"BBCA" validate:"required"`
	// Participant (broker) code.
	Participant string `json:"participant" example:"YP" validate:"required"`
	// Order direction.
	Side string `json:"side" enums:"buy,sell" example:"buy" validate:"required"`
	// Order type. A market order never rests in the book.
	Type string `json:"type" enums:"limit,market" example:"limit" validate:"required"`
	// Whole rupiah, never a float. Required and > 0 for a limit order; ignored
	// and stored as 0 for a market order.
	Price int64 `json:"price" format:"int64" example:"8000"`
	// Quantity to trade. Must be > 0.
	Qty int64 `json:"qty" format:"int64" example:"100" minimum:"1" validate:"required"`
}

// OrderView is the submitted order's post-matching state.
type OrderView struct {
	// Database id of the order.
	ID int64 `json:"id" format:"int64" example:"3"`
	// Lifecycle state after matching.
	Status string `json:"status" enums:"open,filled,cancelled" example:"filled"`
	// Quantity not yet filled.
	Remaining int64 `json:"remaining" format:"int64" example:"0"`
}

// TradeView is a single execution produced by the submitted order.
type TradeView struct {
	// Execution price, which is always the passive (resting) order's price.
	Price int64 `json:"price" format:"int64" example:"8000"`
	// Quantity executed.
	Qty int64 `json:"qty" format:"int64" example:"100"`
	// Id of the buy side of this execution.
	BuyOrderID int64 `json:"buy_order_id" format:"int64" example:"3"`
	// Id of the sell side of this execution.
	SellOrderID int64 `json:"sell_order_id" format:"int64" example:"1"`
}

// SubmitOrderResponse is the body of a successful order submission.
type SubmitOrderResponse struct {
	Order  OrderView   `json:"order"`
	Trades []TradeView `json:"trades"`
}

// OrderHistoryView is one order in the admin history.
type OrderHistoryView struct {
	ID          int64  `json:"id" format:"int64" example:"31"`
	Seq         int64  `json:"seq" format:"int64" example:"31"`
	Emiten      string `json:"emiten" example:"BBCA"`
	Participant string `json:"participant" example:"YP"`
	Side        string `json:"side" enums:"buy,sell" example:"buy"`
	Type        string `json:"type" enums:"limit,market" example:"limit"`
	// 0 for a market order, which carries no price.
	Price int64 `json:"price" format:"int64" example:"8000"`
	Qty   int64 `json:"qty" format:"int64" example:"100"`
	// Quantity not yet filled.
	Remaining int64  `json:"remaining" format:"int64" example:"0"`
	Status    string `json:"status" enums:"open,filled,cancelled" example:"filled"`
}
