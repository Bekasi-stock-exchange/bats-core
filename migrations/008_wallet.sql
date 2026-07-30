-- Broker cash balances, separate from the share holdings in broker_assets_list.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

CREATE TABLE IF NOT EXISTS broker_wallet (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    participant_id bigint      NOT NULL REFERENCES participant(id),
    balance        bigint      NOT NULL DEFAULT 0 CHECK (balance >= 0),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    UNIQUE (participant_id)
);

-- Without an opening balance every buy is untestable.
INSERT INTO broker_wallet (participant_id, balance)
SELECT p.id, 10000000000
FROM participant p
ON CONFLICT (participant_id) DO NOTHING;
