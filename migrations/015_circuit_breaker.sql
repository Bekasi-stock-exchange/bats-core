-- Circuit breaker parameters, editable at runtime through /api/admin/config.
--
-- These extend market_config rather than forming a table of their own: they are
-- exchange-wide trading rules with the same lifecycle as min_price, changed by
-- the same operator through the same endpoint. A separate table would buy
-- nothing and cost a join on a row that is read as a unit.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run. ADD COLUMN IF NOT EXISTS carries a
-- DEFAULT so the columns land populated on an existing row, and the DEFAULT is
-- then kept rather than dropped so a hand-written INSERT that omits them still
-- produces a valid configuration.

-- ------------------------------------------------- emiten halt threshold
-- The single-stock circuit breaker: how far an emiten's price may move from its
-- reference price within a session before trading in it is halted.
--
-- Stored in basis points (1/100th of a percent) as an integer, not a percentage
-- as a float. A halt is a threshold comparison against a price, and price is
-- bigint rupiah everywhere in this schema; comparing it against a float
-- reintroduces the rounding question at exactly the boundary where the answer
-- matters most. 3000 bp = 30%.
--
-- Distinct from auto-rejection (ARA/ARB), which caps what price an order may
-- carry and rejects the order alone. This threshold is measured against trades
-- that actually happened, and its consequence is that the instrument stops
-- trading for everyone.
ALTER TABLE market_config
    ADD COLUMN IF NOT EXISTS emiten_halt_bps integer NOT NULL DEFAULT 3000
        CHECK (emiten_halt_bps > 0 AND emiten_halt_bps <= 10000);

-- -------------------------------------------------- index halt threshold
-- The market-wide circuit breaker: how far the index may fall from its opening
-- value before trading halts across every instrument at once.
--
-- Also basis points; 1200 bp = 12%.
--
-- The ceiling is 10000 bp (100%) for the same reason as above: a threshold the
-- index cannot reach is a disabled breaker written as if it were an enabled one,
-- and that is worse than no breaker because it reads as protection.
ALTER TABLE market_config
    ADD COLUMN IF NOT EXISTS index_halt_bps integer NOT NULL DEFAULT 1200
        CHECK (index_halt_bps > 0 AND index_halt_bps <= 10000);

-- --------------------------------------------------------- halt duration
-- How long a triggered halt lasts, in seconds, before trading may resume.
--
-- Seconds rather than minutes so the operator can express a duration shorter
-- than a minute without the column type forcing a rounding decision. 120 = the
-- two-minute halt this exchange runs.
--
-- The upper bound is one trading day. A halt longer than the session is not a
-- halt but a suspension, which is a different decision with a different approval
-- path, and letting it be typed into this field would let a slip of the keyboard
-- close the market for a week.
ALTER TABLE market_config
    ADD COLUMN IF NOT EXISTS halt_duration_seconds integer NOT NULL DEFAULT 120
        CHECK (halt_duration_seconds > 0 AND halt_duration_seconds <= 86400);
