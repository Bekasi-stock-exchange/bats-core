-- Two-tier auth, broker holdings, and the extra emiten share/status columns.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

-- ---------------------------------------------------------------- emiten
-- unlisted_shares completes the share count: total = listed + unlisted, which is
-- what the free-float percentage on the emiten detail endpoint is derived from.
-- is_active gates matching, not existence: an inactive emiten rejects orders but
-- its book and history stay readable.
ALTER TABLE emiten
    ADD COLUMN IF NOT EXISTS unlisted_shares bigint NOT NULL DEFAULT 0
        CHECK (unlisted_shares >= 0),
    ADD COLUMN IF NOT EXISTS is_active boolean NOT NULL DEFAULT true;

-- ----------------------------------------------------------- participant
-- Only the SHA-256 hash is stored, never the key itself. The prefix is a short
-- non-secret fragment so a key can be identified in listings without exposing it.
ALTER TABLE participant
    ADD COLUMN IF NOT EXISTS api_key_hash      text,
    ADD COLUMN IF NOT EXISTS api_key_prefix    text,
    ADD COLUMN IF NOT EXISTS api_key_issued_at timestamptz;

-- Partial: a hash may appear at most once, but most rows have none.
CREATE UNIQUE INDEX IF NOT EXISTS idx_participant_api_key_hash
    ON participant (api_key_hash) WHERE api_key_hash IS NOT NULL;

-- ---------------------------------------------------------------- trades
-- Denormalize both participants onto the trade. Without this, per-broker history
-- needs trades -> orders (twice) -> participant filtered by
-- "bo.participant_id = $1 OR so.participant_id = $1" — an OR across two joined
-- tables, which no index can serve. With it, each side is a plain indexed lookup.
ALTER TABLE trades
    ADD COLUMN IF NOT EXISTS buy_participant_id  bigint REFERENCES participant(id),
    ADD COLUMN IF NOT EXISTS sell_participant_id bigint REFERENCES participant(id);

UPDATE trades t
SET buy_participant_id  = bo.participant_id,
    sell_participant_id = so.participant_id
FROM orders bo, orders so
WHERE bo.id = t.buy_order_id
  AND so.id = t.sell_order_id
  AND (t.buy_participant_id IS NULL OR t.sell_participant_id IS NULL);

ALTER TABLE trades ALTER COLUMN buy_participant_id  SET NOT NULL;
ALTER TABLE trades ALTER COLUMN sell_participant_id SET NOT NULL;

CREATE INDEX IF NOT EXISTS idx_trades_buy_participant  ON trades (buy_participant_id, seq DESC);
CREATE INDEX IF NOT EXISTS idx_trades_sell_participant ON trades (sell_participant_id, seq DESC);

-- The existing index is on (emiten_id, executed_at); the last-price lookup that
-- market value and the emiten detail endpoint depend on orders by seq.
CREATE INDEX IF NOT EXISTS idx_trades_emiten_seq ON trades (emiten_id, seq DESC);

-- ------------------------------------------------------ broker holdings
-- Share holdings per broker per emiten, written in the same transaction as the
-- match that moves them. Market value is NOT stored here: it depends on the last
-- traded price, so one trade would invalidate every holder of that emiten. It is
-- derived on read instead.
CREATE TABLE IF NOT EXISTS broker_assets_list (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participant_id bigint      NOT NULL REFERENCES participant(id),
    emiten_id      bigint      NOT NULL REFERENCES emiten(id),
    amount_shared  bigint      NOT NULL DEFAULT 0 CHECK (amount_shared >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (participant_id, emiten_id)
);

-- Supports "every holder of this emiten" reads.
CREATE INDEX IF NOT EXISTS idx_broker_assets_emiten ON broker_assets_list (emiten_id);

-- ------------------------------------------------------------- dev seed
-- Lives here, not in 005_seed.sql: 005 runs before this file, so it cannot
-- reference columns created above. The guards make re-runs non-destructive.
UPDATE emiten SET unlisted_shares = v.n
FROM (VALUES
    ('BBCA',  12327505000),
    ('BBRI',  15155900000),
    ('TLKM',   9906221660),
    ('ASII',   4048355314),
    ('GOTO', 118570778100)
) AS v(kode, n)
WHERE emiten.kode = v.kode AND emiten.unlisted_shares = 0;

-- Without opening balances every sell is rejected, so nothing is testable.
INSERT INTO broker_assets_list (participant_id, emiten_id, amount_shared)
SELECT p.id, e.id, 1000000
FROM participant p CROSS JOIN emiten e
ON CONFLICT (participant_id, emiten_id) DO NOTHING;
