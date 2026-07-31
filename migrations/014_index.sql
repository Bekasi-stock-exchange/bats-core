-- Composite index: the exchange-wide price level, computed IHSG-style.
--
-- IHSG is a market-cap weighted index: the summed value of every listed
-- instrument, divided by a divisor, times a base value. The divisor is what
-- makes the series continuous — see below — and it is the reason this needs
-- stored state at all rather than being a pure function of the trades table.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

-- ------------------------------------------------------------ market_index
-- One row per index. IHSG is seeded below; the table is keyed by kode rather
-- than pinned to a single row because a sectoral or subset index is the obvious
-- next thing an exchange wants, and it would be the same computation over a
-- different member set.
--
-- divisor is stored, never recomputed from the current members. That is the
-- whole mechanism behind a continuous index: when an instrument lists or
-- delists, total market cap jumps by that instrument's entire value, and the
-- index must not. The divisor is restated by the same ratio so the level is
-- unchanged across the event, and every subsequent move is a real price move.
-- Recomputing it from scratch would erase exactly that correction.
--
-- base_value is the level on base_date, 100 for IHSG (10 August 1982). It is
-- kept as a column rather than a constant because a sectoral index added later
-- would carry its own base.
--
-- numeric, not bigint, for divisor: it is a ratio-adjusted quantity, not money.
-- Rounding it to a whole number at each listing would let the level drift a
-- little every time, permanently, and the drift only ever accumulates.
CREATE TABLE IF NOT EXISTS market_index (
    id         smallint     GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kode       varchar(20)  NOT NULL UNIQUE,
    nama       varchar(255) NOT NULL,
    base_value numeric(20,6) NOT NULL CHECK (base_value > 0),
    base_date  date         NOT NULL,
    divisor    numeric(30,6) NOT NULL CHECK (divisor > 0),
    updated_at timestamptz  NOT NULL DEFAULT now()
);

-- IHSG, seeded with its real base: 100 on 10 August 1982.
--
-- The divisor starts at 1 and is corrected on the first computation, which is
-- what NULL-free bootstrapping costs. It cannot be seeded to a meaningful value
-- here because it depends on the market cap of the instruments present, which
-- this file cannot see.
--
-- ON CONFLICT DO NOTHING keeps a re-run from resetting a divisor that listings
-- have since adjusted — the one value in this table that must never be reverted.
INSERT INTO market_index (kode, nama, base_value, base_date, divisor)
VALUES ('IHSG', 'Indeks Harga Saham Gabungan', 100, DATE '1982-08-10', 1)
ON CONFLICT (kode) DO NOTHING;

-- --------------------------------------------------------- index_snapshot
-- The index level over time, so the series can be charted and audited.
--
-- Without this the index exists only as a number computed from the current
-- state, and its history is unrecoverable: past levels cannot be reconstructed
-- from the trades table once the divisor has changed, because the divisor that
-- was in force at that moment is gone. Storing it per row is what keeps an old
-- level verifiable.
--
-- market_cap is stored alongside for the same reason — it is the numerator that
-- produced the level, and keeping it makes each row self-contained rather than
-- dependent on re-deriving prices as they stood at that instant.
--
-- members is how many instruments were priced into the level. A level computed
-- over 4 of 5 instruments is not comparable to one over all 5, and without this
-- column that difference is invisible.
CREATE TABLE IF NOT EXISTS index_snapshot (
    id          bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    index_id    smallint      NOT NULL REFERENCES market_index(id),
    value       numeric(20,6) NOT NULL,
    market_cap  numeric(30,0) NOT NULL,
    divisor     numeric(30,6) NOT NULL,
    members     integer       NOT NULL,
    captured_at timestamptz   NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_index_snapshot_time
    ON index_snapshot (index_id, captured_at DESC);
