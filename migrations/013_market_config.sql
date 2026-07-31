-- Exchange-wide trading parameters, editable at runtime through
-- /api/admin/config.
--
-- These are rules the exchange enforces on every order, not properties of any
-- one instrument, so they belong neither on emiten nor in the environment. The
-- environment was the obvious alternative and is the wrong one: changing a
-- trading rule would mean a redeploy, and the value that was in force when an
-- order was rejected would leave no trace.
--
-- A single-row table rather than a key/value store. The columns are a fixed,
-- known set with real types, so a typed row gets a CHECK constraint per rule and
-- a scan that cannot silently coerce "fifty" to 0 — neither of which a
-- text-valued settings table can offer.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

-- ---------------------------------------------------------- market_config
-- min_price is the floor for a limit order's price, in rupiah. It exists because
-- the only price rule the engine had was "> 0", which let a seller quote 58, then
-- 5, then 1 — each one legal, and each one resting in the book as the best ask
-- for anything that came next. BEI's real floor is Rp 50; below it a quote is not
-- a cheap price but a broken one.
--
-- id is pinned to 1 by a CHECK: this is configuration, and a second row would
-- make "the configuration" ambiguous for every reader.
CREATE TABLE IF NOT EXISTS market_config (
    id         smallint    PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    min_price  bigint      NOT NULL CHECK (min_price > 0),
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- The default row. ON CONFLICT DO NOTHING keeps a re-run from resetting a floor
-- the operator has since changed through the API.
INSERT INTO market_config (id, min_price) VALUES (1, 50)
ON CONFLICT (id) DO NOTHING;
