CREATE TABLE IF NOT EXISTS orders (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    emiten_id      bigint      NOT NULL REFERENCES emiten(id),
    participant_id bigint      NOT NULL REFERENCES participant(id),
    side           varchar(4)  NOT NULL CHECK (side IN ('buy','sell')),
    type           varchar(6)  NOT NULL CHECK (type IN ('limit','market')),
    price          bigint      NOT NULL CHECK (price >= 0),
    qty            bigint      NOT NULL CHECK (qty > 0),
    remaining      bigint      NOT NULL CHECK (remaining >= 0),
    status         varchar(10) NOT NULL CHECK (status IN ('open','filled','cancelled')),
    seq            bigint      NOT NULL UNIQUE,
    created_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_orders_emiten_status ON orders (emiten_id, status);
