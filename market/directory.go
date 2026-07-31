package market

import (
	"sort"
	"sync"
)

// Emiten is a listed instrument.
//
// ListedShares is the publicly tradeable portion and UnlistedShares the
// restricted remainder; together they are the total shares outstanding, which is
// what free-float percentage and market cap are derived from.
//
// IsActive gates matching, not existence. An inactive emiten rejects new orders
// while its book and history stay readable, so positions do not become invisible.
//
// IPOPrice is the offering price the instrument was listed at, and is nil for the
// instruments that predate the column. It is the reference price until the first
// trade executes — see ReferencePrice — and is never rewritten afterwards, so the
// listing price stays auditable.
type Emiten struct {
	ID             int64
	Kode           string
	Nama           string
	ListedShares   int64
	UnlistedShares int64
	IsActive       bool
	IPOPrice       *int64
}

// TotalShares is the full share count outstanding.
func (e Emiten) TotalShares() int64 { return e.ListedShares + e.UnlistedShares }

// ReferencePrice resolves the price to value this instrument at, given its most
// recent execution price (nil when it has never traded).
//
// The market's own price wins whenever one exists; the IPO price only stands in
// until the first trade. Both may be absent — an instrument listed before the IPO
// price existed and never traded has no price at all — and nil is returned rather
// than 0, which would claim it is worth nothing.
//
// This lives on the type, not in a query, because both the emiten detail endpoint
// and the holdings valuation must agree on it: two COALESCEs in two SQL files
// drift, one method does not.
func (e Emiten) ReferencePrice(lastTrade *int64) *int64 {
	if lastTrade != nil {
		return lastTrade
	}
	return e.IPOPrice
}

// Participant is a broker (exchange participant).
type Participant struct {
	ID   int64
	Kode string
	Nama string
}

// Directory is the master-data lookup for emiten and participants.
//
// It is seeded at startup and can grow at runtime, because admin can create an
// emiten or a participant while the exchange is running. That is why it is
// locked: reads take RLock, the Add* methods take Lock. It was previously
// immutable and lock-free, and adding creation without the mutex would have been
// a data race against every in-flight request.
//
// Writes are rare (onboarding) and reads happen on nearly every request, so
// RWMutex is the right trade.
type Directory struct {
	mu           sync.RWMutex
	emitenByKode map[string]Emiten
	emitenByID   map[int64]Emiten
	partByKode   map[string]Participant
	partByID     map[int64]Participant
	emitens      []Emiten // kept sorted by Kode
}

// NewDirectory indexes master data by code and by id. The emiten slice is kept
// sorted so list endpoints never sort per request.
func NewDirectory(emitens []Emiten, participants []Participant) *Directory {
	d := &Directory{
		emitenByKode: make(map[string]Emiten, len(emitens)),
		emitenByID:   make(map[int64]Emiten, len(emitens)),
		partByKode:   make(map[string]Participant, len(participants)),
		partByID:     make(map[int64]Participant, len(participants)),
		emitens:      make([]Emiten, len(emitens)),
	}
	copy(d.emitens, emitens)
	sort.Slice(d.emitens, func(i, j int) bool { return d.emitens[i].Kode < d.emitens[j].Kode })

	for _, e := range emitens {
		d.emitenByKode[e.Kode] = e
		d.emitenByID[e.ID] = e
	}
	for _, p := range participants {
		d.partByKode[p.Kode] = p
		d.partByID[p.ID] = p
	}
	return d
}

// Emiten looks up a listed instrument by its code.
func (d *Directory) Emiten(kode string) (Emiten, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.emitenByKode[kode]
	return e, ok
}

// EmitenByID looks up a listed instrument by its id. Used to turn the ids stored
// on trades into codes without a database join.
func (d *Directory) EmitenByID(id int64) (Emiten, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.emitenByID[id]
	return e, ok
}

// Participant looks up a broker by its code.
func (d *Directory) Participant(kode string) (Participant, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.partByKode[kode]
	return p, ok
}

// ParticipantByID looks up a broker by its id.
func (d *Directory) ParticipantByID(id int64) (Participant, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.partByID[id]
	return p, ok
}

// Emitens returns every emiten, ordered by code.
//
// It returns a copy. Handing out the backing array would race with a concurrent
// AddEmiten appending to it — a hazard that did not exist while this type was
// immutable, and one no amount of locking inside this method would fix.
func (d *Directory) Emitens() []Emiten {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Emiten, len(d.emitens))
	copy(out, d.emitens)
	return out
}

// CountEmiten returns how many emiten exist, for pagination totals.
func (d *Directory) CountEmiten() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.emitens)
}

// AddEmiten registers a newly created emiten, keeping the sorted order.
func (d *Directory) AddEmiten(e Emiten) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.emitenByKode[e.Kode] = e
	d.emitenByID[e.ID] = e

	i := sort.Search(len(d.emitens), func(i int) bool { return d.emitens[i].Kode >= e.Kode })
	d.emitens = append(d.emitens, Emiten{})
	copy(d.emitens[i+1:], d.emitens[i:])
	d.emitens[i] = e
}

// AddParticipant registers a newly created broker.
func (d *Directory) AddParticipant(p Participant) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.partByKode[p.Kode] = p
	d.partByID[p.ID] = p
}
