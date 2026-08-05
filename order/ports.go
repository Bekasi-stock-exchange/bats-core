package order

import (
	"context"

	"bekasi-automatic-trading-system/market"
)

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

// The participant ids are stored alongside the order ids so per-broker history is
// an indexed lookup on this table, rather than a join back through orders filtered
// by an OR across both sides — which no index can serve.
type TradeRecord struct {
	EmitenID          int64
	BuyOrderID        int64
	SellOrderID       int64
	BuyParticipantID  int64
	SellParticipantID int64
	Price             int64
	Qty               int64
	Seq               int64
}

// Fill is the quantity a resting order traded away in one matching pass.
type Fill struct {
	OrderID int64
	Qty     int64
}

// AssetDelta is a change to one broker's holding of one emiten: positive when it
// bought, negative when it sold.
type AssetDelta struct {
	ParticipantID int64
	EmitenID      int64
	Delta         int64
}

// WalletDelta is a change to one broker's cash balance: negative when it bought
// (cash paid out), positive when it sold (cash received).
type WalletDelta struct {
	ParticipantID int64
	Delta         int64
}

// Execution is everything a single matching pass produced: the incoming order at
// its final state, the trades it generated, the fills to apply to the resting
// orders it consumed, and the share and cash movements between the brokers
// involved.
//
// It is saved as one unit so the database can never hold a partial outcome — in
// particular, holdings and balances can never disagree with the trades that
// moved them.
type Execution struct {
	Order   OrderRecord
	Trades  []TradeRecord
	Fills   []Fill
	Assets  []AssetDelta
	Wallets []WalletDelta
}

// The interface is declared here, in the package that consumes it, and satisfied
// by the repository package — so the service depends on this behaviour, not on a
// database handle.
type Repository interface {
	// NextOrderID reserves an order id ahead of the insert. The id is needed
	// before matching because trades reference it as a foreign key, while the
	// order row itself can only be written afterwards, once its sequence number
	// and final state are known.
	NextOrderID(ctx context.Context) (int64, error)

	// Writes an entire matching outcome in one transaction.
	SaveExecution(ctx context.Context, ex Execution) error

	// CancelOrder marks a resting order cancelled, keeping whatever quantity it
	// had already filled. It must only affect a row that is still open, and must
	// report whether it did: the in-memory book and this table can only be
	// cancelled together, so a row that moved on (filled by a matching pass that
	// committed first) has to fail the cancel rather than overwrite it.
	CancelOrder(ctx context.Context, orderID int64) (bool, error)

	// MaxSeqs returns the highest sequence number already used by orders and by
	// trades (0 when a table is empty). Used at startup to seed the engine's
	// sequencer so newly assigned values never collide with persisted ones.
	MaxSeqs(ctx context.Context) (maxOrderSeq, maxTradeSeq int64, err error)

	// LoadOpenOrders returns every order still open, sorted by Seq ascending, to
	// rebuild the in-memory book and its reservations at startup.
	LoadOpenOrders(ctx context.Context) ([]market.OpenOrder, error)

	// Newest first.
	ListOrders(ctx context.Context, f OrderFilter, limit, offset int) ([]OrderRecord, error)
	CountOrders(ctx context.Context, f OrderFilter) (int, error)
}

// OrderFilter narrows the order history. A nil field means "no constraint".
type OrderFilter struct {
	EmitenID      *int64
	ParticipantID *int64
	Status        *string
}
