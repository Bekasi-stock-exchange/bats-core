-- Aksi korporasi: the events an issuer performs on its own instrument, which
-- change what a share is or hand something to whoever holds one.
--
-- Four kinds, and they split along a line worth naming. A split or a bonus
-- changes the *share count* — every holder's position is restated, and so is the
-- instrument's listed_shares. A dividend changes nobody's share count; it moves
-- cash. What they have in common is that all four are decided in advance and take
-- effect at a moment the exchange picks, which is why this is two tables rather
-- than one write.
--
-- The two-phase shape (announce, then execute) is not ceremony. Between the two,
-- an operator can still cancel; after execution, every holder's ledger has moved
-- and there is no undoing it from here. Recording the announcement separately is
-- also the only way to answer "why did this instrument's share count change" once
-- the balances have been restated — broker_assets_list keeps a running total and
-- nothing about what moved it.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

-- ------------------------------------------------------ corporate_action
-- One announced event. Rows are never deleted: a cancelled action is a fact
-- about the instrument's history, and dropping it would make the audit trail
-- disagree with what participants were told at the time.
--
-- ratio_from / ratio_to carry the terms of a split or bonus, and amount carries a
-- dividend's per-share figure. Both are nullable because neither applies to every
-- kind, and a CHECK below enforces that the right one is present rather than
-- letting a dividend be announced with a split ratio nobody will read.
CREATE TABLE IF NOT EXISTS corporate_action (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    emiten_id   bigint      NOT NULL REFERENCES emiten(id),

    -- SPLIT, REVERSE_SPLIT, BONUS, or DIVIDEND.
    jenis       text        NOT NULL,

    -- ANNOUNCED, EXECUTED, or CANCELLED. An action enters ANNOUNCED and leaves
    -- for exactly one of the other two, never back.
    status      text        NOT NULL DEFAULT 'ANNOUNCED',

    -- The terms of a split or bonus, as a ratio. A 1:2 split is from=1, to=2:
    -- one old share becomes two. A 2:1 reverse split is from=2, to=1. A bonus of
    -- one new share for every two held is from=2, to=3 — the holder ends with
    -- three where they had two, which is the same arithmetic a split uses and is
    -- why one pair of columns serves both.
    ratio_from  bigint,
    ratio_to    bigint,

    -- Rupiah per share held, for a dividend.
    amount      bigint,

    -- When holdings are read to decide who receives what. Informational on its
    -- own: this engine has no end-of-day snapshot, so execution reads the
    -- holdings as they stand at that moment. Recorded because it is what an
    -- issuer announces and what a participant plans against.
    record_date date,

    -- Free text for the announcement.
    keterangan  text        NOT NULL DEFAULT '',

    created_at  timestamptz NOT NULL DEFAULT now(),
    executed_at timestamptz,

    CONSTRAINT corporate_action_jenis_check
        CHECK (jenis IN ('SPLIT', 'REVERSE_SPLIT', 'BONUS', 'DIVIDEND')),
    CONSTRAINT corporate_action_status_check
        CHECK (status IN ('ANNOUNCED', 'EXECUTED', 'CANCELLED')),

    -- The terms must match the kind. A split with no ratio could not be executed,
    -- and a dividend with no amount would pay nothing — both are better refused at
    -- announcement than discovered at execution, when half the market is waiting.
    CONSTRAINT corporate_action_terms_check CHECK (
        (jenis = 'DIVIDEND' AND amount IS NOT NULL AND amount > 0)
        OR
        (jenis <> 'DIVIDEND'
            AND ratio_from IS NOT NULL AND ratio_from > 0
            AND ratio_to   IS NOT NULL AND ratio_to   > 0)
    ),

    -- executed_at and the EXECUTED status are one fact, so neither may appear
    -- without the other.
    CONSTRAINT corporate_action_executed_check CHECK (
        (status = 'EXECUTED' AND executed_at IS NOT NULL)
        OR
        (status <> 'EXECUTED' AND executed_at IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_corporate_action_emiten
    ON corporate_action (emiten_id, created_at DESC);

-- Listing the work an operator still has to do is a common read, and it is a
-- small fraction of the table once a season of actions has been executed.
CREATE INDEX IF NOT EXISTS idx_corporate_action_status
    ON corporate_action (status)
    WHERE status = 'ANNOUNCED';

-- ------------------------------------------------ corporate_action_entry
-- What each holder actually received, written at execution.
--
-- This is the table that makes an executed action auditable. broker_assets_list
-- and broker_wallet both keep running balances and nothing about what moved them,
-- so without these rows a split becomes indistinguishable from a very large
-- purchase the moment the next trade lands — and a dividend, from a funding
-- adjustment.
--
-- Both share columns are recorded rather than just the delta: the whole value of
-- this row is being able to answer "what did this broker hold, and what did that
-- become", and a delta alone loses the first half.
CREATE TABLE IF NOT EXISTS corporate_action_entry (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    action_id      bigint NOT NULL REFERENCES corporate_action(id) ON DELETE CASCADE,
    participant_id bigint NOT NULL REFERENCES participant(id),

    -- Shares held when the action executed.
    shares_before  bigint NOT NULL CHECK (shares_before >= 0),
    -- Shares held afterwards. Equal to shares_before for a dividend, which pays
    -- cash and restates nothing.
    shares_after   bigint NOT NULL CHECK (shares_after >= 0),
    -- Rupiah paid to this broker. Zero for a split or bonus.
    cash_amount    bigint NOT NULL DEFAULT 0 CHECK (cash_amount >= 0),

    created_at     timestamptz NOT NULL DEFAULT now(),

    -- One entry per broker per action: two would be one entry with the figures
    -- summed, and would double-count in any report over this table.
    UNIQUE (action_id, participant_id)
);

CREATE INDEX IF NOT EXISTS idx_corporate_action_entry_action
    ON corporate_action_entry (action_id);
