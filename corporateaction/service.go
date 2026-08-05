package corporateaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"bekasi-automatic-trading-system/market"
)

// ErrNotFound means the requested action id does not exist.
var ErrNotFound = errors.New("corporateaction: not found")

// ErrEmitenNotFound means the instrument an action names is not listed. Kept
// distinct from ErrNotFound so the controller can name the right thing in a 404.
var ErrEmitenNotFound = errors.New("corporateaction: emiten not found")

// ErrNotAnnounced means the action has already been executed or cancelled, so the
// requested transition is not available. Both endpoints that change status return
// it, and it is a 409 rather than a 404: the action exists, it is simply past the
// point where this was possible.
var ErrNotAnnounced = errors.New("corporateaction: action is not announced")

// ValidationError is a rejected request: well-formed, but breaking a business
// rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// dateLayout is the wire format for record_date: a calendar day, with no time and
// no zone, because that is what an issuer announces.
const dateLayout = "2006-01-02"

// maxInt64 guards the ratio multiplications.
const maxInt64 = int64(^uint64(0) >> 1)

type Service struct {
	repo   Repository
	emiten EmitenReader
	dir    *market.Directory
	reg    *market.Registry
}

func NewService(repo Repository, em EmitenReader, dir *market.Directory, reg *market.Registry) *Service {
	return &Service{repo: repo, emiten: em, dir: dir, reg: reg}
}

// Nothing moves here. The terms are validated fully — including the arithmetic
// that execution will perform — so that an action which cannot be executed is
// refused now rather than at the moment the market is waiting for it.
//
// The instrument must be active. An action over a dormant instrument would
// restate holdings nobody has, and a dividend over one would pay for shares that
// were never placed.
func (s *Service) Announce(ctx context.Context, req AnnounceRequest) (Record, error) {
	kode := strings.TrimSpace(req.Kode)
	if kode == "" {
		return Record{}, invalid("kode is required")
	}

	e, ok := s.dir.Emiten(kode)
	if !ok {
		return Record{}, ErrEmitenNotFound
	}
	if !e.IsActive {
		return Record{}, invalid("emiten %s is not trading yet; run its IPO first", kode)
	}

	jenis := Jenis(strings.ToUpper(strings.TrimSpace(req.Jenis)))
	if !jenis.Valid() {
		return Record{}, invalid("jenis must be one of SPLIT, REVERSE_SPLIT, BONUS, DIVIDEND")
	}

	rec := Record{
		EmitenID:   e.ID,
		Jenis:      jenis,
		Status:     Announced,
		Keterangan: strings.TrimSpace(req.Keterangan),
	}

	if err := applyTerms(&rec, req, e); err != nil {
		return Record{}, err
	}

	if req.RecordDate != "" {
		d, err := time.Parse(dateLayout, strings.TrimSpace(req.RecordDate))
		if err != nil {
			return Record{}, invalid("record_date must be YYYY-MM-DD")
		}
		rec.RecordDate = &d
	}

	return s.repo.CreateAction(ctx, rec)
}

// The two branches are mutually exclusive by design: sending a ratio with a
// dividend, or an amount with a split, is refused rather than ignored. An
// operator who does that has misunderstood the request, and accepting it silently
// would carry the misunderstanding through to execution.
func applyTerms(rec *Record, req AnnounceRequest, e market.Emiten) error {
	if rec.Jenis == Dividend {
		switch {
		case req.Amount <= 0:
			return invalid("amount must be > 0 for a dividend")
		case req.RatioFrom != 0 || req.RatioTo != 0:
			return invalid("ratio_from and ratio_to do not apply to a dividend")
		}
		// The whole payout must stay inside int64, or the credits silently wrap.
		if e.ListedShares > 0 && req.Amount > maxInt64/e.ListedShares {
			return invalid("amount × listed_shares overflows int64")
		}
		amount := req.Amount
		rec.Amount = &amount
		return nil
	}

	switch {
	case req.RatioFrom <= 0 || req.RatioTo <= 0:
		return invalid("ratio_from and ratio_to must both be > 0 for %s", rec.Jenis)
	case req.Amount != 0:
		return invalid("amount does not apply to %s", rec.Jenis)
	}

	// The direction has to match the kind, or the announcement says one thing and
	// does another: a "split" that shrinks every holding is a reverse split
	// mislabelled, and an operator reading the listing would plan against the
	// wrong event.
	switch rec.Jenis {
	case Split:
		if req.RatioTo <= req.RatioFrom {
			return invalid("a split must increase the share count: ratio_to must be > ratio_from")
		}
	case ReverseSplit:
		if req.RatioTo >= req.RatioFrom {
			return invalid("a reverse split must reduce the share count: ratio_to must be < ratio_from")
		}
	case Bonus:
		if req.RatioTo <= req.RatioFrom {
			return invalid("a bonus must increase the share count: ratio_to must be > ratio_from")
		}
	}

	if e.ListedShares > 0 && req.RatioTo > maxInt64/e.ListedShares {
		return invalid("listed_shares × ratio_to overflows int64")
	}

	from, to := req.RatioFrom, req.RatioTo
	rec.RatioFrom = &from
	rec.RatioTo = &to
	return nil
}

func (s *Service) List(ctx context.Context, kode string, page, limit int) ([]Record, int, error) {
	var emitenID *int64
	if kode = strings.TrimSpace(kode); kode != "" {
		e, ok := s.dir.Emiten(kode)
		if !ok {
			return nil, 0, ErrEmitenNotFound
		}
		emitenID = &e.ID
	}

	total, err := s.repo.CountActions(ctx, emitenID)
	if err != nil {
		return nil, 0, err
	}

	recs, err := s.repo.ListActions(ctx, emitenID, limit, (page-1)*limit)
	if err != nil {
		return nil, 0, err
	}
	return recs, total, nil
}

// The entries are empty for an action that has not executed, which is the honest
// answer rather than an error: the action exists and is readable, nobody has
// simply received anything from it yet.
func (s *Service) Detail(ctx context.Context, id int64) (Record, []Entry, error) {
	rec, err := s.repo.FindAction(ctx, id)
	if err != nil {
		return Record{}, nil, err
	}

	entries, err := s.repo.EntriesByAction(ctx, id)
	if err != nil {
		return Record{}, nil, err
	}
	return rec, entries, nil
}

// The row is kept rather than deleted: participants were told about the action,
// so its cancellation is part of the instrument's history and deleting it would
// leave the record disagreeing with what the market was told.
//
// The announced-status check lives in the repository's WHERE clause, not here.
// Only the database sees two concurrent requests, and a check in this process
// would let a cancel and an execute both pass before either wrote.
func (s *Service) Cancel(ctx context.Context, id int64) (Record, error) {
	if err := s.repo.CancelAction(ctx, id); err != nil {
		return Record{}, err
	}
	return s.repo.FindAction(ctx, id)
}

// This is the irreversible half. It reads the holders as they stand, computes
// what each receives, writes every movement in one transaction, and only then
// moves the in-memory ledger into agreement — the same order the IPO allocation
// uses, and for the same reason: crediting memory before the write is durable
// would let a broker trade on shares or cash the database never recorded.
//
// The status check is the repository's, in the UPDATE that marks the action
// executed. A check here would not survive two concurrent execute requests, and
// executing an action twice would double every holding in the instrument.
func (s *Service) Execute(ctx context.Context, id int64) (Record, []Entry, error) {
	rec, err := s.repo.FindAction(ctx, id)
	if err != nil {
		return Record{}, nil, err
	}
	if rec.Status != Announced {
		return Record{}, nil, ErrNotAnnounced
	}

	e, ok := s.dir.EmitenByID(rec.EmitenID)
	if !ok {
		return Record{}, nil, ErrEmitenNotFound
	}

	holders, err := s.repo.HoldersOf(ctx, rec.EmitenID)
	if err != nil {
		return Record{}, nil, err
	}
	if len(holders) == 0 {
		return Record{}, nil, invalid("emiten %s has no holders to distribute to", e.Kode)
	}

	if rec.Jenis == Dividend {
		return s.executeDividend(ctx, rec, e, holders)
	}
	return s.executeShares(ctx, rec, e, holders)
}

// No share count changes and no reference price moves: this engine's reference
// price is the anchor the circuit breaker measures against, and a dividend is not
// a restatement of what a share *is* — the market re-prices it by trading, which
// is exactly what the band is there to bound.
func (s *Service) executeDividend(ctx context.Context, rec Record, e market.Emiten, holders []Holding) (Record, []Entry, error) {
	amount := *rec.Amount

	entries := make([]Entry, 0, len(holders))
	for _, h := range holders {
		// Guarded at announcement against listed_shares; re-checked per holder
		// because holdings are read now and a position could in principle exceed
		// what was listed then.
		if h.Shares > 0 && amount > maxInt64/h.Shares {
			return Record{}, nil, invalid("dividend for participant %d overflows int64", h.ParticipantID)
		}
		entries = append(entries, Entry{
			ParticipantID: h.ParticipantID,
			SharesBefore:  h.Shares,
			SharesAfter:   h.Shares, // a dividend restates nothing
			CashAmount:    h.Shares * amount,
		})
	}

	if err := s.repo.ExecuteDividend(ctx, rec.ID, entries); err != nil {
		return Record{}, nil, err
	}

	// The in-memory cash ledger must learn about the credits too, or a broker's
	// first buy after a dividend is rejected for insufficient balance even though
	// the database says it holds the money. Applied after the write succeeds, so
	// a failed payout never inflates the ledger.
	//
	// A credit is never refused by AdjustCash, so the error it returns for a debit
	// cannot occur here.
	for _, en := range entries {
		if en.CashAmount > 0 {
			_, _ = s.reg.AdjustCash(en.ParticipantID, en.CashAmount)
		}
	}

	return s.reload(ctx, rec.ID)
}

// Every holding is restated by the ratio, and so is the instrument's
// listed_shares. The two must agree: an instrument whose share count does not
// equal the sum of its holders' positions makes every market-cap and free-float
// figure derived from it wrong, which is why they move in one transaction.
//
// listed_shares is recomputed from the ratio rather than summed from the
// restated holdings. The two would disagree wherever a holding truncated (see
// restate), and the instrument's own count is the authority on how many shares
// were issued — the truncation is a loss to the holder, not an un-issuing of
// shares. It also keeps the count correct when part of the float is held
// somewhere this ledger does not see.
func (s *Service) executeShares(ctx context.Context, rec Record, e market.Emiten, holders []Holding) (Record, []Entry, error) {
	from, to := *rec.RatioFrom, *rec.RatioTo

	entries := make([]Entry, 0, len(holders))
	for _, h := range holders {
		after, err := restate(h.Shares, from, to)
		if err != nil {
			return Record{}, nil, err
		}
		entries = append(entries, Entry{
			ParticipantID: h.ParticipantID,
			SharesBefore:  h.Shares,
			SharesAfter:   after,
			CashAmount:    0,
		})
	}

	listedAfter, err := restate(e.ListedShares, from, to)
	if err != nil {
		return Record{}, nil, err
	}

	// The band anchor is restated by the inverse ratio, because the instrument is
	// worth the same immediately after a split as immediately before it. Leaving
	// it alone would be worse than cosmetic: a 1:2 split halves the traded price,
	// and a band still anchored to the pre-split reference would auto-reject every
	// order at the new fair value — or, on a reverse split, admit a doubling
	// without complaint. Nil when the instrument has no anchor yet, which stays
	// nil: there is nothing to restate.
	var reference *int64
	if e.SessionReference != nil {
		adjusted := *e.SessionReference * from / to
		if adjusted < 1 {
			adjusted = 1 // a price of 0 would disable the band entirely
		}
		reference = &adjusted
	}

	if err := s.repo.ExecuteShareAction(ctx, rec.ID, rec.EmitenID, listedAfter, reference, entries); err != nil {
		return Record{}, nil, err
	}

	// The in-memory state must follow the write, or the instrument keeps trading
	// against its pre-split share ledger: a broker whose holding doubled would
	// still be refused the sell, and the band would still reject orders at the new
	// price. Applied only after the transaction commits.
	if err := s.emiten.RestateShares(ctx, rec.EmitenID, listedAfter, reference); err != nil {
		return Record{}, nil, err
	}
	for _, en := range entries {
		s.reg.CreditShares(en.ParticipantID, rec.EmitenID, en.SharesAfter-en.SharesBefore)
	}

	return s.reload(ctx, rec.ID)
}

// Integer division truncates, and that is deliberate: a share is indivisible, so
// a 2:3 bonus on an odd holding cannot hand out the half share the arithmetic
// asks for. Real exchanges settle the remainder in cash; this engine has no
// mechanism for that, so the fraction is dropped — the holder keeps whole shares
// only. Rounding up instead would issue shares the action never authorised.
//
// The multiplication is checked because it happens before the division, which is
// what keeps the result exact for every holding the division does divide evenly.
func restate(shares, from, to int64) (int64, error) {
	if shares == 0 {
		return 0, nil
	}
	if shares > maxInt64/to {
		return 0, invalid("restating %d shares by %d:%d overflows int64", shares, from, to)
	}
	return shares * to / from, nil
}

// Re-read after execution so the response reports what was actually committed —
// including the executed_at the database stamped — rather than what this process
// believed it wrote.
func (s *Service) reload(ctx context.Context, id int64) (Record, []Entry, error) {
	rec, err := s.repo.FindAction(ctx, id)
	if err != nil {
		return Record{}, nil, err
	}
	entries, err := s.repo.EntriesByAction(ctx, id)
	if err != nil {
		return Record{}, nil, err
	}
	return rec, entries, nil
}
