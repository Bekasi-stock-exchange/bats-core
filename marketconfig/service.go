package marketconfig

import (
	"context"
	"fmt"
	"time"
)

// ValidationError is a rejected update: well-formed JSON that breaks a business
// rule. Its message is returned to the client verbatim.
type ValidationError struct {
	Msg string
}

func (e ValidationError) Error() string { return e.Msg }

func invalid(format string, args ...any) error {
	return ValidationError{Msg: fmt.Sprintf(format, args...)}
}

// Service reads and updates the exchange's trading parameters.
type Service struct {
	repo  Repository
	cache *Cache
}

// NewService wires the config domain to its store and the cache the order path
// reads.
func NewService(repo Repository, cache *Cache) *Service {
	return &Service{repo: repo, cache: cache}
}

// Load reads the stored settings into the cache. Called once at startup, before
// the server accepts a request, so the first order is validated against the
// operator's configuration rather than the built-in default.
func (s *Service) Load(ctx context.Context) error {
	stored, err := s.repo.LoadSettings(ctx)
	if err != nil {
		return err
	}
	s.cache.Set(stored)
	return nil
}

// Current returns the configuration in force.
//
// Served from the cache, not the database. The cache is what the order path
// actually enforces, so reading it means this endpoint reports the rule that is
// really being applied rather than a stored value that might not have been
// loaded yet.
func (s *Service) Current() Settings { return s.cache.Settings() }

// Halt returns the circuit breaker policy in force, read from the cache for the
// same reason Current is: it is the configuration actually being enforced.
func (s *Service) Halt() HaltPolicy { return s.cache.Halt() }

// Update changes the trading parameters and returns the configuration now in
// force.
//
// The write comes before the cache update, and the cache is set from what the
// database returned rather than from the request. That ordering is what keeps
// the enforced rule and the stored rule from diverging: a failed write leaves
// the cache untouched, so the exchange carries on enforcing the last value that
// was actually persisted.
//
// Fields left absent in the request keep their current value, so an operator can
// change one parameter without restating the others — and without a zero in the
// JSON silently meaning "no floor".
func (s *Service) Update(ctx context.Context, req UpdateRequest) (Settings, error) {
	next := s.cache.Settings()

	if req.MinPrice != nil {
		next.MinPrice = *req.MinPrice
	}
	if req.EmitenHaltBPS != nil {
		next.EmitenHaltBPS = *req.EmitenHaltBPS
	}
	if req.IndexHaltBPS != nil {
		next.IndexHaltBPS = *req.IndexHaltBPS
	}
	if req.HaltDurationSeconds != nil {
		// Range-checked before the multiplication, not after. Converting first
		// would let a large value overflow time.Duration's int64 nanoseconds and
		// wrap to a negative, which then reads as a perfectly ordinary invalid
		// duration and hides what the operator actually typed from the error
		// message. Checking the seconds keeps the diagnosis honest.
		secs := *req.HaltDurationSeconds
		if secs <= 0 || secs > int64(MaxHaltDuration/time.Second) {
			return Settings{}, invalid(
				"halt_duration_seconds must be between 1 and %d",
				int64(MaxHaltDuration/time.Second))
		}
		next.HaltDuration = time.Duration(secs) * time.Second
	}

	if err := validate(next); err != nil {
		return Settings{}, err
	}

	saved, err := s.repo.SaveSettings(ctx, next)
	if err != nil {
		return Settings{}, err
	}
	s.cache.Set(saved)
	return saved, nil
}

// validate enforces the rules a settings row must satisfy, mirroring the CHECK
// constraints in migrations 013 and 015 so a bad value is a 400 here rather than
// a 500 from the database.
//
// A non-positive floor is rejected rather than treated as "disabled": price is
// quoted in whole rupiah, so a floor of 0 would readmit exactly the quotes the
// setting exists to keep out, and there is no reading of "minimum price 0" that
// is a deliberate exchange policy rather than a mistake.
//
// The halt thresholds are bounded at both ends for asymmetric reasons. Zero or
// negative is not a disabled breaker but one that trips on the first trade of
// the session, closing the market on a value nobody meant to set. Above 100% it
// can never trip at all — a breaker that exists in the configuration, reads as
// protection in every audit, and does nothing.
func validate(s Settings) error {
	if s.MinPrice <= 0 {
		return invalid("min_price must be > 0")
	}
	if s.EmitenHaltBPS <= 0 || s.EmitenHaltBPS > MaxBPS {
		return invalid("emiten_halt_bps must be between 1 and %d", MaxBPS)
	}
	if s.IndexHaltBPS <= 0 || s.IndexHaltBPS > MaxBPS {
		return invalid("index_halt_bps must be between 1 and %d", MaxBPS)
	}
	if s.HaltDuration <= 0 || s.HaltDuration > MaxHaltDuration {
		return invalid("halt_duration_seconds must be between 1 and %d",
			int64(MaxHaltDuration/time.Second))
	}
	// The column stores whole seconds, so a sub-second duration would be
	// truncated on write and read back as something the operator never set —
	// silently, and only visibly wrong once a breaker tripped for the wrong
	// length of time. Update's own range check already rejects out-of-range
	// input; this catches a fractional duration reaching validate by any other
	// route.
	if s.HaltDuration%time.Second != 0 {
		return invalid("halt duration must be a whole number of seconds")
	}
	return nil
}
