-- Reduce an underwriter to what it actually is: a participant permitted to
-- underwrite offerings.
--
-- Migration 011 gave underwriter its own kode, nama and jenis. All three turned
-- out to be wrong:
--
--   kode/nama — an underwriter already points at a participant, and that
--   participant has a code and a name. Storing a second pair meant two sources of
--   truth for one firm's identity, free to drift the moment either changed. They
--   are joined from participant now, so there is only one.
--
--   jenis — the utama/pendukung split encoded a hierarchy the exchange does not
--   need. Every syndicate member is treated equally: no lead to elect, no rule
--   about whose tranche is largest. Dropping the column removes the CHECK that
--   made those rules enforceable, which is the point.
--
-- participant_id becomes UNIQUE: a firm either may underwrite or may not, and
-- registering it twice would give one participant two identities in the same
-- offering.
--
-- Every statement is idempotent: cmd/migrate has no version table and re-applies
-- every file in filename order on each run.

-- ------------------------------------------------------- underwriter
-- Migration 011 no longer creates kode, nama or jenis, so a database built from
-- scratch arrives here already in the target shape and every statement below is a
-- no-op. This file exists for databases created before that change.
--
-- The kode-dependent cleanup is guarded on the column still existing, because it
-- cannot even be parsed against the new shape — hence the EXECUTE.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_name = 'underwriter' AND column_name = 'kode'
    ) THEN
        -- The dev-seed underwriters from 011 (UW01/UW02) carry codes that no
        -- longer mean anything, and both point at the same participant — which
        -- the UNIQUE below forbids. Removed together with any allocation that
        -- references them; this is seed data, not a record of a real offering.
        EXECUTE $q$
            DELETE FROM ipo_allocation
            WHERE underwriter_id IN (SELECT id FROM underwriter WHERE kode LIKE 'UW%')
        $q$;
        EXECUTE $q$DELETE FROM underwriter WHERE kode LIKE 'UW%'$q$;
    END IF;
END $$;

-- Any remaining duplicate registrations must go before the UNIQUE index can be
-- built. The lowest id wins, arbitrarily but deterministically — the rows are
-- interchangeable once kode and nama stop distinguishing them.
DELETE FROM ipo_allocation
WHERE underwriter_id IN (
    SELECT u.id FROM underwriter u
    WHERE EXISTS (
        SELECT 1 FROM underwriter keep
        WHERE keep.participant_id = u.participant_id
          AND keep.id < u.id
    )
);

DELETE FROM underwriter u
WHERE EXISTS (
    SELECT 1 FROM underwriter keep
    WHERE keep.participant_id = u.participant_id
      AND keep.id < u.id
);

ALTER TABLE underwriter DROP COLUMN IF EXISTS jenis;
ALTER TABLE underwriter DROP COLUMN IF EXISTS kode;
ALTER TABLE underwriter DROP COLUMN IF EXISTS nama;

-- One registration per participant. Named explicitly so the re-run is a no-op
-- rather than a second identical index under a generated name.
CREATE UNIQUE INDEX IF NOT EXISTS underwriter_participant_key
    ON underwriter (participant_id);

COMMENT ON TABLE underwriter IS
    'Participants permitted to underwrite offerings. Identity (code, name) comes '
    'from the participant row; this table only records the permission.';

-- ----------------------------------------------------- ipo_allocation
-- jenis was denormalized onto the allocation so a firm could lead one offering
-- and support the next. With the roles gone there is nothing to denormalize.
ALTER TABLE ipo_allocation DROP COLUMN IF EXISTS jenis;
