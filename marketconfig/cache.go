package marketconfig

import (
	"sync"
	"time"
)

// Cache holds the live settings in memory.
//
// It exists so the order path does not query the database to validate a price.
// Every submitted order reads MinPrice, and settings change perhaps once a day —
// a read on the hot path against a write that is almost never issued, which is
// exactly the trade RWMutex is for. It mirrors market.Directory, which caches
// master data for the same reason.
//
// The cache is the single source of truth for readers, and the database is the
// single source of truth for the cache: Set is called only after a write commits,
// so a failed update can never leave the in-memory rule ahead of the stored one.
type Cache struct {
	mu       sync.RWMutex
	settings Settings
}

// NewCache returns a cache holding the built-in defaults.
//
// It is seeded rather than left zero deliberately. A zero MinPrice would mean
// "no floor" and a zero halt threshold would mean "halt on any movement at all",
// so a cache read before Load — a startup ordering mistake — would either
// silently disable a rule or trip a breaker on the first trade. Seeding makes
// that mistake enforce the shipped policy instead.
func NewCache() *Cache {
	return &Cache{settings: Settings{
		MinPrice:      DefaultMinPrice,
		EmitenHaltBPS: DefaultEmitenHaltBPS,
		IndexHaltBPS:  DefaultIndexHaltBPS,
		HaltDuration:  DefaultHaltDuration,
	}}
}

// Settings returns a copy of the current configuration.
func (c *Cache) Settings() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings
}

// MinPrice returns the current limit-order price floor. It is the one field the
// order path needs, exposed directly so the hot path takes the lock once and
// copies a single int rather than the whole struct.
func (c *Cache) MinPrice() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings.MinPrice
}

// HaltPolicy is the circuit breaker configuration, read as a unit.
//
// Grouped into one accessor rather than three because the breaker path reads
// them together and must see one consistent generation of the settings. Three
// separate reads could straddle an operator's update and combine an old
// threshold with a new duration — a combination that was never configured and
// that nothing in the audit trail would explain.
type HaltPolicy struct {
	EmitenBPS int64
	IndexBPS  int64
	Duration  time.Duration
}

// Halt returns the circuit breaker policy in force.
func (c *Cache) Halt() HaltPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return HaltPolicy{
		EmitenBPS: c.settings.EmitenHaltBPS,
		IndexBPS:  c.settings.IndexHaltBPS,
		Duration:  c.settings.HaltDuration,
	}
}

// EmitenBandBPS returns the single-emiten band threshold in basis points, and
// HaltDuration how long a triggered halt lasts. Together they satisfy the
// BreakerPolicy interface the market registry declares, so the registry reads
// live configuration without importing this package.
func (c *Cache) EmitenBandBPS() int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings.EmitenHaltBPS
}

// HaltDuration returns how long a triggered halt lasts.
func (c *Cache) HaltDuration() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.settings.HaltDuration
}

// Set replaces the cached configuration. Called at startup with what the
// database holds, and after every committed update.
func (c *Cache) Set(s Settings) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.settings = s
}
