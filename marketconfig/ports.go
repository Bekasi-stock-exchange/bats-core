// Package marketconfig owns the exchange's runtime trading parameters: the rules
// that apply to every order regardless of instrument, and the admin surface that
// reads and changes them.
//
// It exists because those rules had nowhere honest to live. An instrument's own
// properties belong on emiten, and deployment settings belong in the environment
// — but a trading rule is neither. It is a policy the exchange operator changes
// while the market is running, and one that must survive a restart, so it is
// stored in the database and cached in memory for the order path to read.
//
// The package depends on nothing above it. The order domain reads the cached
// settings through a narrow interface it declares itself, so the validation path
// never reaches into this package's service or its repository.
package marketconfig

import (
	"context"
	"time"
)

// Settings is the full set of exchange-wide trading parameters.
//
// It is a value, not a pointer, everywhere it is passed. The Cache hands out
// copies so a reader can never mutate the live configuration by holding onto
// what it was given.
type Settings struct {
	// MinPrice is the lowest price a limit order may be submitted at, in rupiah.
	//
	// The engine's only price rule was "> 0", which is not a market rule at all:
	// it let a seller quote 58, then 5, then 1, each one legal and each one
	// becoming the best ask for whatever arrived next. Bursa Efek Indonesia's
	// real floor is Rp 50, and below it a quote is not a cheap price but a
	// broken one.
	MinPrice int64

	// EmitenHaltBPS is the single-stock circuit breaker: how far one emiten's
	// price may move from its reference price within a session before trading in
	// that emiten is halted. In basis points — 3000 is 30%.
	//
	// This is not auto-rejection. ARA/ARB caps the price an order may carry and
	// rejects that order alone, leaving the market open; this threshold is
	// measured against trades that actually printed, and its consequence is that
	// the instrument stops trading for everyone holding it.
	EmitenHaltBPS int64

	// IndexHaltBPS is the market-wide circuit breaker: how far the index may fall
	// from its opening value before trading halts across every emiten at once. In
	// basis points — 1200 is 12%.
	IndexHaltBPS int64

	// HaltDuration is how long a triggered halt lasts before trading may resume.
	//
	// A duration rather than a number, so that every caller receives the unit
	// along with the value. The database stores seconds; the conversion happens
	// once, at the repository boundary, rather than at each of the call sites
	// that would otherwise each have to remember what the integer meant.
	HaltDuration time.Duration
}

// BPSDenominator is the number of basis points in 100%. Threshold percentages
// are stored as basis points so a comparison against a rupiah price stays in
// integer arithmetic: a 30% band on a price p is p*3000/10000, evaluated without
// ever constructing a float whose rounding could put a price on the wrong side
// of the boundary.
const BPSDenominator int64 = 10_000

// The configuration a fresh exchange starts with. These seed migration 015 and
// are what the Cache holds if it is read before Load runs.
const (
	// DefaultMinPrice is the Rp 50 floor, matching BEI's own minimum quotable
	// price.
	DefaultMinPrice int64 = 50

	// DefaultEmitenHaltBPS halts a single emiten once it has moved 30% from its
	// reference price.
	DefaultEmitenHaltBPS int64 = 3_000

	// DefaultIndexHaltBPS halts the whole market once the index has fallen 12%
	// from its opening value.
	DefaultIndexHaltBPS int64 = 1_200

	// DefaultHaltDuration is how long trading stops for once a breaker trips.
	DefaultHaltDuration = 2 * time.Minute
)

// MaxBPS is the largest threshold that can be configured: 100%. A threshold
// above it can never be reached, which is a disabled breaker written as though
// it were an enabled one — worse than no breaker, because it reads as
// protection. Mirrors the CHECK in migration 015.
const MaxBPS int64 = 10_000

// MaxHaltDuration is the longest halt that can be configured. Beyond one
// trading day a halt is not a halt but a suspension: a different decision, with
// a different approval path, that must not be reachable by mistyping this
// field. Mirrors the CHECK in migration 015.
const MaxHaltDuration = 24 * time.Hour

// Declared here, in the package that consumes it, and satisfied by the
// repository package — so this package depends on a behaviour rather than on a
// database handle.
type Repository interface {
	LoadSettings(ctx context.Context) (Settings, error)

	// SaveSettings overwrites the stored configuration and returns what was
	// written, so the caller refreshes its cache from the database's own view
	// rather than from what it hoped it wrote.
	SaveSettings(ctx context.Context, s Settings) (Settings, error)
}
