package engine

// Engine owns a single order book (one emiten). Order time-priority Seq and
// global execution (trade) Seq come from a Sequencer shared across all engines,
// so both are monotonic and unique across every emiten — matching the UNIQUE
// (seq) columns in the database.
//
// The engine has exactly one public entry point, Submit. Keeping the surface
// narrow is deliberate: it makes the engine cheap to wrap behind a channel and
// extract into its own service later.
//
// Concurrency: Submit is not internally synchronized, and the shared Sequencer
// is not either. Per the spec the intended pattern is one channel -> one
// goroutine -> the order books, so that matching is sequential and
// deterministic. Callers must ensure only a single goroutine drives Submit
// across all engines that share a Sequencer.
type Engine struct {
	book *OrderBook
	seq  *Sequencer
}

// Sequencer hands out monotonic order and trade sequence numbers. A single
// Sequencer is shared by every engine so the numbers are globally unique. It is
// not internally locked; the caller serializes access (see Engine's concurrency
// note).
type Sequencer struct {
	order int64
	trade int64
}

// NewSequencer returns a Sequencer whose next order/trade Seq continue past the
// highest values already persisted (pass 0 for a fresh database). This keeps Seq
// monotonic and never reused across process restarts.
func NewSequencer(lastOrderSeq, lastTradeSeq int64) *Sequencer {
	return &Sequencer{order: lastOrderSeq, trade: lastTradeSeq}
}

func (s *Sequencer) nextOrder() int64 {
	s.order++
	return s.order
}

func (s *Sequencer) nextTrade() int64 {
	s.trade++
	return s.trade
}

// NewEngine returns an engine with an empty book for the given emiten, using a
// private zero-based Sequencer. Convenient for tests; production wiring shares
// one Sequencer via NewEngineWithSequencer.
func NewEngine(emitenID int64) *Engine {
	return &Engine{book: NewOrderBook(emitenID), seq: NewSequencer(0, 0)}
}

// NewEngineWithSequencer returns an engine for the given emiten that draws Seq
// values from the shared sequencer.
func NewEngineWithSequencer(emitenID int64, seq *Sequencer) *Engine {
	return &Engine{book: NewOrderBook(emitenID), seq: seq}
}

// Book exposes the order book for read-only inspection (order book snapshots,
// tests). Callers must not mutate the returned slices.
func (e *Engine) Book() *OrderBook {
	return e.book
}

// Submit runs the incoming order through continuous matching and returns the
// trades it produced, in execution order.
//
// It mutates o in place: assigns its Seq, decrements Remaining as it fills, and
// sets its final Status. A limit order with leftover quantity is inserted into
// the book; a market order with leftover quantity is cancelled and never booked.
func (e *Engine) Submit(o *Order) []Trade {
	o.Seq = e.seq.nextOrder()
	o.Remaining = o.Qty
	o.Status = Open

	var trades []Trade

	for o.Remaining > 0 {
		passive := e.oppositeBest(o)
		if passive == nil || !e.crosses(o, passive) {
			break
		}

		qty := min64(o.Remaining, passive.Remaining)

		// Execution price is always the passive (resting) order's price.
		buy, sell := sides(o, passive)
		trades = append(trades, Trade{
			EmitenID:          e.book.EmitenID,
			BuyOrderID:        buy.ID,
			SellOrderID:       sell.ID,
			BuyParticipantID:  buy.ParticipantID,
			SellParticipantID: sell.ParticipantID,
			Price:             passive.Price,
			Qty:               qty,
			Seq:               e.seq.nextTrade(),
		})

		o.Remaining -= qty
		passive.Remaining -= qty

		if passive.Remaining == 0 {
			passive.Status = Filled
			e.removeBest(o)
		}
	}

	if o.Remaining == 0 {
		o.Status = Filled
		return trades
	}

	// Leftover quantity.
	if o.Type == Market {
		o.Status = Cancelled // market orders never rest in the book
		return trades
	}

	// Limit order with remainder rests in the book.
	o.Status = Open
	if o.Side == Buy {
		e.book.insertBid(o)
	} else {
		e.book.insertAsk(o)
	}
	return trades
}

// Restore re-inserts an order that was already resting in the book before a
// restart, without running it through matching.
//
// It exists because the book is pure in-memory state: on restart it starts
// empty, while the database still has every order that was left "open" when
// the process stopped. Those orders never got the chance to cross each other
// again — they were already resting together without crossing, or matching
// would have consumed them — so re-submitting them through Submit would be
// wrong twice over: it would re-execute trades that already happened, and it
// would assign them fresh Seq values, destroying the time priority that a
// stored Seq already records.
//
// The caller is responsible for restoring orders across all engines in Seq
// order, so ties within a single price level land in their original sequence.
func (e *Engine) Restore(o *Order) {
	if o.Side == Buy {
		e.book.insertBid(o)
	} else {
		e.book.insertAsk(o)
	}
}

// EstimateCost returns the worst-case notional a buy order could spend, and
// whether that estimate is known.
//
// For a limit order it is Qty * Price: execution price is always the passive
// (resting) order's price, which for a buy can only be at or below its own
// limit, so the limit price is already the ceiling. For a market order there is
// no limit price, so the estimate walks the resting asks the same way matching
// would consume them; if the book cannot fill the whole quantity, the true cost
// is unknowable in advance and ok is false — a market buy against a thin book is
// let through and settled for whatever it actually costs, the same way it
// already risks an unfavourable price with no ceiling at all.
func (e *Engine) EstimateCost(o *Order) (cost int64, ok bool) {
	if o.Type == Limit {
		return o.Qty * o.Price, true
	}

	remaining := o.Qty
	for i := 0; remaining > 0 && i < len(e.book.Asks); i++ {
		ask := e.book.Asks[i]
		qty := min64(remaining, ask.Remaining)
		cost += qty * ask.Price
		remaining -= qty
	}
	if remaining > 0 {
		return 0, false
	}
	return cost, true
}

// oppositeBest returns the best resting order on the side opposite to o.
func (e *Engine) oppositeBest(o *Order) *Order {
	if o.Side == Buy {
		return e.book.bestAsk()
	}
	return e.book.bestBid()
}

// removeBest pops the best resting order on the side opposite to o (the passive
// order that just filled).
func (e *Engine) removeBest(o *Order) {
	if o.Side == Buy {
		e.book.popBestAsk()
	} else {
		e.book.popBestBid()
	}
}

// crosses reports whether the incoming order o can match against passive.
//
// A market order has no price limit, so it always crosses whatever liquidity is
// available. A limit buy crosses when its price is at least the ask; a limit
// sell crosses when its price is at most the bid.
func (e *Engine) crosses(o, passive *Order) bool {
	if o.Type == Market {
		return true
	}
	if o.Side == Buy {
		return o.Price >= passive.Price
	}
	return o.Price <= passive.Price
}

// sides returns the incoming and passive orders sorted into buy and sell.
//
// It replaces the previous buyID/sellID pair: a trade now records each side's
// participant as well as its order id, and resolving the orders once keeps those
// two facts from drifting apart.
func sides(o, passive *Order) (buy, sell *Order) {
	if o.Side == Buy {
		return o, passive
	}
	return passive, o
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
