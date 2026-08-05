package index

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// How often a computed level is written to history.
//
// The index is recomputed on every trade but stored far less often: a row per
// execution would grow the history at the speed of the market while telling a
// chart nothing a one-minute point does not. It is a plain interval rather than
// a session-aligned schedule because the exchange has no trading hours yet.
const captureInterval = time.Minute

// Notifier recomputes the index off the order path.
//
// It exists because the recomputation cannot run inline. order.Service holds
// submitMu across the whole reserve-match-persist sequence, and the index needs
// a market-wide price query plus a pass over every instrument — putting that
// inside the lock would add the cost of valuing the entire market to the latency
// of every single order.
//
// A one-slot signal, not a queue: a trade that arrives while a recomputation is
// already pending needs no second recomputation, because the pending one will
// read the state that includes it. That is what makes a burst of executions cost
// one valuation rather than one each.
//
// Satisfies order.TradeObserver.
type Notifier struct {
	svc    *Service
	signal chan struct{}

	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// Call Run to start it.
func NewNotifier(svc *Service) *Notifier {
	return &Notifier{
		svc:    svc,
		signal: make(chan struct{}, 1),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// Never blocks: if a recomputation is already pending, this one is folded into
// it.
func (n *Notifier) TradesExecuted() {
	select {
	case n.signal <- struct{}{}:
	default: // already pending; it will pick up this trade too
	}
}

// Recomputes on each signal and captures history on a ticker, until Close.
//
// Both live on this one goroutine so that a capture can never read a level that
// is halfway through being replaced, and so recomputation stays serialized
// without a lock of its own.
//
// ctx bounds the database work, not the loop's lifetime — Close does that. They
// are separate because a cancelled request context must not stop the notifier,
// and a shutdown must not wait on a query that has already been abandoned.
func (n *Notifier) Run(ctx context.Context) {
	defer close(n.done)

	ticker := time.NewTicker(captureInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.stop:
			return
		case <-ctx.Done():
			return

		case <-n.signal:
			if err := n.svc.Recompute(ctx); err != nil {
				// Logged, not fatal: a stale index is a reporting problem, while a
				// failed trade would be a correctness one. The trade has already
				// committed and must not be undone over this.
				slog.Error("index: recompute", "err", err)
			}

		case <-ticker.C:
			if err := n.svc.Capture(ctx); err != nil {
				slog.Error("index: capture", "err", err)
			}
		}
	}
}

// Stops the loop and waits for it to finish.
//
// Idempotent, so a shutdown path that runs twice does not panic on a closed
// channel. It does not capture a final snapshot: at shutdown the database may
// already be draining, and a level that is at most one interval old is a better
// outcome than a shutdown that blocks on a write.
func (n *Notifier) Close() {
	n.stopOnce.Do(func() { close(n.stop) })
	<-n.done
}
