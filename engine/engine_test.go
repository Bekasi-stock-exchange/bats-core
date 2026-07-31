package engine

import "testing"

const emiten = int64(1)

// ord is a small helper to build a limit order tersely. Qty is set; Remaining
// and Seq are assigned by Submit.
func limitOrder(id int64, side Side, price, qty int64) *Order {
	return &Order{ID: id, EmitenID: emiten, ParticipantID: 1, Side: side, Type: Limit, Price: price, Qty: qty}
}

func marketOrder(id int64, side Side, qty int64) *Order {
	return &Order{ID: id, EmitenID: emiten, ParticipantID: 1, Side: side, Type: Market, Qty: qty}
}

// Test #1 — ordered insert.
func TestOrderedInsert(t *testing.T) {
	e := NewEngine(emiten)
	// No crossing prices, so all three rest in the book.
	e.Submit(limitOrder(1, Buy, 8000, 10))
	e.Submit(limitOrder(2, Buy, 8100, 10)) // higher bid -> front
	e.Submit(limitOrder(3, Buy, 7900, 10)) // lower bid  -> back

	e.Submit(limitOrder(4, Sell, 8300, 10))
	e.Submit(limitOrder(5, Sell, 8200, 10)) // lower ask -> front
	e.Submit(limitOrder(6, Sell, 8400, 10)) // higher ask -> back

	wantBids := []int64{8100, 8000, 7900}
	for i, p := range wantBids {
		if got := e.Book().Bids[i].Price; got != p {
			t.Fatalf("bids[%d]: got price %d, want %d", i, got, p)
		}
	}
	wantAsks := []int64{8200, 8300, 8400}
	for i, p := range wantAsks {
		if got := e.Book().Asks[i].Price; got != p {
			t.Fatalf("asks[%d]: got price %d, want %d", i, got, p)
		}
	}
}

// Test #2 — simple full match.
func TestSimpleMatch(t *testing.T) {
	e := NewEngine(emiten)
	sell := limitOrder(1, Sell, 8000, 100)
	e.Submit(sell)

	buy := limitOrder(2, Buy, 8000, 100)
	trades := e.Submit(buy)

	if len(trades) != 1 {
		t.Fatalf("got %d trades, want 1", len(trades))
	}
	tr := trades[0]
	if tr.Price != 8000 || tr.Qty != 100 {
		t.Fatalf("trade: got %d@%d, want 100@8000", tr.Qty, tr.Price)
	}
	if tr.BuyOrderID != 2 || tr.SellOrderID != 1 {
		t.Fatalf("trade ids: got buy=%d sell=%d, want buy=2 sell=1", tr.BuyOrderID, tr.SellOrderID)
	}
	if sell.Status != Filled || buy.Status != Filled {
		t.Fatalf("statuses: sell=%s buy=%s, want both filled", sell.Status, buy.Status)
	}
	if len(e.Book().Bids) != 0 || len(e.Book().Asks) != 0 {
		t.Fatalf("book not empty: bids=%d asks=%d", len(e.Book().Bids), len(e.Book().Asks))
	}
}

// Test #3 — partial fill: the larger order returns to the book with correct Remaining.
func TestPartialFill(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8000, 40))

	buy := limitOrder(2, Buy, 8000, 100)
	trades := e.Submit(buy)

	if len(trades) != 1 || trades[0].Qty != 40 {
		t.Fatalf("got %d trades (first qty %v), want 1 of qty 40", len(trades), trades)
	}
	if buy.Remaining != 60 || buy.Status != Open {
		t.Fatalf("buy: remaining=%d status=%s, want 60/open", buy.Remaining, buy.Status)
	}
	if len(e.Book().Bids) != 1 || e.Book().Bids[0].ID != 2 || e.Book().Bids[0].Remaining != 60 {
		t.Fatalf("book bid wrong: %+v", e.Book().Bids)
	}
	if len(e.Book().Asks) != 0 {
		t.Fatalf("asks should be empty, got %d", len(e.Book().Asks))
	}
}

// Test #4 — multi-level sweep: one order consumes several passive orders across price levels.
func TestMultiLevel(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8000, 50))
	e.Submit(limitOrder(2, Sell, 8050, 50))
	e.Submit(limitOrder(3, Sell, 8100, 50))

	buy := limitOrder(4, Buy, 8100, 120)
	trades := e.Submit(buy)

	// Expect 50@8000, 50@8050, 20@8100.
	want := []Trade{
		{Price: 8000, Qty: 50},
		{Price: 8050, Qty: 50},
		{Price: 8100, Qty: 20},
	}
	if len(trades) != len(want) {
		t.Fatalf("got %d trades, want %d: %+v", len(trades), len(want), trades)
	}
	for i, w := range want {
		if trades[i].Price != w.Price || trades[i].Qty != w.Qty {
			t.Fatalf("trade[%d]: got %d@%d, want %d@%d", i, trades[i].Qty, trades[i].Price, w.Qty, w.Price)
		}
	}
	if buy.Status != Filled || buy.Remaining != 0 {
		t.Fatalf("buy: status=%s remaining=%d, want filled/0", buy.Status, buy.Remaining)
	}
	// Ask at 8100 partially filled, 30 remains resting.
	if len(e.Book().Asks) != 1 || e.Book().Asks[0].Remaining != 30 {
		t.Fatalf("remaining ask wrong: %+v", e.Book().Asks)
	}
}

// Test #5 — market order: executes with no price limit; leftover is Cancelled and never booked.
func TestMarketOrder(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8000, 30))
	e.Submit(limitOrder(2, Sell, 8500, 20)) // far price; market takes it anyway

	buy := marketOrder(3, Buy, 100)
	trades := e.Submit(buy)

	// Fills 30@8000 then 20@8500, then liquidity is exhausted.
	if len(trades) != 2 {
		t.Fatalf("got %d trades, want 2: %+v", len(trades), trades)
	}
	if trades[0].Price != 8000 || trades[0].Qty != 30 {
		t.Fatalf("trade0: got %d@%d, want 30@8000", trades[0].Qty, trades[0].Price)
	}
	if trades[1].Price != 8500 || trades[1].Qty != 20 {
		t.Fatalf("trade1: got %d@%d, want 20@8500", trades[1].Qty, trades[1].Price)
	}
	if buy.Status != Cancelled || buy.Remaining != 50 {
		t.Fatalf("buy: status=%s remaining=%d, want cancelled/50", buy.Status, buy.Remaining)
	}
	if len(e.Book().Bids) != 0 {
		t.Fatalf("market order must not rest in book, got %d bids", len(e.Book().Bids))
	}
	if len(e.Book().Asks) != 0 {
		t.Fatalf("asks should be exhausted, got %d", len(e.Book().Asks))
	}
}

// Test #6 — time-priority tie-break: same price, smaller Seq fills first. Do not skip.
func TestTimePriorityTieBreak(t *testing.T) {
	e := NewEngine(emiten)
	first := limitOrder(1, Sell, 8000, 50)  // arrives first
	second := limitOrder(2, Sell, 8000, 50) // same price, arrives later
	e.Submit(first)
	e.Submit(second)

	// The two asks share a price; first (smaller Seq) must be at the front.
	if e.Book().Asks[0].ID != first.ID {
		t.Fatalf("tie-break order wrong: front ask is %d, want %d", e.Book().Asks[0].ID, first.ID)
	}

	buy := limitOrder(3, Buy, 8000, 50)
	trades := e.Submit(buy)

	if len(trades) != 1 || trades[0].SellOrderID != first.ID {
		t.Fatalf("expected first order (id=%d) to fill first, got %+v", first.ID, trades)
	}
	if first.Status != Filled {
		t.Fatalf("first order should be filled, got %s", first.Status)
	}
	if second.Status != Open || second.Remaining != 50 {
		t.Fatalf("second order should be untouched: status=%s remaining=%d", second.Status, second.Remaining)
	}
}

// Manual validation scenario from spec §7.
//
//	Book kosong.
//	1. SELL 100 @ 8000
//	2. SELL  50 @ 8050
//	3. BUY  120 @ 8050
//
// Expected: trade 100@8000, trade 20@8050, book leaves SELL 30@8050, buy Filled.
func TestSpecManualScenario(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8000, 100))
	e.Submit(limitOrder(2, Sell, 8050, 50))

	buy := limitOrder(3, Buy, 8050, 120)
	trades := e.Submit(buy)

	if len(trades) != 2 {
		t.Fatalf("got %d trades, want 2: %+v", len(trades), trades)
	}
	if trades[0].Price != 8000 || trades[0].Qty != 100 {
		t.Fatalf("trade0: got %d@%d, want 100@8000", trades[0].Qty, trades[0].Price)
	}
	if trades[1].Price != 8050 || trades[1].Qty != 20 {
		t.Fatalf("trade1: got %d@%d, want 20@8050", trades[1].Qty, trades[1].Price)
	}
	if buy.Status != Filled {
		t.Fatalf("buy status = %s, want filled", buy.Status)
	}
	if len(e.Book().Asks) != 1 || e.Book().Asks[0].Price != 8050 || e.Book().Asks[0].Remaining != 30 {
		t.Fatalf("book should leave SELL 30@8050, got %+v", e.Book().Asks)
	}
	if len(e.Book().Bids) != 0 {
		t.Fatalf("bids should be empty, got %d", len(e.Book().Bids))
	}
}

// snapshotBook captures id/remaining/status of every resting order on both
// sides, in book order, so a test can assert the book is bit-identical after
// an unwind.
type bookRow struct {
	id, remaining int64
	status        Status
}

func snapshotBook(e *Engine) (bids, asks []bookRow) {
	for _, o := range e.Book().Bids {
		bids = append(bids, bookRow{o.ID, o.Remaining, o.Status})
	}
	for _, o := range e.Book().Asks {
		asks = append(asks, bookRow{o.ID, o.Remaining, o.Status})
	}
	return bids, asks
}

func assertBookEqual(t *testing.T, e *Engine, wantBids, wantAsks []bookRow) {
	t.Helper()
	gotBids, gotAsks := snapshotBook(e)
	if len(gotBids) != len(wantBids) || len(gotAsks) != len(wantAsks) {
		t.Fatalf("book size changed: bids %d->%d, asks %d->%d",
			len(wantBids), len(gotBids), len(wantAsks), len(gotAsks))
	}
	for i := range wantBids {
		if gotBids[i] != wantBids[i] {
			t.Fatalf("bids[%d]: got %+v, want %+v", i, gotBids[i], wantBids[i])
		}
	}
	for i := range wantAsks {
		if gotAsks[i] != wantAsks[i] {
			t.Fatalf("asks[%d]: got %+v, want %+v", i, gotAsks[i], wantAsks[i])
		}
	}
}

func failCommit([]Trade) error { return errTest }

var errTest = errorString("commit failed")

type errorString string

func (e errorString) Error() string { return string(e) }

// A failed commit must leave the book exactly as it was: swept levels
// reinstated with their original remaining/status, the partially filled level
// restored, and the incoming order's resting remainder removed.
func TestSubmitAtomicUnwindsSweep(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8000, 100))
	e.Submit(limitOrder(2, Sell, 8050, 50))
	e.Submit(limitOrder(3, Sell, 8100, 50))
	wantBids, wantAsks := snapshotBook(e)
	seqBefore := *e.seq

	// Sweeps 8000 fully, 8050 partially, then rests 30 on the bid side.
	buy := limitOrder(4, Buy, 8050, 180)
	trades, err := e.SubmitAtomic(buy, failCommit)
	if err == nil || trades != nil {
		t.Fatalf("want commit error and nil trades, got %v, %+v", err, trades)
	}

	assertBookEqual(t, e, wantBids, wantAsks)
	if *e.seq != seqBefore {
		t.Fatalf("sequencer not restored: got %+v, want %+v", *e.seq, seqBefore)
	}

	// The book must still be fully functional: the same submission with a
	// succeeding commit produces the same trades a fresh pass would.
	retry := limitOrder(4, Buy, 8050, 180)
	trades, err = e.SubmitAtomic(retry, func([]Trade) error { return nil })
	if err != nil {
		t.Fatalf("retry failed: %v", err)
	}
	if len(trades) != 2 || trades[0].Qty != 100 || trades[0].Price != 8000 || trades[1].Qty != 50 || trades[1].Price != 8050 {
		t.Fatalf("retry trades wrong: %+v", trades)
	}
	if len(e.Book().Bids) != 1 || e.Book().Bids[0].Remaining != 30 {
		t.Fatalf("retry should rest 30 on bids, got %+v", e.Book().Bids)
	}
}

// An incoming order that matches nothing and rests must be removed again on a
// failed commit.
func TestSubmitAtomicUnwindsRestingOnly(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8100, 50))
	wantBids, wantAsks := snapshotBook(e)

	buy := limitOrder(2, Buy, 8000, 10) // does not cross; would rest
	if _, err := e.SubmitAtomic(buy, failCommit); err == nil {
		t.Fatal("want commit error")
	}
	assertBookEqual(t, e, wantBids, wantAsks)
}

// A market order never rests, so the unwind only has passive fills to restore.
func TestSubmitAtomicUnwindsMarketOrder(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8000, 30))
	e.Submit(limitOrder(2, Sell, 8500, 20))
	wantBids, wantAsks := snapshotBook(e)

	buy := marketOrder(3, Buy, 100) // sweeps both levels, leftover cancelled
	if _, err := e.SubmitAtomic(buy, failCommit); err == nil {
		t.Fatal("want commit error")
	}
	assertBookEqual(t, e, wantBids, wantAsks)
}

// A successful commit keeps the matched state — SubmitAtomic must be
// indistinguishable from Submit when nothing fails.
func TestSubmitAtomicCommitKeepsState(t *testing.T) {
	e := NewEngine(emiten)
	e.Submit(limitOrder(1, Sell, 8000, 100))

	buy := limitOrder(2, Buy, 8000, 40)
	trades, err := e.SubmitAtomic(buy, func([]Trade) error { return nil })
	if err != nil || len(trades) != 1 || trades[0].Qty != 40 {
		t.Fatalf("got err=%v trades=%+v, want one trade of 40", err, trades)
	}
	if e.Book().Asks[0].Remaining != 60 {
		t.Fatalf("ask remaining = %d, want 60", e.Book().Asks[0].Remaining)
	}
}
