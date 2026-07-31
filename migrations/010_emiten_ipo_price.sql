-- IPO price: the reference price an instrument carries before it has ever traded.
--
-- Without it a newly listed emiten has no price at all — last trade is NULL, so
-- current_price, market cap and every holding's market value are NULL, and the
-- first broker to quote it has nothing to anchor on. That is the same hole IDX
-- closes with the offering price, which serves as the previous close on listing
-- day.
--
-- Nullable, not NOT NULL: the five seeded emiten predate this column and already
-- have a trade history, so demanding a value for them would invent one. New
-- listings must supply it (enforced in the emiten service, not here, so the
-- existing rows stay legal).
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

ALTER TABLE emiten
    ADD COLUMN IF NOT EXISTS ipo_price bigint CHECK (ipo_price > 0);

COMMENT ON COLUMN emiten.ipo_price IS
    'Offering price at listing. Reference price until the first trade executes; '
    'never overwritten afterwards, so the listing price stays auditable.';
