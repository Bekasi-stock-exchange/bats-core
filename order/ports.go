package order

import "context"

// OrderRecord is the persisted shape of an order (history/audit). The matching
// source of truth is the in-memory book, not this table.
type OrderRecord struct {
	ID            int64
	EmitenID      int64
	ParticipantID int64
	Side          string
	Type          string
	Price         int64
	Qty           int64
	Remaining     int64
	Status        string
	Seq           int64
}

// TradeRecord is the persisted shape of an executed trade.
type TradeRecord struct {
	EmitenID    int64
	BuyOrderID  int64
	SellOrderID int64
	Price       int64
	Qty         int64
	Seq         int64
}

// Fill is the quantity a resting order traded away in one matching pass.
type Fill struct {
	OrderID int64
	Qty     int64
}

// Execution is everything a single matching pass produced: the incoming order at
// its final state, the trades it generated, and the fills to apply to the resting
// orders it consumed.
//
// It is saved as one unit so the database can never hold a partial outcome.
type Execution struct {
	Order  OrderRecord
	Trades []TradeRecord
	Fills  []Fill
}

// Repository persists orders and trades.
//
// The interface is declared here, in the package that consumes it, and satisfied
// by the repository package — so the service depends on this behaviour, not on a
// database handle.
type Repository interface {
	// NextOrderID reserves an order id ahead of the insert. The id is needed
	// before matching because trades reference it as a foreign key, while the
	// order row itself can only be written afterwards, once its sequence number
	// and final state are known.
	NextOrderID(ctx context.Context) (int64, error)

	// SaveExecution writes an entire matching outcome in one transaction.
	SaveExecution(ctx context.Context, ex Execution) error

	// MaxSeqs returns the highest sequence number already used by orders and by
	// trades (0 when a table is empty). Used at startup to seed the engine's
	// sequencer so newly assigned values never collide with persisted ones.
	MaxSeqs(ctx context.Context) (maxOrderSeq, maxTradeSeq int64, err error)
}
