// Package underwriter is the admin surface for penjamin emisi: the firms that
// guarantee an offering, and the IPO endpoint that lists an instrument and hands
// its shares to them in one step.
//
// It sits above emiten rather than beside it: an IPO *is* a listing plus an
// allocation, so this package drives emiten.Service for the listing half and owns
// only the syndicate and the share hand-out.
package underwriter

import (
	"context"

	"bekasi-automatic-trading-system/emiten"
	"bekasi-automatic-trading-system/market"
)

// Jenis is an underwriter's role. Only two exist, and the database CHECK mirrors
// them — a third value is a schema change, not a new string.
type Jenis string

const (
	// Utama is the lead underwriter, which guarantees the offering.
	Utama Jenis = "utama"
	// Pendukung is a supporting syndicate member.
	Pendukung Jenis = "pendukung"
)

// Valid reports whether j is one of the two defined roles.
func (j Jenis) Valid() bool { return j == Utama || j == Pendukung }

// Record is one underwriter as stored.
//
// ParticipantID is its trading identity: allocations move shares into
// broker_assets_list, which is keyed by participant, so an underwriter without one
// could be handed shares it could never sell.
type Record struct {
	ID            int64
	Kode          string
	Nama          string
	Jenis         Jenis
	ParticipantID int64
	IsActive      bool
}

// Allocation is one underwriter's tranche of an offering, resolved for writing.
//
// It carries ParticipantID alongside UnderwriterID because the share movement and
// the audit row are written in the same transaction and must not disagree about
// who received the shares.
type Allocation struct {
	UnderwriterID int64
	ParticipantID int64
	Jenis         Jenis
	Shares        int64
}

// AllocationRecord is a stored allocation joined back to the names a reader wants.
type AllocationRecord struct {
	UnderwriterKode string
	UnderwriterNama string
	ParticipantKode string
	Jenis           Jenis
	Shares          int64
	Price           int64
}

// Repository reads and writes underwriters and their IPO allocations.
//
// Declared here, in the package that consumes it, and satisfied by the repository
// package — so this package depends on a behaviour, not on a database handle.
type Repository interface {
	ListUnderwriters(ctx context.Context) ([]Record, error)
	UnderwriterByKode(ctx context.Context, kode string) (Record, error)
	CreateUnderwriter(ctx context.Context, u Record) (Record, error)

	// AllocateIPO writes the share allocations for an already-created emiten:
	// the ipo_allocation audit rows and the matching broker_assets_list credits,
	// in one transaction. A partial write here would hand out shares with no
	// record of why, or record an offering whose shares never moved.
	AllocateIPO(ctx context.Context, emitenID int64, price int64, allocs []Allocation) error

	// AllocationsByEmiten returns an offering's syndicate, for the IPO detail view.
	AllocationsByEmiten(ctx context.Context, emitenID int64) ([]AllocationRecord, error)
}

// EmitenLister creates the instrument an IPO lists.
//
// Satisfied by emiten.Service. Declared as an interface so this package drives the
// listing without reaching into the emiten domain's internals, and so the IPO
// service stays testable without a database.
type EmitenLister interface {
	Create(ctx context.Context, req emiten.CreateRequest) (market.Emiten, error)
}
