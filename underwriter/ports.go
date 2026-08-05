// Package underwriter is the admin surface for penjamin emisi: the participants
// permitted to underwrite an offering, and the IPO endpoints that open an
// instrument and hand its shares to them.
//
// An underwriter is not a firm of its own — it is a participant with a permission.
// Registering one records that permission and nothing else; the code and name a
// reader sees are the participant's, joined on the way out.
//
// The syndicate is flat. Every member takes a tranche on the same terms, with no
// lead to elect and no rule about whose share is largest: the only constraint is
// that the tranches sum to exactly the shares being issued.
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

// Record is one underwriter as stored.
//
// It holds no code or name of its own. An underwriter *is* a participant that may
// underwrite, so its identity is that participant's — carrying a second copy here
// would be two sources of truth for one firm, free to drift apart. Readers join
// them from the directory instead.
//
// ParticipantID is also its trading identity: allocations move shares into
// broker_assets_list, which is keyed by participant, so an underwriter without one
// could be handed shares it could never sell.
type Record struct {
	ID            int64
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
	Shares        int64
}

// AllocationRecord is a stored allocation joined back to the participant code and
// name a reader wants.
type AllocationRecord struct {
	ParticipantKode string
	ParticipantNama string
	Shares          int64
	Price           int64
}

// Repository reads and writes underwriters and their IPO allocations.
//
// Declared here, in the package that consumes it, and satisfied by the repository
// package — so this package depends on a behaviour, not on a database handle.
type Repository interface {
	ListUnderwriters(ctx context.Context) ([]Record, error)

	// The broker code is the only code an underwriter has.
	UnderwriterByParticipant(ctx context.Context, kode string) (Record, error)

	CreateUnderwriter(ctx context.Context, u Record) (Record, error)

	// AllocateIPO writes the share allocations for an already-created emiten:
	// the ipo_allocation audit rows and the matching broker_assets_list credits,
	// in one transaction. A partial write here would hand out shares with no
	// record of why, or record an offering whose shares never moved.
	AllocateIPO(ctx context.Context, emitenID int64, price int64, allocs []Allocation) error

	// For the IPO detail view.
	AllocationsByEmiten(ctx context.Context, emitenID int64) ([]AllocationRecord, error)
}

// EmitenLister creates and opens the instrument an IPO lists.
//
// Satisfied by emiten.Service. Declared as an interface so this package drives the
// listing without reaching into the emiten domain's internals, and so the IPO
// service stays testable without a database.
//
// The two halves are separate calls because an instrument may be registered long
// before its offering runs: Create leaves it dormant, and Activate is what opens
// it for trading. An offering over an already-registered instrument uses only the
// second.
type EmitenLister interface {
	// Registers a dormant instrument. It is not tradeable on return.
	Create(ctx context.Context, req emiten.CreateRequest) (market.Emiten, error)

	// Opens a dormant instrument for trading at its offering price, refusing one
	// that is already active — which is what stops an instrument
	// being taken public twice.
	Activate(ctx context.Context, kode string, ipoPrice int64) (market.Emiten, error)
}
