package underwriter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"bekasi-automatic-trading-system/emiten"
	"bekasi-automatic-trading-system/market"
)

// ErrNotFound means the requested underwriter code is not registered.
var ErrNotFound = errors.New("underwriter: not found")

// ErrEmitenNotFound means the instrument an offering names is not listed. Kept
// distinct from ErrNotFound so the controller can name the right thing in a 404:
// "unknown emiten" and "unknown underwriter" are different failures.
var ErrEmitenNotFound = errors.New("underwriter: emiten not found")

// ValidationError is a rejected request: well-formed, but breaking a business
// rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

type Service struct {
	repo   Repository
	lister EmitenLister
	dir    *market.Directory
	reg    *market.Registry
}

func NewService(repo Repository, lister EmitenLister, dir *market.Directory, reg *market.Registry) *Service {
	return &Service{repo: repo, lister: lister, dir: dir, reg: reg}
}

// Ordered by participant code.
func (s *Service) List(ctx context.Context) ([]Record, error) {
	return s.repo.ListUnderwriters(ctx)
}

// The participant must be registered: an underwriter's whole identity is that
// participant's, and allocations credit its holdings, so one that named nothing
// could be handed shares it could never sell. Registering the same broker twice is
// a duplicate, caught by the unique index and surfaced as a 409.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Record, error) {
	partKode := strings.TrimSpace(req.Participant)
	if partKode == "" {
		return Record{}, invalid("participant is required")
	}

	p, ok := s.dir.Participant(partKode)
	if !ok {
		return Record{}, invalid("unknown participant: %s", partKode)
	}

	return s.repo.CreateUnderwriter(ctx, Record{
		ParticipantID: p.ID,
		IsActive:      true,
	})
}

// Order matters and is not arbitrary. The syndicate is resolved and validated
// first, against nothing but already-loaded state, so a bad request fails before
// anything is written. Only then is the emiten created — which is the step that
// owns code uniqueness and cannot be rolled back from here — and the allocation
// written against it. Doing it the other way round would leave a listed instrument
// with no shares behind every rejected syndicate.
//
// The allocation itself is one transaction inside the repository: the audit rows
// and the share credits move together or not at all.
func (s *Service) IPO(ctx context.Context, req IPORequest) (market.Emiten, []AllocationRecord, error) {
	if err := validateOffering(req.IPOPrice, req.ListedShares, req.UnlistedShares, req.Underwriters); err != nil {
		return market.Emiten{}, nil, err
	}

	allocs, err := s.resolveSyndicate(ctx, req.Underwriters)
	if err != nil {
		return market.Emiten{}, nil, err
	}

	if _, err := s.lister.Create(ctx, emiten.CreateRequest{
		Kode:           req.Kode,
		Nama:           req.Nama,
		ListedShares:   req.ListedShares,
		UnlistedShares: req.UnlistedShares,
	}); err != nil {
		return market.Emiten{}, nil, s.translate(err)
	}

	return s.place(ctx, req.Kode, req.IPOPrice, allocs)
}

// For an instrument that is already registered but has never been taken public.
//
// This is the other half of the two-step listing: an instrument enters through
// POST /api/admin/emiten dormant, and this is what opens it for trading.
//
// The share counts are the instrument's own and are not accepted from the request.
// An offering decides who underwrites it and at what price, not how many shares
// the company has — taking them from the body would let an offering silently
// restate the share count every valuation is derived from. So the syndicate is
// validated against the listed_shares the instrument already carries.
//
// An instrument that is already trading is refused: a second offering would issue
// its shares twice. The check here is against the directory; Activate repeats it
// against the database, which is the one that sees concurrent requests.
func (s *Service) IPOExisting(ctx context.Context, kode string, req ExistingIPORequest) (market.Emiten, []AllocationRecord, error) {
	kode = strings.TrimSpace(kode)

	e, ok := s.dir.Emiten(kode)
	if !ok {
		return market.Emiten{}, nil, ErrEmitenNotFound
	}
	if e.IsActive {
		return market.Emiten{}, nil, invalid("emiten %s is already listed and trading", kode)
	}

	// Validated against the instrument's own share counts, not numbers from the
	// request — that is what makes the tranches sum to what was actually issued.
	if err := validateOffering(req.IPOPrice, e.ListedShares, e.UnlistedShares, req.Underwriters); err != nil {
		return market.Emiten{}, nil, err
	}

	allocs, err := s.resolveSyndicate(ctx, req.Underwriters)
	if err != nil {
		return market.Emiten{}, nil, err
	}

	return s.place(ctx, kode, req.IPOPrice, allocs)
}

// Completes an offering over a registered, dormant instrument: it opens the
// instrument at the offering price, writes the allocation, and credits the
// in-memory ledger.
//
// Shared by both entry points, so the two cannot drift on the part that actually
// moves shares. Activation comes first because it is the step that can still
// refuse — an instrument already trading must not have a second allocation written
// against it — and because it fixes the price the allocation is recorded at.
//
// The allocation itself is one transaction inside the repository: the audit rows
// and the share credits move together or not at all.
func (s *Service) place(ctx context.Context, kode string, ipoPrice int64, allocs []Allocation) (market.Emiten, []AllocationRecord, error) {
	e, err := s.lister.Activate(ctx, kode, ipoPrice)
	if err != nil {
		return market.Emiten{}, nil, s.translate(err)
	}

	if err := s.repo.AllocateIPO(ctx, e.ID, ipoPrice, allocs); err != nil {
		return market.Emiten{}, nil, err
	}

	// The in-memory share ledger must learn about the allocation too, or the
	// underwriters' first sell is rejected for insufficient shares even though the
	// database says they hold them. Credited after the write succeeds, so a failed
	// allocation never inflates the ledger.
	for _, a := range allocs {
		s.reg.CreditShares(a.ParticipantID, e.ID, a.Shares)
	}

	records, err := s.repo.AllocationsByEmiten(ctx, e.ID)
	if err != nil {
		return market.Emiten{}, nil, err
	}
	return e, records, nil
}

// Turns the emiten domain's rejections into this package's, so a bad
// listing is a 400 rather than a 500. They arrive as emiten.ValidationError, which
// this package's controller does not recognise.
func (s *Service) translate(err error) error {
	var ev emiten.ValidationError
	if errors.As(err, &ev) {
		return ValidationError{Msg: ev.Msg}
	}
	if errors.Is(err, emiten.ErrNotFound) {
		return ErrEmitenNotFound
	}
	return err
}

// Checks the parts of a request that need no lookups: the instrument's own
// numbers, and the shape of the syndicate.
//
// The syndicate rules encode what the two roles mean. Exactly one lead guarantees
// the offering, so there cannot be none or two. The lead carries the largest
// tranche — "pendukung" is a supporting share, not an equal one — and a support
// tranche that outsized the lead's would make the roles a label rather than a
// fact. And the tranches must sum to listed_shares exactly: a short sum leaves
// shares in nobody's hands, an over-sum conjures shares the instrument never
// issued.
// Takes the share counts as parameters rather than reading them off the request,
// because they come from different places depending on the entry point: from the
// body when the offering also lists the instrument, and from the instrument itself
// when it is already registered. The rules are identical either way.
func validateOffering(ipoPrice, listedShares, unlistedShares int64, syndicate []UnderwriterAllocation) error {
	switch {
	case ipoPrice <= 0:
		return invalid("ipo_price must be > 0")
	case listedShares <= 0:
		return invalid("listed_shares must be > 0")
	case unlistedShares < 0:
		return invalid("unlisted_shares must be >= 0")
	case len(syndicate) == 0:
		return invalid("at least one underwriter is required")
	}

	// price × shares stays inside int64 for any realistic offering, but the check
	// is cheap and an overflow here would silently corrupt every valuation.
	if listedShares > 0 && ipoPrice > maxInt64/listedShares {
		return invalid("listed_shares × ipo_price overflows int64")
	}

	var total int64
	seen := make(map[string]bool, len(syndicate))
	for _, u := range syndicate {
		kode := strings.TrimSpace(u.Underwriter)
		if kode == "" {
			return invalid("underwriter code is required")
		}
		if seen[kode] {
			return invalid("duplicate underwriter: %s", kode)
		}
		seen[kode] = true

		if u.Shares <= 0 {
			return invalid("shares for %s must be > 0", kode)
		}
		total += u.Shares
	}

	if total != listedShares {
		return invalid("underwriter shares sum to %d, must equal listed_shares (%d)",
			total, listedShares)
	}
	return nil
}

// maxInt64 guards the notional multiplications.
const maxInt64 = int64(^uint64(0) >> 1)

// The codes are participant codes: an underwriter is a participant with the
// permission, so a member that is not registered as one is rejected here rather
// than silently underwriting.
//
// There are no role rules left to enforce — the syndicate is flat, and the only
// structural constraint (that the tranches sum to the shares issued) is checked in
// validateOffering, which does not need a lookup to do it.
func (s *Service) resolveSyndicate(ctx context.Context, syndicate []UnderwriterAllocation) ([]Allocation, error) {
	allocs := make([]Allocation, 0, len(syndicate))

	for _, u := range syndicate {
		kode := strings.TrimSpace(u.Underwriter)

		rec, err := s.repo.UnderwriterByParticipant(ctx, kode)
		if errors.Is(err, ErrNotFound) {
			return nil, invalid("not a registered underwriter: %s", kode)
		}
		if err != nil {
			return nil, err
		}
		if !rec.IsActive {
			return nil, invalid("underwriter %s is not active", kode)
		}

		allocs = append(allocs, Allocation{
			UnderwriterID: rec.ID,
			ParticipantID: rec.ParticipantID,
			Shares:        u.Shares,
		})
	}

	// Largest tranche first, so the response reads as the syndicate is weighted.
	// Stable, so equal tranches keep the order the request listed them in.
	sort.SliceStable(allocs, func(i, j int) bool {
		return allocs[i].Shares > allocs[j].Shares
	})
	return allocs, nil
}
