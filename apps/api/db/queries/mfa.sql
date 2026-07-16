-- S7.5.5 MFA / TOTP queries. Auth-plane; enrollment OPEN, enforce enterprise (app layer).

-- ── user_totp (verify-before-arm + replay guard) ──────────────────────────────────
-- name: UpsertUnconfirmedTOTP :exec
-- Start/RE-start enrollment: store a fresh SEALED secret, unconfirmed. Re-generating before
-- confirming replaces the old secret and clears the replay clock — an unconfirmed secret never
-- gates login, so overwriting it is safe.
INSERT INTO user_totp (user_id, secret_enc, confirmed, last_used_timestep, created_at, confirmed_at)
VALUES ($1, $2, false, NULL, now(), NULL)
ON CONFLICT (user_id) DO UPDATE
SET secret_enc = EXCLUDED.secret_enc, confirmed = false, last_used_timestep = NULL,
    created_at = now(), confirmed_at = NULL;

-- name: GetTOTP :one
SELECT * FROM user_totp WHERE user_id = $1;

-- name: GetConfirmedTOTPForUpdate :one
-- Verify path: read the CONFIRMED secret + replay clock under a row lock, so the replay-guard
-- read+update can't interleave with a concurrent verify.
SELECT * FROM user_totp WHERE user_id = $1 AND confirmed FOR UPDATE;

-- name: ConfirmTOTP :execrows
-- Arm enrollment: only an UNCONFIRMED row flips to confirmed, stamping the confirming code's
-- timestep as the replay clock so the very first login can't replay the confirmation code.
UPDATE user_totp
SET confirmed = true, confirmed_at = now(), last_used_timestep = sqlc.arg(last_used_timestep)
WHERE user_id = $1 AND NOT confirmed;

-- name: SetTOTPLastTimestep :exec
-- Replay guard: advance the last-accepted timestep after a successful verify.
UPDATE user_totp SET last_used_timestep = $2 WHERE user_id = $1;

-- name: DeleteTOTP :execrows
-- Disenroll (self re-enroll clears via upsert; explicit delete for self-disenroll + admin-reset).
DELETE FROM user_totp WHERE user_id = $1;

-- ── user_recovery_codes (single-use, hashed) ──────────────────────────────────────
-- name: InsertRecoveryCode :exec
INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1, $2);

-- name: ConsumeRecoveryCode :one
-- Atomic single-use: only an UNUSED code for THIS user is burned; returns its id on success,
-- 0 rows if already used / not found (no which-code oracle to the caller).
UPDATE user_recovery_codes
SET used_at = now()
WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
RETURNING id;

-- name: CountUnusedRecoveryCodes :one
SELECT count(*) FROM user_recovery_codes WHERE user_id = $1 AND used_at IS NULL;

-- name: DeleteRecoveryCodesForUser :exec
DELETE FROM user_recovery_codes WHERE user_id = $1;

-- ── mfa_challenges (the login second-step token — NOT a session) ───────────────────
-- name: CreateMfaChallenge :exec
INSERT INTO mfa_challenges (user_id, token_hash, expires_at) VALUES ($1, $2, $3);

-- name: GetMfaChallengeForUpdate :one
-- Verify path: fetch a LIVE challenge under a row lock (attempt-count + burn serialize here).
SELECT * FROM mfa_challenges WHERE token_hash = $1 AND expires_at > now() FOR UPDATE;

-- name: IncrementMfaChallengeAttempts :one
UPDATE mfa_challenges SET attempts = attempts + 1 WHERE id = $1 RETURNING attempts;

-- name: DeleteMfaChallenge :exec
-- Burn — on SUCCESS or on cap exhaustion (a token never survives its own resolution).
DELETE FROM mfa_challenges WHERE id = $1;

-- name: DeleteExpiredMfaChallenges :exec
DELETE FROM mfa_challenges WHERE expires_at <= now();

-- ── org_mfa (enforce flag — slice 2 logic) ────────────────────────────────────────
-- name: GetOrgMfa :one
SELECT * FROM org_mfa WHERE org_id = $1;

-- name: UpsertOrgMfaEnforce :exec
INSERT INTO org_mfa (org_id, enforce, updated_at) VALUES ($1, $2, now())
ON CONFLICT (org_id) DO UPDATE SET enforce = EXCLUDED.enforce, updated_at = now();
