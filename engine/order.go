// Package engine is the JAST core matching engine.
//
// It is deliberately pure: it imports nothing from the api or store layers, no
// HTTP library, no database driver, and no encoding/json. Engine types carry no
// struct tags so the engine can later be extracted into a standalone service
// without dragging transport or persistence concerns with it.
package engine

// Side is the direction of an order.
type Side string

const (
	Buy  Side = "buy"
	Sell Side = "sell"
)

// Type is the order type. Only limit and market are supported.
type Type string

const (
	Limit  Type = "limit"
	Market Type = "market"
)

// Status is the lifecycle state of an order.
type Status string

const (
	Open      Status = "open"
	Filled    Status = "filled"
	Cancelled Status = "cancelled"
)

// Order is a single order tracked by the engine.
//
// Price and quantities are int64 (whole rupiah / whole lots). Float is never
// used for money. Price is 0 by convention for a market order.
type Order struct {
	ID            int64
	EmitenID      int64
	ParticipantID int64
	Side          Side
	Type          Type
	Price         int64 // 0 for market orders
	Qty           int64 // original quantity
	Remaining     int64 // quantity not yet filled
	Status        Status
	Seq           int64 // monotonic entry sequence; the time-priority key
}

// Trade is a single execution between a buy order and a sell order.
type Trade struct {
	EmitenID    int64
	BuyOrderID  int64
	SellOrderID int64
	Price       int64 // execution price = the passive (resting) order's price
	Qty         int64
	Seq         int64 // global execution sequence
}
