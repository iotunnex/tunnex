# EPIC 13 walk — RECORD

Branch `story/S13.1-gateway-recovery` · CP sha `9f7c56f` · edition **enterprise** · schema **64**
Rig: `docs/infra-inventory.md`. Runsheet: `docs/S13-boxwalk.md`.

**TTL: `TUNNEX_AGENT_CERT_TTL=10m` for this run — this is a REHEARSAL.** It exercises the mechanics and the code
paths; it does not exercise the shipped 48-hour behaviour. Every leg below records the TTL it ran under, and a
pass at 10m SUBSTITUTES for nothing at 48h.

---

## WF-S13-1 — no surface can choose which gateway a device is homed on, and "active" is the wrong test

**Found during staging, before any leg ran.** Severity **MEDIUM** (it fails loudly on one path and silently on
another — see below). HELD for disposition; not fixed.

### What happened

Creating a device in the UI returned:

> *the node has not reported its endpoint/key yet; ensure the agent is enrolled and TUNNEX_NODE_ENDPOINT is set*

The device form has a name and a type and **no gateway picker**. `Devices.tsx` calls
`defaultDeviceNode(nodes)` → `selectableNodes(nodes)[0]` (`apps/web/src/lib/nodepick.ts`), i.e. the FIRST node
with `status === "active"` in `created_at` order. On this fleet that is **azure-gw** — active, but its agent has
been gone six days and it **never reported an endpoint at all**. The server's own guard refused, correctly.

### Why it is not just "azure-gw should have been revoked"

**`active` is the wrong predicate for "can host a device."** EPIC 11's finding S13-1 fixed `nodes[0]` →
`active[0]`, which removed *revoked* gateways from selection. This is the same shape one predicate over: a
gateway that is `active` but has never reported, or has been offline for days with an expired certificate, cannot
serve a device either.

**And the next node in line is worse.** Revoking `azure-gw` to clear position 0 promotes **aws-gw-2** — which HAS
reported an endpoint and key, so device creation would **succeed** and home the device on a gateway that has been
offline six days with an expired certificate. The failure would be silent, and the one-time config unusable. That
is exactly what `nodepick.ts`'s own doc comment warns about:

> *"homing a device on a dead gateway produces a one-time config that can never connect, and a one-time secret
> cannot be re-issued — so the failure is not merely inconvenient, it burns the artifact."*

The module reasoned its way to the right principle and then encoded a predicate that does not enforce it.

### Two consumers, one rule, neither able to choose

| surface | selection | can the operator choose? |
|---|---|---|
| `apps/web` `Devices.tsx` | `selectableNodes(nodes)[0]` | **no** — no picker in the form |
| `apps/cli` `internal/cli/device.go:44-48` | first `n.Status == "active"` | **no** — `device create` has `--name` and `--full-tunnel` only |

This is the four-surface census's *"surfaces that CHOOSE a gateway"* row, counted at two, with the same defect in
both. On a single-gateway deployment neither is visible; on a four-gateway fleet the target is a guess.

### Suggested direction (NOT ruled)

1. Narrow selection to gateways that can actually serve: reported endpoint AND public key, unexpired certificate,
   and a recent `last_seen_at`.
2. Give both surfaces an explicit target — a picker in the web form, `--gateway` on the CLI — because on a
   multi-gateway fleet a default is a guess whatever the predicate.
3. When nothing is selectable, say which condition failed. "No gateway available" and "your gateways are all
   offline" send an operator to different places.

### Consequence for this walk — DECLARED STAGING

No product path can home a device on `aws-gw-1`, so the walk's devices are created through the API with an
explicit `node_id`. **That is a workaround for WF-S13-1, not a walk procedure**, and it is recorded here rather
than performed quietly: the zero-touch bar applies to recovery, and this is device staging, but a reader must be
able to tell which commands were the product working and which were us going around it.
