// policyview — PURE view-models for the S7.4a Zero Trust admin UI. No React, no DOM,
// no network: every consequential decision lives here as a pure function so it is
// unit-tested directly (kit-minimum — no component-render harness). The Access page
// and its sections are thin shells that call these.
import { can } from "./rbac";
import type { Role, UserGroup, Resource, PolicyRule, Member, Loaded } from "./api";

// roleFromMembers resolves the actor's role from the roster load ([0] fix). A FAILED
// members load must NOT read as "no role" — that silently downgrades an admin to the
// member gate (a false lockout from their own admin surface). Distinguish role-unknown-
// because-the-fetch-FAILED from a genuine member: `failed` true → the caller shows
// "couldn't determine your role — retry", never the manage-gated-away notice.
export function roleFromMembers(loaded: Loaded<Member[]>, myId: string): { role?: Role; failed: boolean } {
  if (!loaded.ok) return { failed: true };
  return { role: loaded.data.find((m) => m.user_id === myId)?.role, failed: false };
}

// ── D-a4: mode-enable confirm copy = a pure function of the ALLOW-RULE COUNT ────────
// NOT a computed blast radius (that would reimplement the compiler client-side — a
// divergent source of truth, D-A1). Zero rules surfaces the S7.1 default-deny footgun.
export interface ConfirmCopy {
  title: string;
  body: string;
  danger: boolean; // the zero-rules case is the strong, danger-styled gate
  confirmLabel: string;
}

export function modeEnableConfirm(ruleCount: number): ConfirmCopy {
  if (ruleCount <= 0) {
    return {
      title: "Enable enforcing with NO allow rules?",
      body:
        "You have no allow rules. Enabling Enforcing denies ALL traffic — including your own access — " +
        "until you add rules. Continue?",
      danger: true,
      confirmLabel: "Enable anyway",
    };
  }
  const n = `${ruleCount} allow rule${ruleCount === 1 ? "" : "s"}`;
  return {
    title: "Enable enforcing?",
    body: `Enforcing denies all traffic except what your rules allow — you have ${n}. Continue?`,
    danger: false,
    confirmLabel: "Enable enforcing",
  };
}

// ── Bug law (S7.4a fold-2): legibility signals COMPOSE, never compete ────────────────
// An error state may replace CONTENT, never another WARNING. A partial-swap notice (a stale
// enforcing rule is still active, D-a5) must render even when a coincident reload failed
// ([291]). sectionRender is the pure render-plan: retry replaces the list, but the notice
// always shows when set.
export interface SectionRender {
  showRetry: boolean;
  showContent: boolean;
  showNotice: boolean;
}
export function sectionRender(loadError: string | null, notice: string | null): SectionRender {
  return { showRetry: !!loadError, showContent: !loadError, showNotice: !!notice };
}

// The partial-swap notice is DERIVED from ONE state — the SET of rule ids a create-then-delete
// left un-deleted (staleRuleIds). No separate `notice` state exists, so the two can never
// desync ([291]/[309]/[371] are structurally impossible). A SET (not a single id) so sequential
// partials each stay tracked — a second partial never orphans the first's warning (amendment B).
export function staleNoticeText(staleRuleIds: string[]): string | null {
  if (staleRuleIds.length === 0) return null;
  if (staleRuleIds.length === 1) return swapPartialMessage(staleRuleIds[0].slice(0, 8));
  return `${staleRuleIds.length} rules could not be removed after an edit — they are still active. Retry the removals.`;
}

// pruneStaleRuleIds is the ONLY clear path. AMENDMENT A: it prunes ONLY on a SUCCESSFUL rules
// load (`loadOk`) — a failed/transient load must NEVER satisfy the clear (that would be [291]
// via the clear path). On success, keep per-id only the ids still present in the fresh list
// (amendment B) — so a resolved stale rule clears while others persist.
export function pruneStaleRuleIds(staleRuleIds: string[], loadOk: boolean, rules: PolicyRule[]): string[] {
  if (!loadOk) return staleRuleIds; // A: never clear on a failed load
  return staleRuleIds.filter((id) => rules.some((r) => r.id === id));
}

// ── Parent access-page gate as a PURE function ([75]+[101]) ──────────────────────────
// The upsell needs only EDITION (role-irrelevant); the admin body needs ROLE RESOLVED. A
// members-load failure must NOT blank a non-enterprise user's upsell ([75]), and role
// in-flight must render "loading", never the manage-gated-away notice ([101]).
export type AccessView =
  | "loading"
  | "fatal"
  | "load_retry"
  | "upsell"
  | "role_retry"
  | "role_loading"
  | "member_gate"
  | "admin_body";

export function accessView(i: {
  fatal: boolean;
  loadError: boolean;
  editionReady: boolean; // meta + org both loaded
  isEnterprise: boolean;
  roleError: boolean;
  roleResolved: boolean;
  canView: boolean;
}): AccessView {
  if (i.fatal) return "fatal";
  if (i.loadError) return "load_retry";
  if (!i.editionReady) return "loading";
  if (!i.isEnterprise) return "upsell"; // [75]: role irrelevant here — never role_retry
  if (i.roleError) return "role_retry";
  if (!i.roleResolved) return "role_loading"; // [101]: never the gate copy while role in-flight
  return i.canView ? "admin_body" : "member_gate";
}

// ── policy RBAC + edition gate (pure) ───────────────────────────────────────────────
// Whole feature is enterprise-gated; view needs policy:view; managing needs
// policy:manage AND a verified email (mirrors the server's verified-email requirement
// on mutating calls). Device-approval management is the separate device:approve grant.
export interface PolicyGate {
  isEnterprise: boolean;
  canView: boolean;
  canManagePolicy: boolean;
  canManageDevices: boolean;
  // S7.5.3: posture-check config is its OWN grant (device_health:manage) — deliberately
  // not a reuse of device:approve (approval and health are orthogonal governance axes).
  canManageDeviceHealth: boolean;
}

export function policyGate(input: {
  role: Role | undefined;
  emailVerified: boolean;
  edition: string | undefined;
}): PolicyGate {
  const isEnterprise = input.edition === "enterprise";
  const canView = isEnterprise && can(input.role, "policy:view");
  return {
    isEnterprise,
    canView,
    canManagePolicy: canView && input.emailVerified && can(input.role, "policy:manage"),
    canManageDevices: isEnterprise && input.emailVerified && can(input.role, "device:approve"),
    canManageDeviceHealth: isEnterprise && input.emailVerified && can(input.role, "device_health:manage"),
  };
}

// ── D-a6: ID→name join, NEVER omit, and DELETED ≠ UNRESOLVED ─────────────────────────
// A rule the server is enforcing must always be visible even if its referents are broken
// (the UI never hides live policy). The label must not LIE about why a referent is
// missing: absent from a SUCCESSFULLY-LOADED set = "deleted"; the set FAILED TO LOAD =
// "unresolved — refresh" (so a transient fetch failure can't render healthy policy as
// broken — the false-alarm class this project hit at staleness/desync/migration).
export type RefState = "ok" | "deleted" | "unresolved";

export interface RefLabel {
  id: string;
  label: string;
  state: RefState;
}

export interface RuleRow {
  id: string;
  src: RefLabel;
  dst: RefLabel;
  /** true if either end is not "ok" — the row renders a warning marker but is NEVER hidden. */
  broken: boolean;
}

// loaded flags say whether each referent SET loaded successfully. When a set failed to
// load we cannot tell deleted from present, so an unfound ref is "unresolved", not "deleted".
export interface LoadState {
  groupsLoaded: boolean;
  resourcesLoaded: boolean;
  membersLoaded?: boolean; // S7.5.4: for resolving a per-user subject to a member name
}

function short(id: string): string {
  return id.length > 8 ? `${id.slice(0, 8)}…` : id;
}

function resolveUser(id: string, members: Member[], loaded: boolean): RefLabel {
  const m = members.find((x) => x.user_id === id);
  if (m) return { id, label: m.name || m.email, state: "ok" };
  if (!loaded) return { id, label: `unresolved user ${short(id)} — refresh`, state: "unresolved" };
  // A per-user grant whose subject is no longer a member (the src_user_id→memberships
  // cascade should delete such a rule, so this is a transient/edge render, shown honestly).
  return { id, label: `removed user ${short(id)}`, state: "deleted" };
}

function resolveGroup(id: string, groups: UserGroup[], loaded: boolean): RefLabel {
  const g = groups.find((x) => x.id === id);
  if (g) return { id, label: g.name, state: "ok" };
  if (!loaded) return { id, label: `unresolved group ${short(id)} — refresh`, state: "unresolved" };
  return { id, label: `deleted group ${short(id)}`, state: "deleted" };
}

function resolveResource(id: string, resources: Resource[], loaded: boolean): RefLabel {
  const r = resources.find((x) => x.id === id);
  if (r) return { id, label: r.name, state: "ok" };
  if (!loaded) return { id, label: `unresolved resource ${short(id)} — refresh`, state: "unresolved" };
  return { id, label: `deleted resource ${short(id)}`, state: "deleted" };
}

export function ruleRow(
  rule: PolicyRule,
  groups: UserGroup[],
  resources: Resource[],
  members: Member[],
  loaded: LoadState,
): RuleRow {
  // S7.5.4: a rule's source is a group OR a single user — resolve each to a name,
  // honestly (a removed-user or deleted-group ref shows distinctly, never mislabeled).
  const src: RefLabel =
    rule.src_kind === "user"
      ? resolveUser(rule.src_user_id ?? "", members, loaded.membersLoaded ?? false)
      : resolveGroup(rule.src_group_id ?? "", groups, loaded.groupsLoaded);
  const dst =
    rule.dst_kind === "group"
      ? resolveGroup(rule.dst_group_id ?? "", groups, loaded.groupsLoaded)
      : resolveResource(rule.dst_resource_id ?? "", resources, loaded.resourcesLoaded);
  return { id: rule.id, src, dst, broken: src.state !== "ok" || dst.state !== "ok" };
}

// ── S7.5.4 temporary-grant expiry (the linger model — expired grants stay VISIBLE) ────

export type GrantExpiryState = "permanent" | "active" | "expired";

export interface GrantExpiry {
  state: GrantExpiryState;
  label: string; // "permanent" | "expires in 3h" | "expired 2h ago"
  /** A temporary grant offers Extend. A LAPSED one still offers it (the server 409s
   *  grant_lapsed, surfaced legibly) — the linger model shows expired-but-present. */
  extendable: boolean;
}

export function grantExpiry(rule: Pick<PolicyRule, "expires_at">, now: number): GrantExpiry {
  if (!rule.expires_at) return { state: "permanent", label: "permanent", extendable: false };
  const exp = new Date(rule.expires_at).getTime();
  if (exp <= now) return { state: "expired", label: `expired ${compactSpan(now - exp)} ago`, extendable: true };
  return { state: "active", label: `expires in ${compactSpan(exp - now)}`, extendable: true };
}

function compactSpan(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// extendErrorCopy maps the server's typed 409 codes to legible copy (never a raw error).
export function extendErrorCopy(code: string | undefined): string {
  switch (code) {
    case "grant_lapsed":
      return "This grant already expired — create a new one instead of extending.";
    case "not_temporary":
      return "This is a permanent grant — it has no expiry to extend.";
    default:
      return "Could not extend the grant.";
  }
}

// ── S7.5.4 flow-event source attribution legibility (v3 device/user, rider 1) ─────────
// The report-absent law (same as the S7.5.3 posture tri-state): a device stamped but user
// unresolved shows "device X · user unknown" — never a blank/dash that reads as "no device"
// or as fine. Absence is VISIBLY absence.
export interface FlowAttribution {
  deviceId?: string | null;
  userId?: string | null;
  deviceName?: string; // resolved display name if available
  userName?: string;
}

export function attributionLabel(a: FlowAttribution): string {
  const dev = a.deviceId ? a.deviceName ?? `device ${short(a.deviceId)}` : null;
  const user = a.userId ? a.userName ?? a.userId : null;
  if (!dev && !user) return "unattributed"; // no device stamped (src had no grant) — honest, not blank
  if (dev && !user) return `${dev} · user unknown`; // device known, user unresolved — ABSENCE visible
  if (!dev && user) return user; // (unusual: user derives from device CP-side)
  return `${dev} · ${user}`;
}

// activeMembers filters a roster to CURRENT active members — the D1 constraint mirrored
// client-side so the user picker never offers a non-member (which the server would 4xx).
export function activeMembers(members: Member[]): Member[] {
  return members.filter((m) => m.status === "active");
}

// ── D-a5: rule edit = CREATE-THEN-DELETE, gap-free, with a LEGIBLE partial outcome ──
// No updatePolicyRule server-side. Create the new rule FIRST; only on success delete the
// old. Gap-free because grants are an allow-only UNION — a transient duplicate is a no-op
// (S7.1 semantics), and nothing is freed/re-claimed (unlike the S3.5 IP cap). Delete-first
// is FORBIDDEN (mid-edit access gap + rule loss on a failed recreate). A create-ok/delete-
// fail leaves a DUPLICATE that must be LEGIBLE (partial-success + both rules shown + retry),
// because a duplicate nobody knows about is how a "deleted" rule keeps granting access
// (S7.3-[0] failure-must-be-legible).
export type SwapOutcome =
  | { outcome: "replaced"; newId: string }
  | { outcome: "create_failed"; error: unknown }
  | { outcome: "partial"; newId: string; oldId: string; error: unknown };

export async function swapRule(
  oldId: string,
  createNew: () => Promise<{ id: string } | { error: unknown }>,
  deleteOld: (id: string) => Promise<{ error?: unknown } | void>,
): Promise<SwapOutcome> {
  const created = await createNew();
  if ("error" in created) return { outcome: "create_failed", error: created.error };
  // Create succeeded → old rule still present (no gap). Now remove the old one.
  const del = await deleteOld(oldId);
  if (del && typeof del === "object" && "error" in del && del.error) {
    // Duplicate persists — surface it, list BOTH, offer retry. NEVER silent.
    return { outcome: "partial", newId: created.id, oldId, error: del.error };
  }
  return { outcome: "replaced", newId: created.id };
}

export function swapPartialMessage(oldIdShort: string): string {
  return `New rule created, but the old rule (${oldIdShort}) could not be removed — it is still active. Retry the removal.`;
}
