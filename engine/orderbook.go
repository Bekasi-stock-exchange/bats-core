package engine

// OrderBook is the in-memory book for a single emiten.
//
// Both sides are plain slices of *Order kept sorted on insert. Per the spec this
// is intentional: no heap, tree, or skip list. O(n) insertion is acceptable at
// this stage; data-structure optimization is a non-goal.
//
//   - Bids: buy orders, highest price first. Tie on price -> smaller Seq first.
//   - Asks: sell orders, lowest price first.  Tie on price -> smaller Seq first.
//
// The front element of each slice is therefore always the best price with the
// oldest arrival, i.e. the price-time-priority head.
type OrderBook struct {
	EmitenID int64
	Bids     []*Order
	Asks     []*Order
}

func NewOrderBook(emitenID int64) *OrderBook {
	return &OrderBook{EmitenID: emitenID}
}

// Keeps Bids sorted by price desc, then Seq asc.
func (b *OrderBook) insertBid(o *Order) {
	i := 0
	for i < len(b.Bids) {
		cur := b.Bids[i]
		// New order goes before the first resting order that is strictly worse:
		// a lower price, or the same price but a later Seq.
		if o.Price > cur.Price || (o.Price == cur.Price && o.Seq < cur.Seq) {
			break
		}
		i++
	}
	b.Bids = insertAt(b.Bids, i, o)
}

// Keeps Asks sorted by price asc, then Seq asc.
func (b *OrderBook) insertAsk(o *Order) {
	i := 0
	for i < len(b.Asks) {
		cur := b.Asks[i]
		if o.Price < cur.Price || (o.Price == cur.Price && o.Seq < cur.Seq) {
			break
		}
		i++
	}
	b.Asks = insertAt(b.Asks, i, o)
}

func (b *OrderBook) bestBid() *Order {
	if len(b.Bids) == 0 {
		return nil
	}
	return b.Bids[0]
}

func (b *OrderBook) bestAsk() *Order {
	if len(b.Asks) == 0 {
		return nil
	}
	return b.Asks[0]
}

func (b *OrderBook) popBestBid() {
	b.Bids = b.Bids[1:]
}

func (b *OrderBook) popBestAsk() {
	b.Asks = b.Asks[1:]
}

func insertAt(s []*Order, i int, o *Order) []*Order {
	s = append(s, nil)
	copy(s[i+1:], s[i:])
	s[i] = o
	return s
}
