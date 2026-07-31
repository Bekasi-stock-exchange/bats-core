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

// ValidationError is a rejected request: well-formed, but breaking a business
// rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Service registers underwriters and runs offerings.
type Service struct {
	repo   Repository
	lister EmitenLister
	dir    *market.Directory
	reg    *market.Registry
}

// NewService wires the underwriter domain to the emiten domain and the market
// kernel.
func NewService(repo Repository, lister EmitenLister, dir *market.Directory, reg *market.Registry) *Service {
	return &Service{repo: repo, lister: lister, dir: dir, reg: reg}
}

// List returns every registered underwriter, ordered by code.
func (s *Service) List(ctx context.Context) ([]Record, error) {
	return s.repo.ListUnderwriters(ctx)
}

// Create registers a new underwriter against an existing broker.
func (s *Service) Create(ctx context.Context, req CreateRequest) (Record, error) {
	kode := strings.TrimSpace(req.Kode)
	nama := strings.TrimSpace(req.Nama)
	jenis := Jenis(strings.TrimSpace(req.Jenis))
	partKode := strings.TrimSpace(req.Participant)

	switch {
	case kode == "":
		return Record{}, invalid("kode is required")
	case nama == "":
		return Record{}, invalid("nama is required")
	case !jenis.Valid():
		return Record{}, invalid(`jenis must be "utama" or "pendukung"`)
	case partKode == "":
		return Record{}, invalid("participant is required")
	}

	p, ok := s.dir.Participant(partKode)
	if !ok {
		return Record{}, invalid("unknown participant: %s", partKode)
	}

	return s.repo.CreateUnderwriter(ctx, Record{
		Kode:          kode,
		Nama:          nama,
		Jenis:         jenis,
		ParticipantID: p.ID,
		IsActive:      true,
	})
}

// IPO lists an instrument and hands its shares to the underwriting syndicate.
//
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
	if err := validateOffering(req); err != nil {
		return market.Emiten{}, nil, err
	}

	allocs, err := s.resolveSyndicate(ctx, req)
	if err != nil {
		return market.Emiten{}, nil, err
	}

	e, err := s.lister.Create(ctx, emiten.CreateRequest{
		Kode:           req.Kode,
		Nama:           req.Nama,
		ListedShares:   req.ListedShares,
		UnlistedShares: req.UnlistedShares,
		IPOPrice:       req.IPOPrice,
	})
	if err != nil {
		// The listing's own rejections are this request's rejections too, but they
		// arrive as emiten.ValidationError, which this package's controller does not
		// recognise — translated here so a bad kode is a 400 rather than a 500.
		var ev emiten.ValidationError
		if errors.As(err, &ev) {
			return market.Emiten{}, nil, ValidationError{Msg: ev.Msg}
		}
		return market.Emiten{}, nil, err
	}

	if err := s.repo.AllocateIPO(ctx, e.ID, req.IPOPrice, allocs); err != nil {
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

// validateOffering checks the parts of a request that need no lookups: the
// instrument's own numbers, and the shape of the syndicate.
//
// The syndicate rules encode what the two roles mean. Exactly one lead guarantees
// the offering, so there cannot be none or two. The lead carries the largest
// tranche — "pendukung" is a supporting share, not an equal one — and a support
// tranche that outsized the lead's would make the roles a label rather than a
// fact. And the tranches must sum to listed_shares exactly: a short sum leaves
// shares in nobody's hands, an over-sum conjures shares the instrument never
// issued.
func validateOffering(req IPORequest) error {
	switch {
	case req.IPOPrice <= 0:
		return invalid("ipo_price must be > 0")
	case req.ListedShares <= 0:
		return invalid("listed_shares must be > 0")
	case req.UnlistedShares < 0:
		return invalid("unlisted_shares must be >= 0")
	case len(req.Underwriters) == 0:
		return invalid("at least one underwriter is required")
	}

	// price × shares stays inside int64 for any realistic offering, but the check
	// is cheap and an overflow here would silently corrupt every valuation.
	if req.ListedShares > 0 && req.IPOPrice > maxInt64/req.ListedShares {
		return invalid("listed_shares × ipo_price overflows int64")
	}

	var total int64
	seen := make(map[string]bool, len(req.Underwriters))
	for _, u := range req.Underwriters {
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

	if total != req.ListedShares {
		return invalid("underwriter shares sum to %d, must equal listed_shares (%d)",
			total, req.ListedShares)
	}
	return nil
}

// maxInt64 guards the notional multiplications.
const maxInt64 = int64(^uint64(0) >> 1)

// resolveSyndicate turns underwriter codes into allocations, and enforces the
// rules that need the stored role: exactly one lead, and the lead holding the
// largest tranche.
func (s *Service) resolveSyndicate(ctx context.Context, req IPORequest) ([]Allocation, error) {
	allocs := make([]Allocation, 0, len(req.Underwriters))
	var leads, leadShares, maxSupport int64

	for _, u := range req.Underwriters {
		kode := strings.TrimSpace(u.Underwriter)

		rec, err := s.repo.UnderwriterByKode(ctx, kode)
		if errors.Is(err, ErrNotFound) {
			return nil, invalid("unknown underwriter: %s", kode)
		}
		if err != nil {
			return nil, err
		}
		if !rec.IsActive {
			return nil, invalid("underwriter %s is not active", kode)
		}

		if rec.Jenis == Utama {
			leads++
			leadShares = u.Shares
		} else if u.Shares > maxSupport {
			maxSupport = u.Shares
		}

		allocs = append(allocs, Allocation{
			UnderwriterID: rec.ID,
			ParticipantID: rec.ParticipantID,
			Jenis:         rec.Jenis,
			Shares:        u.Shares,
		})
	}

	switch {
	case leads == 0:
		return nil, invalid("an offering needs one underwriter with jenis \"utama\"")
	case leads > 1:
		return nil, invalid("an offering takes exactly one \"utama\" underwriter, got %d", leads)
	case leadShares < maxSupport:
		return nil, invalid(
			"the \"utama\" underwriter must take the largest tranche: got %d against a \"pendukung\" tranche of %d",
			leadShares, maxSupport)
	}

	// Lead first, then the supporters largest-first, so the response reads as the
	// syndicate is actually structured.
	sort.SliceStable(allocs, func(i, j int) bool {
		if (allocs[i].Jenis == Utama) != (allocs[j].Jenis == Utama) {
			return allocs[i].Jenis == Utama
		}
		return allocs[i].Shares > allocs[j].Shares
	})
	return allocs, nil
}
