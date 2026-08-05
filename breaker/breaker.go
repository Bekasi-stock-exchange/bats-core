// Package breaker is the circuit breaker's moving parts: the observer that
// records a halt when one trips, and the loop that ends halts when their time is
// up.
//
// It exists because a halt spans three things that must not know about each
// other. The registry owns the live state and the lock that keeps matching
// deterministic; the repository owns the copy that survives a restart; the hub
// owns the fan-out to subscribers. Wiring those together inside the registry
// would drag persistence and websockets below the shared kernel, and putting it
// in the order domain would make halts a side effect of submitting an order
// rather than a market-wide event. This package sits above all three and depends
// on each through an interface it declares itself.
//
// It is also the first thing in this system that runs on a clock rather than in
// response to a request, which is the one structural change the circuit breaker
// forces. See Supervisor.Run for why that is a single poll rather than a timer
// per halt.
package breaker

import (
	"context"
	"log/slog"
	"time"

	"bekasi-automatic-trading-system/market"
)

// Store persists halts so they outlive the process.
//
// Declared here and satisfied by repository.Halt, so this package depends on the
// behaviour rather than on a database handle.
type Store interface {
	SaveHalt(ctx context.Context, emitenID, price, reference int64, until time.Time) error
	ClearHalt(ctx context.Context, emitenID int64) error
}

// Books is the slice of the registry this package drives.
//
// Narrow on purpose: the supervisor may end halts and read book state, and
// nothing else. Handing it the whole *market.Registry would let a later change
// reach matching from a background goroutine, which is exactly what the
// registry's concurrency model forbids.
type Books interface {
	ExpireHalts() []int64

	// For broadcasting a resume.
	Snapshot(emitenID int64) (market.BookState, error)
}

// Satisfied by market.Hub.
type Broadcaster interface {
	Broadcast(emitenID int64, state market.BookState)
}

type Supervisor struct {
	books Books
	store Store
	hub   Broadcaster
	log   *slog.Logger

	// interval is how often expired halts are swept up. It bounds how late a
	// resume is *announced*, not how late trading reopens: the registry decides
	// that against the clock, so an instrument is tradeable the instant its
	// deadline passes regardless of when this next runs.
	interval time.Duration
}

// DefaultInterval is how often the supervisor sweeps for expired halts.
//
// One second, against halts measured in minutes. The registry already reopens a
// book at its exact deadline, so this only bounds the lag on the announcement
// and the database cleanup — a second of which nobody can act on, while a
// tighter loop would take the registry's global lock for no gain.
const DefaultInterval = time.Second

// hub may be nil, in which case resumes are not broadcast.
func NewSupervisor(books Books, store Store, hub Broadcaster, log *slog.Logger) *Supervisor {
	return &Supervisor{
		books:    books,
		store:    store,
		hub:      hub,
		log:      log,
		interval: DefaultInterval,
	}
}

// Halted records a tripped breaker. It satisfies market.HaltObserver.
//
// Called from the submit path with the registry's lock already released. The
// write is best-effort and its failure is logged rather than returned: the halt
// is already in force in the registry, the order that triggered it has already
// committed, and failing the client's request now would report a submission as
// rejected when it in fact executed. What is lost on a failed write is only the
// halt's ability to survive a restart, which is worth a loud log and not worth
// unwinding a trade for.
func (s *Supervisor) Halted(emitenID int64, price, reference int64, until time.Time) {
	s.log.Warn("circuit breaker tripped",
		"emiten_id", emitenID,
		"price", price,
		"reference", reference,
		"resumes_at", until)

	// A background context, deliberately. This runs on the submit path, but the
	// halt is market state rather than part of the client's request — if the
	// client disconnects, the halt must still be recorded.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.store.SaveHalt(ctx, emitenID, price, reference, until); err != nil {
		s.log.Error("failed to persist halt; it will not survive a restart",
			"emiten_id", emitenID, "error", err)
	}
}

// Satisfies market.HaltObserver.
func (s *Supervisor) Resumed(emitenID int64) {
	s.log.Info("trading resumed", "emiten_id", emitenID)
}

// Run sweeps expired halts until ctx is cancelled.
//
// A single poll on one interval, rather than a timer per halt. The registry
// serializes every book behind one lock so that matching stays sequential and
// deterministic; a goroutine per halt would reopen a book from whichever thread
// the runtime happened to schedule, racing the matching path that lock exists to
// protect. One goroutine taking the lock once per tick keeps that guarantee, and
// costs a map scan over a few hundred emiten.
//
// Blocks until ctx is done, so the caller runs it in its own goroutine.
func (s *Supervisor) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sweep(ctx)
		}
	}
}

func (s *Supervisor) sweep(ctx context.Context) {
	for _, emitenID := range s.books.ExpireHalts() {
		s.Resumed(emitenID)

		if err := s.store.ClearHalt(ctx, emitenID); err != nil {
			// Logged, not retried. The row is already ignored by
			// LoadActiveHalts, which filters on resumes_at, and
			// PurgeExpiredHalts clears it at the next startup — so a failure
			// here leaves a stale row rather than a wrongly halted instrument.
			s.log.Error("failed to clear halt record",
				"emiten_id", emitenID, "error", err)
		}

		// Subscribers watching a halted instrument have had no book updates
		// since it stopped; without this they would wait for the next order to
		// discover it reopened.
		if s.hub != nil {
			if state, err := s.books.Snapshot(emitenID); err == nil {
				s.hub.Broadcast(emitenID, state)
			}
		}
	}
}
