import type { Node } from "./api";

/**
 * Node-selection rules for surfaces that must choose a gateway (S13.1 Slice 3, the four-surface decisions
 * census).
 *
 * WHY THIS IS A MODULE AND NOT `nodes[0]`. The device-create path used `nodes[0].id` directly, and the list it
 * indexes comes from `ListNodes` — every node in the org, revoked included, ordered by `created_at`. So on any
 * deployment whose OLDEST gateway had been revoked, every new device was homed on a dead gateway and issued a
 * config pointing at nothing. The EPIC 11 walk's own fleet was in exactly that state: `aws-gw-1` was revoked and
 * had the earliest `created_at`, so it WAS `nodes[0]`.
 *
 * The same page's sibling surfaces got it right — `Sites.tsx` filters `status === "active"` before offering a
 * gateway — which is the asymmetry class this epic keeps finding: two consumers of one list, one of them
 * remembering. Selection lives here now so there is one rule and it is testable.
 */

/** Gateways eligible to receive new work. Revoked gateways can neither renew nor reconcile. */
export function selectableNodes(nodes: Node[]): Node[] {
  return nodes.filter((n) => n.status === "active");
}

/**
 * The gateway a new device should be homed on, or null when there is none.
 *
 * Returns null rather than falling back to a revoked gateway: homing a device on a dead gateway produces a
 * one-time config that can never connect, and a one-time secret cannot be re-issued — so the failure is not
 * merely inconvenient, it burns the artifact. Refusing is the recoverable direction.
 */
export function defaultDeviceNode(nodes: Node[]): Node | null {
  return selectableNodes(nodes)[0] ?? null;
}

/**
 * Display label for a node in a list that may contain several rows sharing a name.
 *
 * Migration 0056 made `(org_id, name)` unique only among non-revoked rows, so a name may be held by several
 * revoked gateways plus at most one active one. Any surface listing revoked rows can therefore show duplicate
 * labels, and a label alone stops identifying a gateway.
 *
 * The census decision for DISPLAYS: keep the name, and mark the revoked ones — the status is what disambiguates,
 * and it is information the operator needs anyway. (Surfaces that OFFER a gateway resolve it differently: they
 * filter to active via selectableNodes, so duplicates cannot arise there at all.)
 */
export function nodeLabel(n: Pick<Node, "name" | "status">): string {
  return n.status === "revoked" ? `${n.name} (revoked)` : n.name;
}
