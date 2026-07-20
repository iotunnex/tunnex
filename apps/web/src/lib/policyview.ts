// policyview — PURE view-models for the S7.4a Zero Trust admin UI. No React, no DOM,
// no network: every consequential decision lives here as a pure function so it is
// unit-tested directly (kit-minimum — no component-render harness). The Access page
// and its sections are thin shells that call these.
import { can } from "./rbac";
import type { Role, UserGroup, Resource, PolicyRule, Member, Loaded, CreatePolicyRuleRequest, Site } from "./api";

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
  /**
   * S8.7 warn-not-refuse (D1): the SERVER's read-time judgment that a src_kind='cidr' rule's CIDR is inside
   * no current org range — a rule matching nothing (a reassuring-rule). Rendered VERBATIM from the served
   * `cidr_outside_org_ranges` field; the UI NEVER re-derives org ranges (one-validator). Self-clears when the
   * server re-derives on the next list (a range landed). Distinct from `broken` — an out-of-world CIDR is a
   * VALID rule that warns, not a broken reference.
   */
  cidrOutsideRanges: boolean;
}

// loaded flags say whether each referent SET loaded successfully. When a set failed to
// load we cannot tell deleted from present, so an unfound ref is "unresolved", not "deleted".
export interface LoadState {
  groupsLoaded: boolean;
  resourcesLoaded: boolean;
  membersLoaded?: boolean; // S7.5.4: for resolving a per-user subject to a member name
  sitesLoaded?: boolean; // S8.2c WF-8: for resolving a site subject to its NAME (not the raw UUID)
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

// resolveSite (WF-8): render a site subject by its NAME. The raw truncated UUID was both unreadable
// AND ambiguous — sites are UUIDv7 (time-ordered), so two sites created seconds apart share a prefix
// (`019f762b…`) and rendered identically. Falls back to "site <id>" only when the sites set is
// unavailable (can't tell deleted from present), matching the group/resource honesty.
function resolveSite(id: string, sites: Site[], loaded: boolean): RefLabel {
  const s = sites.find((x) => x.id === id);
  if (s) return { id, label: `site ${s.name}`, state: "ok" };
  if (!loaded) return { id, label: `site ${short(id)} — refresh`, state: "unresolved" };
  return { id, label: `deleted site ${short(id)}`, state: "deleted" };
}

export function ruleRow(
  rule: PolicyRule,
  groups: UserGroup[],
  resources: Resource[],
  members: Member[],
  sites: Site[],
  loaded: LoadState,
): RuleRow {
  // S7.5.4: a rule's source is a group OR a single user (S8.2: OR a site) — resolve each to a NAME,
  // honestly (a removed-user / deleted-group / deleted-site ref shows distinctly, never mislabeled).
  const src: RefLabel =
    rule.src_kind === "user"
      ? resolveUser(rule.src_user_id ?? "", members, loaded.membersLoaded ?? false)
      : rule.src_kind === "site" // WF-8: resolve to the site NAME, not the ambiguous UUIDv7 prefix
        ? resolveSite(rule.src_site_id ?? "", sites, loaded.sitesLoaded ?? false)
        : rule.src_kind === "cidr" // S8.7: a literal CIDR — a VALUE, never a referent, so always "ok"
          ? { id: rule.src_cidr ?? "", label: rule.src_cidr ?? "cidr", state: "ok" }
          : resolveGroup(rule.src_group_id ?? "", groups, loaded.groupsLoaded);
  // S8.1: dst_kind may be 'site' (a site-subnet grant) — resolve it to a site NAME (WF-8), NOT the
  // resource branch (which would render a valid site rule as a broken 'deleted resource'), preserving
  // the never-mislabeled invariant.
  const dst: RefLabel =
    rule.dst_kind === "group"
      ? resolveGroup(rule.dst_group_id ?? "", groups, loaded.groupsLoaded)
      : rule.dst_kind === "site"
        ? resolveSite(rule.dst_site_id ?? "", sites, loaded.sitesLoaded ?? false)
        : resolveResource(rule.dst_resource_id ?? "", resources, loaded.resourcesLoaded);
  // S8.7: the warn is the SERVER's read-time field, rendered verbatim (no client-side org-range re-check).
  return { id: rule.id, src, dst, broken: src.state !== "ok" || dst.state !== "ok", cidrOutsideRanges: rule.cidr_outside_org_ranges };
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

// ── S8.3 CP: the rules summary line (states ENUMERATED, derived from a Loaded<T>) ──────
// The one-line posture summary atop the rules list. It derives from the LOAD RESULTS, never from an empty
// default: a FAILED rules load must never render "0 rules — all denied" (the reassuring-empty class on the
// loudest line). enforcing+0 is the LOUD legibility-law state (a live default-deny with no rules).
export type RulesSummaryState = "loading" | "failed" | "off" | "enforcing_empty" | "enforcing";

export interface RulesSummaryView {
  state: RulesSummaryState;
  text: string;
  loud: boolean; // the enforcing-empty lockout — rendered prominently, not a caption
}

export function rulesSummary(i: {
  modeResult: Loaded<"off" | "enforcing"> | null; // null = still loading
  rulesResult: Loaded<number> | null; // the rule COUNT from a real load; null = still loading
}): RulesSummaryView {
  if (!i.modeResult || !i.rulesResult) return { state: "loading", text: "…", loud: false };
  // A failed load (mode OR rules) cannot render a truthful posture → say so, never a defaulted count.
  if (!i.modeResult.ok || !i.rulesResult.ok) return { state: "failed", text: "Rule status unavailable — refresh.", loud: false };
  if (i.modeResult.data === "off") return { state: "off", text: "Policy not enforced — open mesh.", loud: false };
  const n = i.rulesResult.data;
  if (n === 0) return { state: "enforcing_empty", text: "0 rules — ALL traffic denied.", loud: true };
  return { state: "enforcing", text: `${n} ${n === 1 ? "rule" : "rules"} — default-deny active.`, loud: false };
}

// ── S8.2c D5: the rule-create body (PURE, so the site-subject branches are unit-tested) ───────────────
// The Access builder now creates group/user/SITE sources and group/resource/SITE destinations, all through
// the SAME policies API (validation + audit intact — the demo's raw DB insert was the anti-pattern this
// closes). Exactly ONE of each side's id fields is set; expiry is CREATE-only.
// defaultDstKind / defaultSrcKind pick the modal's initial subject kind so a fresh org opens on a kind that
// actually HAS options — otherwise the required select is empty and Create stays disabled with no obvious
// reason (re-review #4: the src-side fix left the dst side able to dead-end). Priority: an existing rule's
// kind (edit) → else groups if present → else the other available kind. PURE (unit-pins the dead-end fix).
export function defaultDstKind(i: {
  editingKind?: "group" | "resource" | "site";
  hasGroups: boolean;
  hasResources: boolean;
  hasSites: boolean;
}): "group" | "resource" | "site" {
  if (i.editingKind) return i.editingKind;
  if (i.hasGroups) return "group";
  if (i.hasResources) return "resource";
  if (i.hasSites) return "site";
  return "group"; // empty org — the modal isn't reachable (Add-rule gated), so any value is inert
}

export function defaultSrcKind(i: {
  editingKind?: "group" | "user" | "site" | "cidr";
  hasGroups: boolean;
  hasSites: boolean;
}): "group" | "user" | "site" | "cidr" {
  if (i.editingKind) return i.editingKind;
  if (i.hasGroups) return "group";
  if (i.hasSites) return "site"; // a no-groups site org creates site→ rules; users alone can't open the modal
  return "group";
}

export interface RuleBodyInput {
  srcKind: "group" | "user" | "site" | "cidr";
  dstKind: "group" | "resource" | "site";
  src: string; // group id
  srcUser: string;
  srcSite: string;
  srcCidr: string; // S8.7: literal source CIDR (src_kind='cidr')
  dstGroup: string;
  dstResource: string;
  dstSite: string;
  expiresAt: string; // datetime-local, "" = permanent
  editing: boolean; // expiry is create-only
}

export function ruleBody(i: RuleBodyInput): CreatePolicyRuleRequest {
  const srcPart =
    i.srcKind === "user"
      ? { src_kind: "user" as const, src_user_id: i.srcUser }
      : i.srcKind === "site"
        ? { src_kind: "site" as const, src_site_id: i.srcSite }
        : i.srcKind === "cidr" // S8.7: a literal source CIDR (free-text, server-validated)
          ? { src_kind: "cidr" as const, src_cidr: i.srcCidr }
          : { src_kind: "group" as const, src_group_id: i.src };
  const dstPart =
    i.dstKind === "group"
      ? { dst_kind: "group" as const, dst_group_id: i.dstGroup }
      : i.dstKind === "site"
        ? { dst_kind: "site" as const, dst_site_id: i.dstSite }
        : { dst_kind: "resource" as const, dst_resource_id: i.dstResource };
  const expiry = !i.editing && i.expiresAt ? { expires_at: new Date(i.expiresAt).toISOString() } : {};
  return { ...srcPart, ...dstPart, ...expiry };
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

// canEditRuleInModal: the rule-EDIT (swap) modal only rewrites group/resource grants with a group/user
// source (create-then-delete). A rule whose DST is a site (S8.1) OR whose SRC is a site (S8.2) must NOT be
// editable there — editing would silently rewrite it into a group/resource rule, a policy MUTATION
// disguised as a display limitation. Site rules are CREATED via the Access rule builder (S8.2c D5) and
// managed via the API; only in-place EDIT is withheld here. (The read-side kind coercion in the modal is
// display-only; this blocks the WRITE path.)
export function canEditRuleInModal(rule: { src_kind?: string; dst_kind: string }): boolean {
  return rule.dst_kind !== "site" && rule.src_kind !== "site";
}
