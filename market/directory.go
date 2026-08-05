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
// SessionReference is the anchor the price band is measured from, frozen for
// the session, and nil for an instrument that has no anchor yet.
//
// Deliberately not the same thing as the ReferencePrice method below. That
// method answers "what is this worth right now" and moves with every execution,
// which is correct for valuation and wrong for a band: anchored to the last
// trade, a 30% limit lets 190 admit 247, which admits 321, which admits 417 —
// each step legal, and the instrument walks to 1000 without one rejection. The
// band holds still for the session so the cumulative move is what is bounded.
type Emiten struct {
	ID               int64
	Kode             string
	Nama             string
	ListedShares     int64
	UnlistedShares   int64
	IsActive         bool
	IPOPrice         *int64
	SessionReference *int64
}

func (e Emiten) TotalShares() int64 { return e.ListedShares + e.UnlistedShares }

// lastTrade is the most recent execution price, nil when it has never traded.
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
// Seeded at startup and can grow at runtime, because admin can create an
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

// Indexes master data by code and by id. The emiten slice is kept sorted so
// list endpoints never sort per request.
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

func (d *Directory) Emiten(kode string) (Emiten, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.emitenByKode[kode]
	return e, ok
}

// Used to turn the ids stored on trades into codes without a database join.
func (d *Directory) EmitenByID(id int64) (Emiten, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	e, ok := d.emitenByID[id]
	return e, ok
}

func (d *Directory) Participant(kode string) (Participant, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.partByKode[kode]
	return p, ok
}

func (d *Directory) ParticipantByID(id int64) (Participant, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	p, ok := d.partByID[id]
	return p, ok
}

// Returns every emiten ordered by code — and returns a copy. Handing out the backing array would race with a concurrent
// AddEmiten appending to it — a hazard that did not exist while this type was
// immutable, and one no amount of locking inside this method would fix.
func (d *Directory) Emitens() []Emiten {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]Emiten, len(d.emitens))
	copy(out, d.emitens)
	return out
}

// For pagination totals.
func (d *Directory) CountEmiten() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.emitens)
}

// Keeps the sorted order.
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

// Reports whether the code was found.
//
// It exists because an instrument is created dormant and only opens for trading
// once its shares have been placed — see the emiten service. The IPO price is set
// here rather than at creation for the same reason: the offering price is decided
// when the offering runs, not when the instrument is first registered.
//
// The three maps hold copies of the struct, not pointers, so each must be updated
// in turn; missing one would leave a reader seeing a stale IsActive depending on
// which lookup it happened to use.
func (d *Directory) ActivateEmiten(kode string, ipoPrice int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, ok := d.emitenByKode[kode]
	if !ok {
		return false
	}

	e.IsActive = true
	e.IPOPrice = &ipoPrice

	d.emitenByKode[e.Kode] = e
	d.emitenByID[e.ID] = e
	for i := range d.emitens {
		if d.emitens[i].ID == e.ID {
			d.emitens[i] = e
			break
		}
	}
	return true
}

// Applied after a corporate action; reports whether the code was found.
//
// A split or bonus changes what a share *is*, so both move together: the count of
// shares outstanding, and the reference the price band is measured from. Leaving
// the anchor behind would auto-reject every order at the post-split fair value —
// a 1:2 split halves the traded price, and a band still centred on the old
// reference would refuse it. A nil reference leaves the anchor alone, which is an
// instrument that never had one.
//
// UnlistedShares is deliberately untouched. A corporate action here is announced
// over the listed float, and restating the restricted portion as well would issue
// shares against a holding this engine does not track.
//
// The three maps hold copies of the struct, not pointers, so each must be updated
// in turn — the same reason ActivateEmiten does.
func (d *Directory) RestateShares(kode string, listedShares int64, reference *int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	e, ok := d.emitenByKode[kode]
	if !ok {
		return false
	}

	e.ListedShares = listedShares
	if reference != nil {
		e.SessionReference = reference
	}

	d.emitenByKode[e.Kode] = e
	d.emitenByID[e.ID] = e
	for i := range d.emitens {
		if d.emitens[i].ID == e.ID {
			d.emitens[i] = e
			break
		}
	}
	return true
}

func (d *Directory) AddParticipant(p Participant) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.partByKode[p.Kode] = p
	d.partByID[p.ID] = p
}
