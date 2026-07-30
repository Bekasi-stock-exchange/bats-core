package market

import "sort"

// Emiten is a listed instrument. ListedShares is used downstream to compute
// market cap for the index.
type Emiten struct {
	ID           int64
	Kode         string
	Nama         string
	ListedShares int64
}

// Participant is a broker (exchange participant).
type Participant struct {
	ID   int64
	Kode string
	Nama string
}

// Directory is the master-data lookup for emiten and participants.
//
// It is loaded once at startup and immutable afterwards, which is why it needs no
// lock. Adding a reload path would require one — the immutability is what makes
// concurrent reads safe.
type Directory struct {
	emitenByKode map[string]Emiten
	partByKode   map[string]Participant
	emitens      []Emiten // sorted by Kode
}

// NewDirectory indexes master data by code. The emiten slice is sorted once here
// so list endpoints need not sort on every request.
func NewDirectory(emitens []Emiten, participants []Participant) *Directory {
	d := &Directory{
		emitenByKode: make(map[string]Emiten, len(emitens)),
		partByKode:   make(map[string]Participant, len(participants)),
		emitens:      make([]Emiten, len(emitens)),
	}
	copy(d.emitens, emitens)
	sort.Slice(d.emitens, func(i, j int) bool { return d.emitens[i].Kode < d.emitens[j].Kode })

	for _, e := range emitens {
		d.emitenByKode[e.Kode] = e
	}
	for _, p := range participants {
		d.partByKode[p.Kode] = p
	}
	return d
}

// Emiten looks up a listed instrument by its code.
func (d *Directory) Emiten(kode string) (Emiten, bool) {
	e, ok := d.emitenByKode[kode]
	return e, ok
}

// Participant looks up a broker by its code.
func (d *Directory) Participant(kode string) (Participant, bool) {
	p, ok := d.partByKode[kode]
	return p, ok
}

// Emitens returns every emiten, ordered by code. Callers must not mutate the
// result.
func (d *Directory) Emitens() []Emiten {
	return d.emitens
}

// CountEmiten returns how many emiten exist, for pagination totals.
func (d *Directory) CountEmiten() int {
	return len(d.emitens)
}
