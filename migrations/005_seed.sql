-- Dummy master data for local development. Manual INSERTs are sufficient; there
-- is no CRUD for master data at this stage.

INSERT INTO emiten (kode, nama, listed_shares) VALUES
    ('BBCA', 'Bank Central Asia Tbk',       123275050000),
    ('BBRI', 'Bank Rakyat Indonesia Tbk',   151559000000),
    ('TLKM', 'Telkom Indonesia Tbk',         99062216600),
    ('ASII', 'Astra International Tbk',       40483553140),
    ('GOTO', 'GoTo Gojek Tokopedia Tbk',   1185707781000)
ON CONFLICT (kode) DO NOTHING;

INSERT INTO participant (kode, nama) VALUES
    ('YP', 'Mirae Asset Sekuritas'),
    ('PD', 'Indo Premier Sekuritas'),
    ('CC', 'Mandiri Sekuritas'),
    ('NI', 'BNI Sekuritas'),
    ('AK', 'UBS Sekuritas Indonesia')
ON CONFLICT (kode) DO NOTHING;
