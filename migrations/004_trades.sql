CREATE TABLE IF NOT EXISTS trades (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    emiten_id      bigint      NOT NULL REFERENCES emiten(id),
    buy_order_id   bigint      NOT NULL REFERENCES orders(id),
    sell_order_id  bigint      NOT NULL REFERENCES orders(id),
    price          bigint      NOT NULL CHECK (price > 0),
    qty            bigint      NOT NULL CHECK (qty > 0),
    seq            bigint      NOT NULL UNIQUE,
    executed_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_trades_emiten_time ON trades (emiten_id, executed_at);
