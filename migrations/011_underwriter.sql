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
-- jenis: 'utama' is the lead underwriter, 'pendukung' a supporting syndicate
-- member. Constrained rather than free text so a typo cannot silently create a
-- third kind.
CREATE TABLE IF NOT EXISTS underwriter (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kode           varchar(10)  NOT NULL UNIQUE,
    nama           varchar(255) NOT NULL,
    jenis          varchar(20)  NOT NULL CHECK (jenis IN ('utama', 'pendukung')),
    participant_id bigint       NOT NULL REFERENCES participant(id),
    is_active      boolean      NOT NULL DEFAULT true,
    created_at     timestamptz  NOT NULL DEFAULT now()
);

-- Supports "which underwriters trade through this broker".
CREATE INDEX IF NOT EXISTS idx_underwriter_participant ON underwriter (participant_id);

-- ------------------------------------------------------- ipo allocation
-- Who underwrote which listing, and for how many shares. This is the audit trail
-- the share movement itself does not keep: broker_assets_list records only a
-- running balance, so without this table an IPO allocation becomes
-- indistinguishable from an ordinary purchase the moment a second trade lands.
--
-- The lead/support split is denormalized onto the row (jenis) because an
-- underwriter's role can differ per offering — lead on one listing, support on the
-- next — so it belongs to the allocation, not to the firm.
CREATE TABLE IF NOT EXISTS ipo_allocation (
    id             bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    emiten_id      bigint      NOT NULL REFERENCES emiten(id),
    underwriter_id bigint      NOT NULL REFERENCES underwriter(id),
    participant_id bigint      NOT NULL REFERENCES participant(id),
    jenis          varchar(20) NOT NULL CHECK (jenis IN ('utama', 'pendukung')),
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
-- against a fresh database without hand-building a syndicate first. Guarded so
-- re-runs are non-destructive, and skipped entirely if no participant exists yet.
INSERT INTO underwriter (kode, nama, jenis, participant_id)
SELECT v.kode, v.nama, v.jenis, p.id
FROM (VALUES
    ('UW01', 'Danareksa Sekuritas',   'utama'),
    ('UW02', 'Mandiri Sekuritas',     'pendukung')
) AS v(kode, nama, jenis)
CROSS JOIN LATERAL (
    SELECT id FROM participant ORDER BY id LIMIT 1
) p
ON CONFLICT (kode) DO NOTHING;
