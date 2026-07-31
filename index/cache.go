package index

import "sync"

// Cache holds the last computed level and the live definition in memory.
//
// It exists because the index is read far more often than it changes: every
// client polling the market wants the level, while it only moves when a trade
// executes. Recomputing per request would mean a full market valuation — one
// price query plus a pass over every instrument — on a path that is otherwise a
// single struct copy. It mirrors marketconfig.Cache and market.Directory, which
// cache for the same reason.
//
// The definition is cached alongside the level because the divisor is needed to
// compute, not just to report: a recomputation that had to read it from the
// database first would put a query back on the path this type exists to keep
// clear.
//
// Level is a pointer so that "not yet computed" is distinguishable from a level
// of zero. A zero-valued Level would report an index of 0.00 over 0 members,
// which is a real-looking answer to a question that has not been answered yet.
type Cache struct {
	mu    sync.RWMutex
	def   Definition
	level *Level
}

// NewCache returns an empty cache. It holds no defaults: unlike a price floor,
// there is no conservative index level to fall back on, so a read before the
// first computation correctly reports that there is nothing yet.
func NewCache() *Cache { return &Cache{} }

// Definition returns a copy of the cached index definition.
func (c *Cache) Definition() Definition {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.def
}

// SetDefinition replaces the cached definition. Called at startup with what the
// database holds, and after every committed divisor adjustment.
func (c *Cache) SetDefinition(d Definition) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.def = d
}

// Level returns the last computed level, or false if none has been computed yet.
func (c *Cache) Level() (Level, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.level == nil {
		return Level{}, false
	}
	return *c.level, true
}

// SetLevel replaces the cached level.
func (c *Cache) SetLevel(l Level) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.level = &l
}
