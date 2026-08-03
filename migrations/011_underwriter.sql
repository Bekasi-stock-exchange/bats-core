-- Underwriters (penjamin emisi) and the share allocations they receive at IPO.
--
-- An underwriter is its own entity, not a flag on participant: the roles are
-- different (a broker trades on the exchange, an underwriter guarantees an
-- offering) and one firm may act as underwriter without ever quoting a price.
--
-- It nonetheless points at a participant. Shares only mean something to a holder
-- that can trade them — broker_assets_list, the wallet, and the matching engine's
-- ledger are all keyed by participant_id — so an allocation to an underwriter with
-- no trading identity would be shares that can never reach the market, which is the
-- opposite of what an IPO is for.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

-- ----------------------------------------------------------- underwriter
-- An underwriter carries no code or name of its own: it *is* a participant that
-- may underwrite, and that participant already has both. Storing a second copy
-- would be two sources of truth for one firm's identity.
--
-- This table originally had kode, nama and a utama/pendukung jenis column.
-- Migration 016 drops all three; they are simply never created here, so a fresh
-- database and a migrated one end up with the same table. 016 still exists for
-- databases that were created before it.
CREATE TABLE IF NOT EXISTS underwriter (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participant_id bigint      NOT NULL REFERENCES participant(id),
    is_active      boolean     NOT NULL DEFAULT true,
    created_at     timestamptz NOT NULL DEFAULT now()
);

-- One registration per broker: a firm either may underwrite or may not, and two
-- rows would give one participant two identities in the same offering.
CREATE UNIQUE INDEX IF NOT EXISTS underwriter_participant_key
    ON underwriter (participant_id);

-- ------------------------------------------------------- ipo allocation
-- Who underwrote which listing, and for how many shares. This is the audit trail
-- the share movement itself does not keep: broker_assets_list records only a
-- running balance, so without this table an IPO allocation becomes
-- indistinguishable from an ordinary purchase the moment a second trade lands.
--
-- The syndicate is flat: a tranche records who took how many shares at what
-- price, and nothing about rank. This table originally carried a utama/pendukung
-- jenis column, dropped by migration 016 and no longer created here.
CREATE TABLE IF NOT EXISTS ipo_allocation (
    id             bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    emiten_id      bigint      NOT NULL REFERENCES emiten(id),
    underwriter_id bigint      NOT NULL REFERENCES underwriter(id),
    participant_id bigint      NOT NULL REFERENCES participant(id),
    shares         bigint      NOT NULL CHECK (shares > 0),
    price          bigint      NOT NULL CHECK (price > 0),
    created_at     timestamptz NOT NULL DEFAULT now(),
    -- One underwriter takes at most one tranche per offering; two tranches would
    -- be one allocation with the shares summed.
    UNIQUE (emiten_id, underwriter_id)
);

CREATE INDEX IF NOT EXISTS idx_ipo_allocation_emiten ON ipo_allocation (emiten_id);

-- ------------------------------------------------------------- dev seed
-- Two underwriters over the existing brokers, so the IPO endpoint is exercisable
-- against a fresh database without hand-building a syndicate first. Skipped
-- entirely if no participant exists yet.
--
-- Written against the *final* shape of this table, not the one created above:
-- migration 016 drops kode, nama and jenis, and cmd/migrate re-applies every file
-- on every run, so a seed naming those columns would fail on the second run once
-- 016 had landed. Only participant_id is inserted, which is true of both shapes.
--
-- Two distinct brokers, because 016 also makes participant_id UNIQUE — one
-- registration per firm. NOT EXISTS rather than ON CONFLICT for the same
-- forward-compatibility reason: the unique index it would need to name does not
-- exist yet at this point in the sequence.
INSERT INTO underwriter (participant_id)
SELECT p.id
FROM (SELECT id FROM participant ORDER BY id LIMIT 2) p
WHERE NOT EXISTS (
    SELECT 1 FROM underwriter u WHERE u.participant_id = p.id
);
