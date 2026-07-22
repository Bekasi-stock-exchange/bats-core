CREATE TABLE IF NOT EXISTS participant (
    id   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kode varchar(10)  NOT NULL UNIQUE,
    nama varchar(255) NOT NULL
);
