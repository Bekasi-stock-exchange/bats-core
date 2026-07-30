-- Dev-only API keys for the seed brokers (005_seed.sql), so local testing does
-- not need an admin call to POST /api/admin/participants/apikey before a broker
-- can authenticate.
--
-- Only the SHA-256 hash and a non-secret prefix are ever stored — same as any
-- key issued through the API. The plaintext below is deterministic and
-- committed to source control, so it must never be used outside local
-- development; treat these brokers as already compromised in any shared
-- environment and re-issue their keys via POST /api/admin/participants/apikey.
--
-- Send as the X-Participant-Key header:
--   YP  jast_YP_devseed0000000000000000000000000000000000000000
--   PD  jast_PD_devseed0000000000000000000000000000000000000000
--   CC  jast_CC_devseed0000000000000000000000000000000000000000
--   NI  jast_NI_devseed0000000000000000000000000000000000000000
--   AK  jast_AK_devseed0000000000000000000000000000000000000000
--
-- Idempotent and non-destructive: a broker that already has a key (issued via
-- the API after this migration first ran) keeps it.
UPDATE participant SET
    api_key_hash      = v.hash,
    api_key_prefix    = v.prefix,
    api_key_issued_at = now()
FROM (VALUES
    ('YP', '16a1499b86c047f4c449ef5cbc23c87ce49191fb6a84a57afcb3108f5cb0f569', 'jast_YP_devs'),
    ('PD', '6384929775d50525bd34b6440a554ea0d8b547b4a3522dab979f526466793e63', 'jast_PD_devs'),
    ('CC', '9f431bbfa9df21394f923b051a5419340605f141f603e14235b5838b4f514b2a', 'jast_CC_devs'),
    ('NI', '4cee1a4de90fcf0d3b0c17477016c979e772e3dc813df6fee4572892929292fd', 'jast_NI_devs'),
    ('AK', '827b94de817de075b3f753bf2cfe2164837e6c2167e7c08286769f5758f0f861', 'jast_AK_devs')
) AS v(kode, hash, prefix)
WHERE participant.kode = v.kode
  AND participant.api_key_hash IS NULL;
