-- Backfill ipo_price for the five seeded emiten.
--
-- Migration 010 added the column as nullable precisely because these rows predate
-- it: they already carry a trade history, so the schema could not demand a value
-- without inventing one. This file supplies the real offering prices instead, so
-- the seed data stops being the one case where ipo_price is absent.
--
-- The prices are the actual IPO offering prices on the IDX. Four of these listed
-- long before their current price levels and have been through stock splits since,
-- so the numbers look small next to today's quotes — that is correct and harmless.
-- ipo_price is the listing price, kept for the record; it is never rewritten, and
-- it only backs a valuation while the instrument has not traded. All five have
-- traded, so their reference_price stays trade-derived (price_source "trade") and
-- market cap is unaffected by this file.
--
-- Only fills NULLs: an emiten that already has a price keeps it, so re-running
-- this can never overwrite a value set through the API or an IPO. That, plus the
-- WHERE clause, is what makes it idempotent — cmd/migrate has no version table and
-- re-applies every file in filename order on each run.

UPDATE emiten AS e
SET ipo_price = v.ipo_price
FROM (VALUES
    ('BBCA',  1400::bigint),  -- May 2000
    ('BBRI',   875::bigint),  -- Nov 2003
    ('TLKM',  2050::bigint),  -- Nov 1995
    ('ASII', 14850::bigint),  -- April 1990
    ('GOTO',   338::bigint)   -- April 2022
) AS v(kode, ipo_price)
WHERE e.kode = v.kode
  AND e.ipo_price IS NULL;
