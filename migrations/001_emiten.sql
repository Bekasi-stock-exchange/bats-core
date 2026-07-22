CREATE TABLE IF NOT EXISTS emiten (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kode          varchar(10)  NOT NULL UNIQUE,
    nama          varchar(255) NOT NULL,
    listed_shares bigint       NOT NULL CHECK (listed_shares > 0),
    created_at    timestamptz  NOT NULL DEFAULT now()
);
