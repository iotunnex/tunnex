-- name: InsertAccessEvent :execrows
-- Idempotent on (org_id, seq) so a replayed ingest batch can't double-insert. The id is
-- app-generated (uuid v7) so the SAME id identifies the row in BOTH the PG hot-window and
-- the JSONL source-of-truth stream.
INSERT INTO access_events (
    id, org_id, seq, node_id, occurred_at, decision, rule_id,
    src_device_id, src_user_id, src_ip, dst_ip, dst_resource_id, dst_group_id,
    protocol, dst_port, deny_count, window_end
) VALUES (
    $1, $2, $3, $4, $5, $6, $7,
    $8, $9, $10, $11, $12, $13,
    $14, $15, $16, $17
)
ON CONFLICT (org_id, seq) DO NOTHING;

-- name: ListAccessEvents :many
-- Keyset page, newest-first, scoped by org. Expanded (created_at, id) < (cursor) predicate
-- (row-value form confuses sqlc's type inference for the id cursor). First page passes a
-- far-future created_at + a max uuid so the whole feed is < the cursor. Uses
-- access_events_org_created_id_idx.
SELECT * FROM access_events
WHERE org_id = $1
  AND (created_at < sqlc.arg(before_created_at)
       OR (created_at = sqlc.arg(before_created_at) AND id < sqlc.arg(before_id)))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: ListAccessDenies :many
-- The security-focused feed: deny + deny_aggregate + terminated only, same keyset shape.
SELECT * FROM access_events
WHERE org_id = $1
  AND decision <> 'allow'
  AND (created_at < sqlc.arg(before_created_at)
       OR (created_at = sqlc.arg(before_created_at) AND id < sqlc.arg(before_id)))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(page_limit);

-- name: MaxAccessEventSeqForOrg :one
-- The current high-water seq for an org — the ingest resumes the monotonic counter from
-- here after a restart so the per-org sequence never rewinds (gap-detection integrity).
SELECT COALESCE(MAX(seq), 0)::bigint AS max_seq FROM access_events WHERE org_id = $1;

-- name: SweepAccessEventsByAge :execrows
-- lint:cross-org — retention housekeeping runs across ALL orgs by INGEST age (created_at,
-- not the agent-clock occurred_at, so agent skew can't extend retention). The JSONL stream
-- keeps the full record; PG is a bounded cache, so this delete is lossless for export.
DELETE FROM access_events WHERE created_at < sqlc.arg(older_than);

-- name: SweepAccessEventsOverCap :execrows
-- Per-org row cap: keep the newest keep_newest rows for the org, delete the rest. Protects
-- the disk when one org is noisy without touching quiet orgs.
DELETE FROM access_events AS target
WHERE target.org_id = sqlc.arg(org_id)
  AND target.id NOT IN (
    SELECT ae.id FROM access_events ae
    WHERE ae.org_id = sqlc.arg(org_id)
    ORDER BY ae.created_at DESC, ae.id DESC
    LIMIT sqlc.arg(keep_newest)
  );
