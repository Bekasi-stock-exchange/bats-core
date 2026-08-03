-- The per-emiten reference price: the anchor auto-rejection and the single-stock
-- circuit breaker measure against.
--
-- This is deliberately NOT the same thing as market.Emiten.ReferencePrice(), the
-- method that resolves "what is this instrument worth right now" by preferring
-- the last trade and falling back to the IPO price. That is a valuation, and it
-- moves with every execution.
--
-- A price band cannot be anchored to something that moves with every execution.
-- If the band were measured from the last trade, then with a 30% limit a price
-- of 190 admits 247, which then admits 321, which admits 417 — each step legal
-- on its own, and the instrument walks from 190 to 1000 in five orders without a
-- single rejection. The whole point of the band is that it is fixed for the
-- session, so the cumulative move is bounded rather than the per-order one.
--
-- Hence a stored column, frozen at the session boundary, rather than a query
-- over trades.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

-- ------------------------------------------------------- reference_price
-- Nullable, with no default. An instrument that has never traded and was listed
-- before ipo_price existed genuinely has no anchor, and inventing one — 0, or
-- the current best bid — would either reject every order or anchor the band to a
-- number nobody set. NULL means "no band applies yet", which the order path
-- honours by letting the order through.
ALTER TABLE emiten
    ADD COLUMN IF NOT EXISTS reference_price bigint CHECK (reference_price > 0);

-- Seed the anchor for instruments that already have an offering price. This is
-- the correct opening reference for a freshly listed instrument: on its first
-- trading day, the band is measured from what it was sold at.
--
-- Only fills the gap; an instrument whose reference has already been set by a
-- session roll is left alone, so re-running the migration never rewinds a live
-- band to the listing price.
UPDATE emiten
   SET reference_price = ipo_price
 WHERE reference_price IS NULL
   AND ipo_price IS NOT NULL;

-- --------------------------------------------------------- trading_halt
-- An active halt on one instrument: when it started, and when it may resume.
--
-- A halt is state, not a rule, and it must survive a restart — otherwise
-- stopping the process during a halt reopens the instrument early, which is
-- exactly the moment an operator is least likely to be watching.
--
-- One row per emiten, replaced on each halt rather than appended to. The audit
-- trail of past halts belongs in its own history table if it is ever needed;
-- this table answers one question only, and answers it with a primary-key
-- lookup: is this instrument halted right now.
CREATE TABLE IF NOT EXISTS trading_halt (
    emiten_id  bigint      PRIMARY KEY REFERENCES emiten (id) ON DELETE CASCADE,
    halted_at  timestamptz NOT NULL DEFAULT now(),
    resumes_at timestamptz NOT NULL,
    -- The price that tripped the breaker and the reference it was measured
    -- against, kept so an operator can answer "why did this halt" without
    -- reconstructing the configuration that was in force at the time.
    trigger_price   bigint  NOT NULL CHECK (trigger_price > 0),
    reference_price bigint  NOT NULL CHECK (reference_price > 0),

    CHECK (resumes_at > halted_at)
);

-- Resuming is a scan for halts whose time has come, run on a timer.
CREATE INDEX IF NOT EXISTS trading_halt_resumes_at_idx
    ON trading_halt (resumes_at);
