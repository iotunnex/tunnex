-- ⛔ DEMO FIXTURES — the DESIGNED PICTURE, on a local stack (S14.5).
--
-- WHY THIS EXISTS. Every EPIC-14 screen was being reviewed against an empty database, so the founder was
-- judging EMPTY STATES while the designed screen stayed invisible. The Sites network map cost SIX review
-- rounds partly for this reason: the wireframe draws five nodes and a hub, the stack had one gateway that
-- was its own hub, and no amount of design fidelity turns two nodes into five.
--
-- ════════════════════════════════════════════════════════════════════════════════════════════════════════
-- ⛔ THE BINDING CONSTRAINT: NO FIXTURE MAY CREATE A STATE THE PRODUCT CANNOT REACH ON ITS OWN.
-- ════════════════════════════════════════════════════════════════════════════════════════════════════════
--
-- A seeded row that no product path produces is a picture of something that does not exist, and it would be
-- reviewed as though it did. So every row below is what some real path WRITES:
--
--   · a site + binding + subnet         = what POST /routed-lans writes
--   · a pending subnet                  = what POST .../subnets writes before approval
--   · node_peer_status rows             = what an agent's status report writes
--   · a revoked node/device             = what the revoke endpoints write
--
-- ⚠ AND THE HEALTH KINDS ARE **NEVER WRITTEN** — `policy_degraded_kind` is COMPUTED by PolicyHealthForNodes
-- from topology + handshake freshness. So this file cannot say "degraded"; it seeds the INPUTS (real peer
-- rows with real timestamps) and lets the control plane derive the badge. If the derivation disagrees with
-- the intent commented on each row, THE DERIVATION IS RIGHT and the comment is the bug — that disagreement
-- is itself the useful signal, because it means the fixture described a state the product does not produce.
--
-- IDEMPOTENT: fixed UUIDs + ON CONFLICT DO NOTHING. Safe to re-run.
-- GUARD: everything hangs off the demo org, which `countRealOrgs` already excludes, so this cannot fight the
-- existing real-data refusal.
-- CLOCK: every timestamp is relative to now(), so "3 minutes ago" stays 3 minutes ago on every reseed.

BEGIN;

-- ── GATEWAYS ────────────────────────────────────────────────────────────────────────────────────────────
-- 5: three site-bound, one UNBOUND (so `Route a LAN` stays reachable), one REVOKED (the revoked-badge path).
-- `endpoint` + `wg_public_key` are the hub-election capability gate (electSiteHubSet), so only the intended
-- hub carries both.
INSERT INTO nodes (id, org_id, name, status, cert_serial, agent_version, enrolled_at, last_seen_at, wg_public_key, endpoint)
VALUES
  ('01900000-0000-7000-8000-0000000f0001', '01900000-0000-7000-8000-000000000001', 'gw-us-east',   'active',  'FIXTURE-01', '0.3.0', now() - interval '30 days', now() - interval '20 seconds', 'ZmlY3R1cmVLZXlIVUIwMDAwMDAwMDAwMDAwMDAwMDA9', '198.51.100.10:51820'),
  ('01900000-0000-7000-8000-0000000f0002', '01900000-0000-7000-8000-000000000001', 'gw-eu-west',   'active',  'FIXTURE-02', '0.3.0', now() - interval '22 days', now() - interval '35 seconds', 'ZmlY3R1cmVLZXlFVTAwMDAwMDAwMDAwMDAwMDAwMDA9', ''),
  ('01900000-0000-7000-8000-0000000f0003', '01900000-0000-7000-8000-000000000001', 'gw-ap-south',  'active',  'FIXTURE-03', '0.2.9', now() - interval '14 days', now() - interval '25 seconds', 'ZmlY3R1cmVLZXlBUDAwMDAwMDAwMDAwMDAwMDAwMDA9', ''),
  ('01900000-0000-7000-8000-0000000f0004', '01900000-0000-7000-8000-000000000001', 'gw-unbound-1', 'active',  'FIXTURE-04', '0.3.0', now() - interval '2 days',  now() - interval '15 seconds', 'ZmlY3R1cmVLZXlVTjAwMDAwMDAwMDAwMDAwMDAwMDA9', ''),
  ('01900000-0000-7000-8000-0000000f0005', '01900000-0000-7000-8000-000000000001', 'gw-retired-1', 'revoked', 'FIXTURE-05', '0.2.4', now() - interval '90 days', now() - interval '9 days',   '',                                             '')
ON CONFLICT (id) DO NOTHING;

UPDATE nodes SET revoked_at = now() - interval '9 days'
 WHERE id = '01900000-0000-7000-8000-0000000f0005' AND revoked_at IS NULL;

-- ── SITES ───────────────────────────────────────────────────────────────────────────────────────────────
-- FOUR: the hub's own site plus three spokes. Three spokes is the minimum that renders as a RING rather than
-- a line, and ≥2 sites with approved subnets is what crosses the multi-site threshold so routes compile at
-- all (crossesMultiSiteThreshold).
INSERT INTO sites (id, org_id, name, link_transport, created_at)
VALUES
  ('01900000-0000-7000-8000-0000000e0001', '01900000-0000-7000-8000-000000000001', 'us-east-dc', 'wireguard', now() - interval '30 days'),
  ('01900000-0000-7000-8000-0000000e0002', '01900000-0000-7000-8000-000000000001', 'eu-lan',     'wireguard', now() - interval '22 days'),
  ('01900000-0000-7000-8000-0000000e0003', '01900000-0000-7000-8000-000000000001', 'ap-lan',     'wireguard', now() - interval '14 days'),
  ('01900000-0000-7000-8000-0000000e0004', '01900000-0000-7000-8000-000000000001', 'sa-lan',     'wireguard', now() - interval '6 days')
ON CONFLICT (id) DO NOTHING;

-- BINDINGS. `sa-lan` is deliberately left with NO gateway: it exercises the "no link exists" rendering,
-- which is a DIFFERENT fact from a link that is down and must never be drawn as one.
UPDATE nodes SET site_id = '01900000-0000-7000-8000-0000000e0001' WHERE id = '01900000-0000-7000-8000-0000000f0001';
UPDATE nodes SET site_id = '01900000-0000-7000-8000-0000000e0002' WHERE id = '01900000-0000-7000-8000-0000000f0002';
UPDATE nodes SET site_id = '01900000-0000-7000-8000-0000000e0003' WHERE id = '01900000-0000-7000-8000-0000000f0003';

-- ── SUBNETS ─────────────────────────────────────────────────────────────────────────────────────────────
-- Four APPROVED (routed) + one PENDING (populates the approval queue with a live Approve) + one PENDING that
-- OVERLAPS an approved range, so attempting to approve it renders the server's verbatim `subnet_not_disjoint`
-- refusal — the teaching text the panel exists to show, produced by the real validator rather than mocked.
INSERT INTO site_subnets (id, site_id, cidr, status, created_at)
VALUES
  ('01900000-0000-7000-8000-0000000d0001', '01900000-0000-7000-8000-0000000e0001', '10.10.0.0/16', 'approved', now() - interval '30 days'),
  ('01900000-0000-7000-8000-0000000d0002', '01900000-0000-7000-8000-0000000e0002', '10.20.0.0/16', 'approved', now() - interval '22 days'),
  ('01900000-0000-7000-8000-0000000d0003', '01900000-0000-7000-8000-0000000e0003', '10.30.0.0/16', 'approved', now() - interval '14 days'),
  ('01900000-0000-7000-8000-0000000d0004', '01900000-0000-7000-8000-0000000e0003', '10.31.0.0/24', 'approved', now() - interval '10 days'),
  ('01900000-0000-7000-8000-0000000d0005', '01900000-0000-7000-8000-0000000e0004', '10.40.0.0/16', 'pending',  now() - interval '2 hours'),
  ('01900000-0000-7000-8000-0000000d0006', '01900000-0000-7000-8000-0000000e0002', '10.30.4.0/24', 'pending',  now() - interval '40 minutes')
ON CONFLICT (id) DO NOTHING;

-- ── LINK STATE — ALL THREE TONES, DERIVED NOT DECLARED ──────────────────────────────────────────────────
-- These are `node_peer_status` rows: exactly what an agent's status report writes. The BADGE is computed
-- from their freshness by the control plane; nothing here names a health kind.
--
--   eu-west   handshake 40s ago   → fresh   → intended LINKED (and the flowing edge)
--   ap-south  handshake 20m ago   → stale   → intended DOWN
--   sa-lan    no gateway at all   → no row  → intended NO LINK (absent edge, not a red one)
INSERT INTO node_peer_status (node_id, public_key, last_handshake_at, rx_bytes, tx_bytes, updated_at)
VALUES
  ('01900000-0000-7000-8000-0000000f0001', 'ZmlY3R1cmVLZXlFVTAwMDAwMDAwMDAwMDAwMDAwMDA9', now() - interval '40 seconds', 184320041, 91238400, now()),
  ('01900000-0000-7000-8000-0000000f0002', 'ZmlY3R1cmVLZXlIVUIwMDAwMDAwMDAwMDAwMDAwMDA9', now() - interval '45 seconds', 90118400,  183001200, now()),
  ('01900000-0000-7000-8000-0000000f0001', 'ZmlY3R1cmVLZXlBUDAwMDAwMDAwMDAwMDAwMDAwMDA9', now() - interval '20 minutes', 4194304,   2097152,   now() - interval '20 minutes'),
  ('01900000-0000-7000-8000-0000000f0003', 'ZmlY3R1cmVLZXlIVUIwMDAwMDAwMDAwMDAwMDAwMDA9', now() - interval '20 minutes', 2097152,   4194304,   now() - interval '20 minutes')
ON CONFLICT (node_id, public_key) DO UPDATE
  SET last_handshake_at = EXCLUDED.last_handshake_at,
      updated_at        = EXCLUDED.updated_at;

-- ⛔ THESE UPSERT RATHER THAN DO-NOTHING, and the reason is the whole point of the fixture.
--
-- Liveness is RELATIVE TO now(). A fixture that inserts once and never updates is fresh for ninety seconds
-- and stale forever after — so the map showed every link DOWN a couple of minutes after seeding, which is
-- not the designed picture and is not a bug in the map.
--
-- A DEMO FIXTURE FOR A LIVE SYSTEM HAS TO BE RE-RUNNABLE INTO FRESHNESS. `make seed-fixtures` is now the
-- verb for "make the demo network current again", and it stays idempotent in every other respect.
UPDATE nodes SET last_seen_at = now() - interval '20 seconds'
 WHERE id IN ('01900000-0000-7000-8000-0000000f0001',
              '01900000-0000-7000-8000-0000000f0002',
              '01900000-0000-7000-8000-0000000f0004');
-- ap-south stays STALE on purpose: it is the one gateway whose offline rendering we need to see.
UPDATE nodes SET last_seen_at = now() - interval '20 minutes'
 WHERE id = '01900000-0000-7000-8000-0000000f0003';

-- ── HA HUB SET ──────────────────────────────────────────────────────────────────────────────────────────
-- Two pinned candidates, which is what crosses the HA panel's threshold so it renders the SET rather than the
-- precondition notice. `members` is ordered: [primary, standby].
UPDATE nodes SET hub_priority = 1 WHERE id = '01900000-0000-7000-8000-0000000f0001';
UPDATE nodes SET hub_priority = 2 WHERE id = '01900000-0000-7000-8000-0000000f0002';

-- NOTE: the column is `configured`, not `members` — 0043 renamed it when `demoted` was added. I wrote
-- `members` from the ORIGINAL CREATE TABLE and missed the later ALTER, which is the same error one scale
-- down as reading a screenshot instead of the source: I read ONE statement rather than the schema's history.
-- The live `\d org_hub_set` is the authority.
-- ⛔ `DO UPDATE`, NOT `DO NOTHING` — AND THAT IS A BUG FIX, NOT A PREFERENCE.
-- `make seed` already writes an org_hub_set row, so `DO NOTHING` meant THIS FIXTURE NEVER APPLIED. The live
-- `configured` held a single base-seed node, the HA panel rendered base-seed state, and nothing anywhere
-- said so. Same class as the `NET` bug and the missing OpenVPN state: a write that silently does not happen
-- looks exactly like a write that did.
--
-- WF-B: `demoted` carries the SUBORDINATE member, and `siteLinkVerdictFrom` needs it OBSERVED-AND-STALE —
-- a MISSING peer row yields nothing, because silence is not death (a reassuring subordinate on an unobserved
-- peer is the reassuring-green class one tier down, WF-B review F1).
--
-- THE DEMOTED MEMBER IS ap-south (f0003), NOT eu-west. eu-west is deliberately FRESH so the map has a linked
-- flowing edge; demoting it would have produced the note by destroying the state next to it. ap-south is
-- ALREADY observed-stale at 20 minutes and already site-bound, so it yields the demoted-dead peer while every
-- other fixture intent survives untouched — and its own offline rendering is unaffected, so both states
-- coexist on one screen.
INSERT INTO org_hub_set (org_id, configured, demoted, generation, updated_at)
VALUES ('01900000-0000-7000-8000-000000000001',
        ARRAY['01900000-0000-7000-8000-0000000f0001','01900000-0000-7000-8000-0000000f0002','01900000-0000-7000-8000-0000000f0003']::uuid[],
        ARRAY['01900000-0000-7000-8000-0000000f0003']::uuid[],
        7, now())
ON CONFLICT (org_id) DO UPDATE
  SET configured = EXCLUDED.configured,
      demoted    = EXCLUDED.demoted,
      generation = EXCLUDED.generation,
      updated_at = now();

-- ── OPENVPN: OPTED IN, AND ONE GATEWAY REFUSING LOUDLY ──────────────────────────────────────────────────
-- Registered fixture debt from the S14.6 review, trigger "S14.7 Routed Ranges visual review" — discharged.
-- Without these two writes the Gateways screen's OpenVPN panel could only ever render its not-opted-in
-- precondition, so the opted-in and the faulting branches were UNREVIEWABLE on localhost.
UPDATE organizations SET ovpn_enabled = true
 WHERE id = '01900000-0000-7000-8000-000000000001';

-- `ovpn_health` is NOT a column: it rides `nodes.capabilities`, which the control plane builds server-side
-- from the agent's typed report (a compromised agent cannot inject arbitrary JSON). Seeding the REPORT is
-- therefore the honest fixture — the health kind stays derived, exactly as `node_peer_status` is for liveness.
UPDATE nodes SET capabilities = capabilities || '{"ovpn_health":"ovpn_certs_absent"}'::jsonb
 WHERE id = '01900000-0000-7000-8000-0000000f0002';

-- ── CROSS-SITE DNS ──────────────────────────────────────────────────────────────────────────────────────
-- Two clean zones plus ONE ORG-WIDE CONFLICT: `*.corp` resolves differently depending on the site, which is
-- exactly the invariant the org-wide panel exists to surface and which no per-site view could show.
UPDATE sites SET dns_forwarding = '[{"domain":"*.eu.corp","resolver_ip":"10.20.0.53"},{"domain":"*.corp","resolver_ip":"10.20.0.53"}]'::jsonb
 WHERE id = '01900000-0000-7000-8000-0000000e0002' AND dns_forwarding = '[]'::jsonb;
UPDATE sites SET dns_forwarding = '[{"domain":"*.corp","resolver_ip":"10.10.0.53"}]'::jsonb
 WHERE id = '01900000-0000-7000-8000-0000000e0001' AND dns_forwarding = '[]'::jsonb;

-- ── DEVICES ─────────────────────────────────────────────────────────────────────────────────────────────
-- Connected / idle / revoked, owned by the seeded users. Pending-approval and posture states are ENTERPRISE
-- surfaces and live in the enterprise fixture file, so this one stays honest on an open stack.
-- NOTE: `devices` carries NO last_handshake_at — device liveness lives in `device_status`, the same
-- split as nodes/node_peer_status. Read from the LIVE schema after two column guesses failed; the migration
-- files describe the schema's HISTORY, the database describes its STATE, and only one of those is authority.
INSERT INTO devices (id, org_id, user_id, node_id, name, platform, public_key, assigned_ip, status, created_at, full_tunnel)
VALUES
  ('01900000-0000-7000-8000-0000000c0001', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', '01900000-0000-7000-8000-0000000f0001', 'macbook-owner', 'darwin',  'ZmlY3R1cmVEZXYwMDEwMDAwMDAwMDAwMDAwMDAwMDA9', '10.99.0.11', 'active',  now() - interval '20 days', false),
  ('01900000-0000-7000-8000-0000000c0002', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000003', '01900000-0000-7000-8000-0000000f0002', 'thinkpad-erin', 'windows', 'ZmlY3R1cmVEZXYwMDIwMDAwMDAwMDAwMDAwMDAwMDA9', '10.99.0.12', 'active',  now() - interval '12 days', true),
  ('01900000-0000-7000-8000-0000000c0003', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000003', '01900000-0000-7000-8000-0000000f0003', 'pixel-erin',    'android', 'ZmlY3R1cmVEZXYwMDMwMDAwMDAwMDAwMDAwMDAwMDA9', '10.99.0.13', 'active',  now() - interval '9 days',  false),
  ('01900000-0000-7000-8000-0000000c0004', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', '01900000-0000-7000-8000-0000000f0001', 'ipad-owner',    'ios',     'ZmlY3R1cmVEZXYwMDQwMDAwMDAwMDAwMDAwMDAwMDA9', '10.99.0.14', 'active',  now() - interval '3 days',  false),
  ('01900000-0000-7000-8000-0000000c0005', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000003', '01900000-0000-7000-8000-0000000f0002', 'old-laptop',    'linux',   'ZmlY3R1cmVEZXYwMDUwMDAwMDAwMDAwMDAwMDAwMDA9', NULL,         'revoked', now() - interval '60 days', false)
ON CONFLICT (id) DO NOTHING;

-- Device liveness is a SEPARATE table, exactly as gateway liveness is. Two connected, one idle-but-seen,
-- one that has NEVER handshaked (no row at all — enrolled, never connected, which is a different fact from
-- idle and the Devices screen must not render them alike).
INSERT INTO device_status (device_id, last_handshake_at, rx_bytes, tx_bytes, updated_at)
VALUES
  ('01900000-0000-7000-8000-0000000c0001', now() - interval '30 seconds', 52428800, 10485760, now()),
  ('01900000-0000-7000-8000-0000000c0002', now() - interval '55 seconds', 83886080, 20971520, now()),
  ('01900000-0000-7000-8000-0000000c0003', now() - interval '4 hours',    1048576,  524288,   now() - interval '4 hours')
ON CONFLICT (device_id) DO NOTHING;

UPDATE devices SET revoked_at = now() - interval '15 days'
 WHERE id = '01900000-0000-7000-8000-0000000c0005' AND revoked_at IS NULL;

-- ── AUDIT ───────────────────────────────────────────────────────────────────────────────────────────────
-- Real action names taken from the product's own emitters, across both actor kinds. `actor_user_id IS NULL`
-- is how a SYSTEM actor is stored — the reconciler acting on its own, which the Audit Log renders
-- first-class and which a human-only fixture set would never show.
-- `actor_system` is FIRST-CLASS (S7.x) and it is TEXT — the NAME of the system actor, not a boolean. A
-- CHECK enforces `actor_user_id IS NULL OR actor_system IS NULL`: exactly one kind of actor per row. A system
-- action seeded as a NULL user would render as 'unknown', which is the opposite of the point.
INSERT INTO audit_logs (id, org_id, actor_user_id, actor_system, action, target_type, target_id, metadata, created_at)
VALUES
  ('01900000-0000-7000-8000-0000000b0001', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'site.create',        'site',   'us-east-dc',    '{"name":"us-east-dc"}',                    now() - interval '30 days'),
  ('01900000-0000-7000-8000-0000000b0002', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'site.bind_node',     'site',   'us-east-dc',    '{"node":"gw-us-east"}',                    now() - interval '30 days'),
  ('01900000-0000-7000-8000-0000000b0003', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'site.subnet_approve','subnet', '10.10.0.0/16',  '{"site":"us-east-dc"}',                    now() - interval '30 days'),
  ('01900000-0000-7000-8000-0000000b0004', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'site.create',        'site',   'eu-lan',        '{"name":"eu-lan"}',                        now() - interval '22 days'),
  ('01900000-0000-7000-8000-0000000b0005', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'device.create',      'device', 'thinkpad-erin', '{"transport":"wireguard"}',                now() - interval '12 days'),
  ('01900000-0000-7000-8000-0000000b0006', '01900000-0000-7000-8000-000000000001', NULL, 'reconciler',                                   'hub_set.promotion',  'org',    'hub-set',       '{"generation":7,"cause":"primary_stale"}', now() - interval '6 days'),
  ('01900000-0000-7000-8000-0000000b0007', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'device.revoke',      'device', 'old-laptop',    '{"reason":"decommissioned"}',              now() - interval '15 days'),
  ('01900000-0000-7000-8000-0000000b0008', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'node.revoke',        'node',   'gw-retired-1',  '{"reason":"hardware_retired"}',            now() - interval '9 days'),
  ('01900000-0000-7000-8000-0000000b0009', '01900000-0000-7000-8000-000000000001', NULL, 'reconciler',                                   'node.reconcile',     'node',   'gw-ap-south',   '{"result":"routes_pushed"}',               now() - interval '3 days'),
  ('01900000-0000-7000-8000-0000000b000a', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'site.create',        'site',   'sa-lan',        '{"name":"sa-lan"}',                        now() - interval '6 days'),
  ('01900000-0000-7000-8000-0000000b000b', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000003', NULL, 'device.create',      'device', 'pixel-erin',    '{"transport":"wireguard"}',                now() - interval '9 days'),
  ('01900000-0000-7000-8000-0000000b000c', '01900000-0000-7000-8000-000000000001', '01900000-0000-7000-8000-000000000002', NULL, 'site.subnet_advertise','subnet','10.40.0.0/16',  '{"site":"sa-lan"}',                        now() - interval '2 hours')
ON CONFLICT (id) DO NOTHING;

COMMIT;
