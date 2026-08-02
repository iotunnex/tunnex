import { useCallback, useEffect, useState } from "react";
import {
  api,
  apiErrorMessage,
  apiErrorCode,
  loadOne,
  type Loaded,
  type Meta,
  type Org,
  type Member,
  type Role,
  type UserGroup,
  type Resource,
  type Site,
  type K8sService,
  type PolicyRule,
  type ZeroTrustMode,
  type AffectedDevice,
  type DeviceApproval,
  type Device,
  type HealthCheck,
  type CreatePolicyRuleRequest,
} from "../lib/api";
import { useAuth } from "../lib/auth";
import { portLabel } from "../lib/k8sview";
import {
  Button,
  Card,
  ErrorText,
  Field,
  Input,
  Modal,
  Select,
} from "../components/ui";
import { ComposeGate } from "../components/ComposeGate";
import { LoadRetry } from "../components/LoadRetry";
import {
  accessView,
  modeEnableConfirm,
  policyGate,
  roleFromMembers,
  ruleRow,
  disableConfirmText,
  sectionRender,
  staleNoticeText,
  pruneStaleRuleIds,
  swapRule,
  grantExpiry,
  rulesSummary,
  rulesEmptyState,
  rulesEmptyCopy,
  flowGraphState,
  flowGraphNote,
  flowLayout,
  flowGlyph,
  flowTag,
  type FlowKind,
  ruleBody,
  defaultSrcKind,
  defaultDstKind,
  extendErrorCopy,
  resPortsValid,
  activeMembers,
  canEditRuleInModal,
  grantControls,
  managedGrantWarning,
  type LoadState,
} from "../lib/policyview";
import { ManagedBadge } from "../components/ManagedBadge";
import {
  POSTURE_HONESTY_LINE,
  buildOsVersionParam,
  checkModeOf,
  osVersionCoverage,
  osVersionMins,
  wouldFailCopy,
  type CheckMode,
} from "../lib/postureview";
// swapRule + swapPartialMessage power the create-then-delete rule edit (D-a5) in RuleFormModal.
// Every GET here goes through loadOne — a raw api.GET whose emptiness is user-meaningful is
// review-refused (S7.4a review): a fetch failure must render a legible retry, never a
// reassuring empty state. (LoadRetry — the shared legible-retry affordance — lives in components/LoadRetry.)

export default function Access() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  const emailVerified = state.status === "authed" && state.user.email_verified;
  const [meta, setMeta] = useState<Meta | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [myRole, setMyRole] = useState<Role | undefined>(undefined);
  // Page-level gating inputs, kept DISTINCT so no signal blanks another (fold-2):
  // - loadError: meta/org fetch failed (can't determine edition) → retry, nothing renderable.
  // - fatal: terminal, non-retryable (no org).
  // - roleError / roleResolved: the members fetch — its failure affects ONLY the enterprise
  //   admin path ([75]); role in-flight must render "loading", never the gate notice ([101]).
  const [loadError, setLoadError] = useState<string | null>(null);
  const [fatal, setFatal] = useState<string | null>(null);
  const [roleError, setRoleError] = useState<string | null>(null);
  // S8.5 stale-button fix (one-truth at the React tier): RulesSection and GroupsResourcesSection each hold
  // their OWN copy of the groups list (a cohesive batched load in RulesSection, so lifting just groups would
  // fracture it). subjectsRev is the parent-owned invalidation signal — GroupsResourcesSection bumps it on a
  // group/resource mutation, RulesSection re-loads on the bump — so its subject copies (and the "Add rule"
  // enabled state derived from them) can never go stale behind a group add. Invalidate the copy, not the
  // symptom (patching the disabled expression would leave the stale copy feeding the rule modal too).
  const [subjectsRev, setSubjectsRev] = useState(0);
  const [roleResolved, setRoleResolved] = useState(false);

  const reload = useCallback(async () => {
    setLoadError(null);
    setFatal(null);
    setRoleError(null);
    setRoleResolved(false);
    const mRes = await loadOne(() => api.GET("/api/v1/meta"));
    if (!mRes.ok) return setLoadError(mRes.error); // [67]: surface loadOne's (human) message
    setMeta(mRes.data as Meta);
    const oRes = await loadOne(() => api.GET("/api/v1/organizations"));
    if (!oRes.ok) return setLoadError(oRes.error);
    const first = (oRes.data as Org[])[0];
    if (!first)
      return setFatal("You are not a member of any organization yet.");
    setOrg(first);
    const memRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/members", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Member[]>;
    const resolved = roleFromMembers(memRes, myId);
    if (resolved.failed)
      return setRoleError(
        memRes.ok ? "Couldn't determine your role." : memRes.error,
      );
    setMyRole(resolved.role);
    setRoleResolved(true);
  }, [myId]);
  useEffect(() => {
    reload();
  }, [reload]);

  const gate = policyGate({
    role: myRole,
    emailVerified,
    edition: meta?.edition,
  });
  const view = accessView({
    fatal: fatal != null,
    loadError: loadError != null,
    editionReady: meta != null && org != null,
    isEnterprise: gate.isEnterprise,
    roleError: roleError != null,
    roleResolved,
    canView: gate.canView,
    role: myRole,
  });

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
      <div style={{ display: "flex", alignItems: "flex-end", gap: "14px" }}>
        <div>
          <div style={{ font: "700 22px 'Instrument Sans'", color: "#F5F5F5" }}>Access policies</div>
          <div style={{ font: "400 12px 'Instrument Sans'", color: "#6E6E6B" }}>
            {org ? org.name : "…"} · <span style={{ color: "#858582" }}>control plane</span> <span style={{ color: "#A9A9A6" }}>● healthy</span>
          </div>
        </div>
        <div style={{ flex: 1 }}></div>
      </div>

      {view === "fatal" && <ErrorText>{fatal}</ErrorText>}
      {view === "load_retry" && (
        <LoadRetry error={loadError ?? "Couldn't load."} onRetry={reload} />
      )}
      {view === "role_retry" && (
        <LoadRetry
          error={roleError ?? "Couldn't determine your role."}
          onRetry={reload}
        />
      )}
      {(view === "loading" || view === "role_loading") && (
        <p className="mt-6 text-sm text-slate-500">Loading…</p>
      )}

      {view === "upsell" && (
        <Card className="mt-6">
          <h2 className="text-sm font-semibold text-slate-300">
            Zero Trust access
          </h2>
          <p className="mt-1 text-xs text-slate-500">
            Policy rules, device approval, and default-deny enforcement are a
            Tunnex Enterprise feature.
          </p>
        </Card>
      )}

      {view === "member_gate" && (
        <Card className="mt-6">
          <p className="text-sm text-slate-400">
            Access policies are managed by owners and admins.
          </p>
        </Card>
      )}

      {view === "admin_body" && org && (
        <div style={{ display: "flex", flexDirection: "column", gap: "14px" }}>
          <ModeSection orgId={org.id} canManage={gate.canManagePolicy} />
          <RulesSection
            orgId={org.id}
            canManage={gate.canManagePolicy}
            subjectsRev={subjectsRev}
          />
          <GroupsResourcesSection
            orgId={org.id}
            canManage={gate.canManagePolicy}
            onSubjectsChanged={() => setSubjectsRev((v) => v + 1)}
          />
          <DeviceApprovalSection
            orgId={org.id}
            canManage={gate.canManageDevices}
          />
          <PostureChecksSection
            orgId={org.id}
            canManage={gate.canManageDeviceHealth}
          />
        </div>
      )}
    </div>
  );
}

// ── Zero Trust mode ─────────────────────────────────────────────────────────────────
function ModeSection({
  orgId,
  canManage,
}: {
  orgId: string;
  canManage: boolean;
}) {
  const [mode, setMode] = useState<"off" | "enforcing" | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [confirmCount, setConfirmCount] = useState(0);
  const [busy, setBusy] = useState(false);
  const [affected, setAffected] = useState<AffectedDevice[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    const r = await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/zero-trust-mode", {
        params: { path: { orgId } },
      }),
    );
    if (!r.ok) {
      setLoadError(r.error); // never hide the toggle on a failure ([5]) — show retry
      return;
    }
    setLoadError(null);
    setMode((r.data as ZeroTrustMode).mode);
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load]);

  // [1]+[7]: fetch the rule count FRESH at Enable-click — never a stale/defaulted-0 count that
  // would show the false zero-rules danger gate. A failed count fetch aborts LEGIBLY.
  async function openEnableConfirm() {
    setErr(null);
    const r = await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/policies", {
        params: { path: { orgId } },
      }),
    );
    if (!r.ok) return setErr("Couldn't verify the current rule count. retry.");
    setConfirmCount((r.data as PolicyRule[]).length);
    setConfirming(true);
  }

  async function setModeTo(next: "off" | "enforcing") {
    setBusy(true);
    setErr(null);
    setAffected(null);
    const { data, error } = await api.PUT(
      "/api/v1/organizations/{orgId}/zero-trust-mode",
      {
        params: { path: { orgId } },
        body: { mode: next },
      },
    );
    setBusy(false);
    setConfirming(false);
    if (error)
      return setErr(apiErrorMessage(error, "Could not change the mode."));
    const zt = data as ZeroTrustMode | undefined;
    if (zt) {
      setMode(zt.mode);
      if (zt.affected_full_tunnel_devices?.length)
        setAffected(zt.affected_full_tunnel_devices);
    }
  }

  const confirm = modeEnableConfirm(confirmCount);

  return (
    <Card className="mt-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-300">
            Zero Trust mode
          </h2>
          <p className="mt-1 text-xs text-slate-500">
            {mode === "enforcing"
              ? "Enforcing. default-deny; only your allow rules pass."
              : mode === "off"
                ? "Off. legacy full-mesh (all devices reach all devices)."
                : loadError
                  ? "n/a"
                  : "…"}
          </p>
        </div>
        {canManage && mode != null && !loadError && (
          <Button
            variant={mode === "enforcing" ? "ghost" : "primary"}
            disabled={busy}
            onClick={() =>
              mode === "enforcing" ? setModeTo("off") : openEnableConfirm()
            }
          >
            {mode === "enforcing" ? "Disable" : "Enable enforcing"}
          </Button>
        )}
      </div>
      {loadError && <LoadRetry error={loadError} onRetry={load} />}
      <ErrorText>{err}</ErrorText>

      {affected && (
        <div className="mt-3 rounded-md border border-warn/30 bg-warn/5 px-3 py-2 text-xs text-amber-300">
          Now enforcing. {affected.length} full-tunnel device(s) lost internet
          egress until a rule allows it:
          <span className="text-amber-200">
            {" "}
            {affected.map((d) => d.name).join(", ")}
          </span>
        </div>
      )}

      {confirming && (
        <Modal
          title={confirm.title}
          danger={confirm.danger}
          onDismiss={() => setConfirming(false)}
          actions={
            <>
              <Button variant="ghost" onClick={() => setConfirming(false)}>
                Cancel
              </Button>
              <Button
                variant={confirm.danger ? "danger" : "primary"}
                disabled={busy}
                onClick={() => setModeTo("enforcing")}
              >
                {confirm.confirmLabel}
              </Button>
            </>
          }
        >
          {confirm.body}
        </Modal>
      )}
    </Card>
  );
}

// ── Rules ─────────────────────────────────────────────────────────────────────────────
function RulesSection({
  orgId,
  canManage,
  subjectsRev,
}: {
  orgId: string;
  canManage: boolean;
  subjectsRev: number;
}) {
  const [rules, setRules] = useState<PolicyRule[]>([]);
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [members, setMembers] = useState<Member[]>([]);
  const [sites, setSites] = useState<Site[]>([]); // S8.2c D5: site rule subjects
  const [services, setServices] = useState<K8sService[]>([]); // S10.3: k8s_service dst subjects
  const [loaded, setLoaded] = useState<LoadState>({
    groupsLoaded: false,
    resourcesLoaded: false,
    membersLoaded: false,
  });
  const [loadError, setLoadError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<PolicyRule | null>(null);
  const [extendingGrant, setExtendingGrant] = useState<PolicyRule | null>(null);
  const [disablingRule, setDisablingRule] = useState<PolicyRule | null>(null); // F3: the rule pending a disable-confirm
  // SINGLE source of truth for the partial-swap warning: the SET of rule ids a create-then-
  // delete left un-deleted. The notice is DERIVED (staleNoticeText) — no separate state to
  // desync ([291]/[309]/[371]). Pruned ONLY on a successful load (amendment A), per-id (B).
  const [staleRuleIds, setStaleRuleIds] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);
  // S8.3 CP summary line: BOTH derived from real load results (never an empty default) so a failed load
  // can't render the loud "0 rules — all denied". null until the first load resolves.
  const [modeResult, setModeResult] = useState<Loaded<
    "off" | "enforcing"
  > | null>(null);
  const [rulesResult, setRulesResult] = useState<Loaded<number> | null>(null);

  const load = useCallback(async () => {
    setErr(null); // [310]: never carry a stale partial-load/mutation error into a fresh load
    const [rr, gr, resr, mr, mo, sr, ksr] = await Promise.all([
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/policies", {
          params: { path: { orgId } },
        }),
      ),
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/groups", {
          params: { path: { orgId } },
        }),
      ),
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/resources", {
          params: { path: { orgId } },
        }),
      ),
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/members", {
          params: { path: { orgId } },
        }),
      ),
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/zero-trust-mode", {
          params: { path: { orgId } },
        }),
      ),
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/sites", {
          params: { path: { orgId } },
        }),
      ), // S8.2c D5: site rule subjects
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/k8s/services", {
          params: { path: { orgId } },
        }),
      ), // S10.3: k8s_service dst subjects
    ]);
    // Summary inputs — set from the SAME results (a rules-load failure → summary shows "failed", never 0).
    setRulesResult(
      rr.ok ? { ok: true, data: (rr.data as PolicyRule[]).length } : rr,
    );
    setModeResult(
      mo.ok
        ? { ok: true, data: (mo.data as ZeroTrustMode).mode }
        : (mo as Loaded<"off" | "enforcing">),
    );
    // The RULES fetch failing means the section cannot render truthfully — show retry, NOT
    // the reassuring "No rules — enforcing denies everything" ([2]). Amendment A: on this
    // FAILED path the stale-rule set is left untouched (the warning persists).
    if (!rr.ok) return setLoadError(rr.error);
    setLoadError(null);
    const freshRules = rr.data as PolicyRule[];
    setRules(freshRules);
    setGroups((gr.ok ? (gr.data as UserGroup[]) : []) as UserGroup[]);
    setResources((resr.ok ? (resr.data as Resource[]) : []) as Resource[]);
    setMembers((mr.ok ? (mr.data as Member[]) : []) as Member[]);
    setSites((sr.ok ? (sr.data as Site[]) : []) as Site[]); // D5
    setServices((ksr.ok ? (ksr.data as K8sService[]) : []) as K8sService[]); // S10.3: k8s_service dst subjects
    // D-a6 loaded flags come from the SAME source: a set that FAILED to load → its refs are
    // "unresolved", not "deleted".
    setLoaded({
      groupsLoaded: gr.ok,
      resourcesLoaded: resr.ok,
      membersLoaded: mr.ok,
      sitesLoaded: sr.ok,
      k8sServicesLoaded: ksr.ok,
    }); // sitesLoaded → WF-8; k8sServicesLoaded → S10.3
    setErr(
      gr.ok && resr.ok && mr.ok && sr.ok && ksr.ok
        ? null
        : "Some groups/resources/members/sites/services failed to load. names may show as unresolved. Refresh.",
    ); // ksr.ok: a services-load failure must raise the banner too
    // The ONLY clear path (amendment A: gated on this successful load): drop stale ids no
    // longer present, keep the rest (B).
    setStaleRuleIds((prev) => pruneStaleRuleIds(prev, true, freshRules));
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load, subjectsRev]); // S8.5: re-load when a sibling section mutates groups/resources (stale-button fix)

  const notice = staleNoticeText(staleRuleIds); // DERIVED — no notice state

  async function del(id: string) {
    const { error } = await api.DELETE(
      "/api/v1/organizations/{orgId}/policies/{ruleId}",
      {
        params: { path: { orgId, ruleId: id } },
      },
    );
    if (error)
      return setErr(apiErrorMessage(error, "Could not delete the rule."));
    load();
  }

  // F3: toggle a rule enabled/disabled. Disabling withdraws its allow (in-hash push, effective in seconds);
  // ENABLE is one-click (additive/harmless), DISABLE goes through the confirm modal (asymmetric ceremony).
  async function setEnabled(id: string, enabled: boolean) {
    const { error } = await api.PATCH(
      "/api/v1/organizations/{orgId}/policies/{ruleId}",
      {
        params: { path: { orgId, ruleId: id } },
        body: { enabled },
      },
    );
    if (error)
      return setErr(
        apiErrorMessage(
          error,
          enabled
            ? "Could not enable the rule."
            : "Could not disable the rule.",
        ),
      );
    load();
  }

  const view = sectionRender(loadError, notice);

  return (
    <Card className="mt-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold text-slate-300">Rules</h2>
        {/* TWO seams, both producing ABSENCE, and the ORDER is the decision (S14.2 D3):
            PERMISSION first, WIDTH second. A member who may not manage rules sees nothing — never
            "read-only on this screen size", which would imply a wider window grants what their role does not. */}
        {canManage && !view.showRetry && (
          <ComposeGate surface="Access rules">
            <Button
              onClick={() => setCreating(true)}
              disabled={groups.length === 0 && sites.length === 0}
            >
              Add rule
            </Button>
          </ComposeGate>
        )}
      </div>
      <p className="mt-1 text-xs text-slate-500">
        Allow rules: a source group may reach a destination group or resource.
      </p>

      {/* S8.3 CP: the posture summary line. enforcing+0 is LOUD (a live default-deny with no rules); a
          failed load reads "unavailable", never the reassuring 0-rules message. */}
      {(() => {
        const s = rulesSummary({ modeResult, rulesResult });
        if (s.state === "loading") return null;
        return (
          <p
            className={
              s.loud
                ? "mt-2 rounded-md border border-danger/40 bg-danger/10 px-3 py-1.5 text-sm font-semibold text-danger"
                : `mt-2 text-xs ${s.state === "failed" ? "text-amber-300" : "text-slate-400"}`
            }
          >
            {s.text}
          </p>
        );
      })()}

      {/* [291] legibility signals COMPOSE: the partial-swap notice + a mutation error render at
          TOP LEVEL — a load failure replaces the LIST (content), never a warning. */}
      {view.showNotice && (
        <p className="mt-2 text-xs text-amber-300">{notice}</p>
      )}
      <ErrorText>{err}</ErrorText>
      {view.showRetry && (
        <LoadRetry error={loadError ?? "Couldn't load rules."} onRetry={load} />
      )}

      {view.showContent && (
        <>
          {groups.length === 0 && sites.length === 0 && loaded.groupsLoaded && (
            <p className="mt-2 text-xs text-slate-500">
              Create a group of users or register a site (site-to-site source)
              to add a rule.
            </p>
          )}
          {/* ── ACCESS FLOW ({{ polFlow }}) — built from the handoff's buildPolicyFlow(), not from a screenshot.
              GEOMETRY VERBATIM: canvas 600x312 rx14 over a 16px dot field; node boxes 152x36 rx10 at cx±76,
              columns at LX=95 / RX=505 so the paths own the middle 260px; vertical pitch 68 from cy=54;
              glyph circle r8 at cx-60. EDGES are cubic beziers with HORIZONTAL control points ±130 —
              `M170,sy C300,sy 300,dy 430,dy` — so they leave and arrive flat and read as flows, not chords.
              Temporary grants are DASHED (5 6), allow is solid: the design distinguishes them by dash, not
              by colour. Legend bottom-left, readout bottom-right, both INSIDE the panel.

              ⛔ ORDERING IS THE FIX, NOT THE CURVE. The handoff's own data crosses ZERO times because a human
              ordered the destination column so each source's edge is level or one slot away. Ours was
              insertion order and crossed six times out of nine. Destinations are now placed by the MEAN slot
              of their sources (one barycentric pass), which reproduces the handoff's hand-chosen order on its
              own data. A prettier line over the same tangle would have been worse — it would look deliberate.

              ⛔ AND THE COLUMN IS CAPPED AT THE DESIGN'S OWN FOUR SLOTS. The handoff draws 5 edges over 8
              nodes while its rule table shows 9 rows — it renders a SUBSET, by hand. Above the cap the
              remainder is stated, never silently dropped, and the table below stays authoritative. */}
          {(() => {
            const rows = rules.map((r) => {
              const rr = ruleRow(r, groups, resources, members, sites, loaded, services);
              return {
                id: r.id,
                src: rr.src.label,
                dst: rr.dst.label,
                temp: r.expires_at != null,
                // ⛔ FROM THE RULE'S OWN UNION, never inferred from the label.
                srcKind: r.src_kind as FlowKind,
                dstKind: r.dst_kind as FlowKind,
              };
            });
            const g = flowGraphState(rows.length);
            if (g.kind !== "draw")
              return (
                <p className="mt-3 text-xs text-slate-500" data-testid="flow-withheld">
                  {flowGraphNote(g)}
                </p>
              );
            const { srcs, dsts, shown, hidden } = flowLayout(rows);
            const si = (l: string) => srcs.findIndex((n) => n.label === l);
            const di = (l: string) => dsts.findIndex((n) => n.label === l);
            const cy = (i: number) => 54 + i * 68;
            const node = (n: { label: string; kind: FlowKind }, i: number, isSrc: boolean) => {
              const cx = isSrc ? 95 : 505;
              return (
                <g key={(isSrc ? "s" : "d") + n.label}>
                  <rect x={cx - 76} y={cy(i) - 18} width="152" height="36" rx="10"
                        fill="var(--tnx-surface-inset)" stroke="var(--tnx-divider)" strokeWidth="1.4" />
                  <circle cx={cx - 60} cy={cy(i)} r="8"
                          fill="var(--tnx-surface)" stroke="var(--tnx-divider)" />
                  <text x={cx - 60} y={cy(i) + 3} textAnchor="middle"
                        style={{ fontSize: "8px" }} className="fill-slate-400">
                    {flowGlyph(n.kind)}
                  </text>
                  <text x={cx - 46} y={cy(i) - 2} style={{ fontSize: "10px" }} className="fill-slate-200">
                    {n.label.length > 18 ? n.label.slice(0, 17) + "\u2026" : n.label}
                  </text>
                  <text x={cx - 46} y={cy(i) + 9} style={{ fontSize: "7px", letterSpacing: ".08em" }}
                        className="fill-slate-500">
                    {flowTag(n.kind)}
                  </text>
                </g>
              );
            };
            return (
              <div className="mt-3">
                {/* ⛔ FIXED 600x312 — ONE USER UNIT IS ONE PIXEL. `w-full` on a viewBox scaled the whole
                    drawing to the container (~1900px), magnifying 152x36 boxes to ~490x130 and truncating
                    every name. THE SCALE IS A CONTRACT: same law, same panel shape, second occurrence after
                    the Sites map. width/height are set explicitly and the SVG is centred, never stretched. */}
                <svg
                  width="600"
                  height="312"
                  viewBox="0 0 600 312"
                  className="mx-auto block max-w-full"
                  role="img"
                  aria-label={`Access flow: ${shown.length} of ${rows.length} rules drawn, ${srcs.length} sources to ${dsts.length} destinations`}
                >
                  <defs>
                    <pattern id="tnxPolDots" width="16" height="16" patternUnits="userSpaceOnUse">
                      <circle cx="1.5" cy="1.5" r="1" fill="var(--tnx-divider)" />
                    </pattern>
                  </defs>
                  <rect x="0" y="0" width="600" height="312" rx="14" fill="url(#tnxPolDots)" />
                  {shown.map((r) => {
                    const sy = cy(si(r.src)), dy = cy(di(r.dst));
                    return (
                      <path key={r.id} fill="none" strokeWidth="2" className="tnx-flow-edge"
                            stroke={r.temp ? "var(--tnx-neutral)" : "var(--tnx-accent)"}
                            strokeDasharray={r.temp ? "5 6" : undefined}
                            d={`M170,${sy} C300,${sy} 300,${dy} 430,${dy}`} />
                    );
                  })}
                  {srcs.map((n, i) => node(n, i, true))}
                  {dsts.map((n, i) => node(n, i, false))}
                </svg>
                <div className="mx-auto mt-1 flex max-w-[600px] items-center justify-between text-[10px] text-slate-500">
                  <span>
                    <span className="text-slate-300">&#8212;&#8212;</span> allow&nbsp;&nbsp;
                    <span className="text-slate-300">- - -</span> temporary
                  </span>
                  <span>
                    {hidden > 0
                      ? `${shown.length} of ${rows.length} flows drawn. ${hidden} more in the table below.`
                      : "All access flows"}
                  </span>
                </div>
              </div>
            );
          })()}
          <ul className="mt-3 space-y-1">
            {rules.map((r) => {
              const row = ruleRow(
                r,
                groups,
                resources,
                members,
                sites,
                loaded,
                services,
              );
              const exp = grantExpiry(r, Date.now());
              return (
                <li
                  key={r.id}
                  className={`flex items-center justify-between rounded-md bg-white/5 px-3 py-2 text-sm ${r.enabled ? "" : "opacity-50"}`}
                >
                  <span className="text-slate-200">
                    <RefText
                      label={row.src.label}
                      broken={row.src.state !== "ok"}
                    />{" "}
                    <span className="text-slate-500">→</span>{" "}
                    <RefText
                      label={row.dst.label}
                      broken={row.dst.state !== "ok"}
                    />
                    {/* S10.2 D2 cond 1: a GitOps-managed grant is badged; its mutation controls are withheld below. */}
                    {row.managedByOperator && <ManagedBadge />}
                    {/* F3: a disabled rule is shown DISTINCTLY, never hidden — the list must not lie about what's enforcing. */}
                    {!r.enabled && (
                      <span className="ml-2 rounded-full border border-slate-700 bg-slate-800/80 px-2 py-0.5 font-mono text-[10px] font-semibold text-slate-400">
                        disabled
                      </span>
                    )}
                    {/* S8.7 warn-not-refuse (D1): the SERVER's read-time judgment, rendered verbatim — a CIDR
                        rule matching no current org range (a reassuring-rule). Self-clears when a range lands. */}
                    {row.cidrOutsideRanges && (
                      <span
                        className="ml-2 rounded-full border border-amber-800/50 bg-amber-950/40 px-2 py-0.5 font-mono text-[10px] font-semibold text-amber-400"
                        title="This CIDR is inside no current site subnet. the rule matches nothing until the range is declared."
                      >
                        OUTSIDE RANGES
                      </span>
                    )}
                    {/* S10.3 warn-not-refuse: the SERVER's read-time judgment — the dst Service was unexposed
                        or its cluster deregistered, so the grant compiles to nothing. Self-clears if it returns. */}
                    {row.k8sServiceVanished && (
                      <span
                        className="ml-2 rounded-full border border-rose-800/50 bg-rose-950/40 px-2 py-0.5 font-mono text-[10px] font-semibold text-rose-400"
                        title="The Kubernetes Service this rule reaches is no longer exposed. the grant matches nothing until it is re-exposed."
                      >
                        VANISHED
                      </span>
                    )}
                    {/* S7.5.4 linger model: a temporary grant shows its window; an EXPIRED grant
                        stays visible (audit-history), rendered distinctly — never hidden. */}
                    {exp.state !== "permanent" && (
                      <span
                        className={`ml-2 rounded-full border px-2 py-0.5 font-mono text-[10px] font-semibold ${exp.state === "expired" ? "border-rose-800/50 bg-rose-950/40 text-rose-400" : "border-amber-800/50 bg-amber-950/40 text-amber-300"}`}
                      >
                        TEMP · {exp.label}
                      </span>
                    )}
                  </span>
                  {canManage &&
                    (grantControls(row).withheld ? (
                      // D2 cond 1: withhold EVERY dashboard mutation (extend/edit/disable/delete) on a
                      // GitOps-managed grant — warn at the point of editing, never silently revert on reconcile.
                      <span
                        className="text-xs text-amber-400/90"
                        title={managedGrantWarning()}
                        aria-label={managedGrantWarning()}
                      >
                        edit the CR
                      </span>
                    ) : (
                      <span className="flex gap-2">
                        {exp.extendable && (
                          <Button
                            variant="ghost"
                            onClick={() => setExtendingGrant(r)}
                          >
                            Extend
                          </Button>
                        )}
                        {canEditRuleInModal(r) && (
                          <Button variant="ghost" onClick={() => setEditing(r)}>
                            Edit
                          </Button>
                        )}
                        {/* F3: enable = one click (additive); disable = confirm (revokes live access instantly). */}
                        {r.enabled ? (
                          <Button
                            variant="ghost"
                            onClick={() => setDisablingRule(r)}
                          >
                            Disable
                          </Button>
                        ) : (
                          <Button
                            variant="ghost"
                            onClick={() => setEnabled(r.id, true)}
                          >
                            Enable
                          </Button>
                        )}
                        <Button variant="danger" onClick={() => del(r.id)}>
                          Delete
                        </Button>
                      </span>
                    ))}
                </li>
              );
            })}
            {(() => {
              const es = rulesEmptyState({ rulesResult, modeResult, renderedCount: rules.length });
              if (es.kind === "rows") return null;
              const c = rulesEmptyCopy(es);
              return (
              <li className={es.kind === "enforcing_empty" ? "text-xs font-semibold text-warn" : "text-xs text-slate-500"}>
                {c.text}
              </li>
              );
            })()}
          </ul>
        </>
      )}

      {(creating || editing) && (
        <RuleFormModal
          orgId={orgId}
          groups={groups}
          resources={resources}
          members={activeMembers(members)}
          sites={sites}
          services={services}
          editing={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
          onDone={(staleId) => {
            // A partial swap adds the un-deleted rule id to the set; a clean create adds
            // nothing (so it can never drop a live warning — [371]).
            if (staleId)
              setStaleRuleIds((prev) =>
                prev.includes(staleId) ? prev : [...prev, staleId],
              );
            setCreating(false);
            setEditing(null);
            load();
          }}
        />
      )}
      {extendingGrant && (
        <ExtendGrantModal
          orgId={orgId}
          rule={extendingGrant}
          onClose={() => setExtendingGrant(null)}
          onDone={() => {
            setExtendingGrant(null);
            load();
          }}
        />
      )}
      {/* F3: the disable-confirm — NAMES the rule's own subject→destination + the immediate effect. Only
          disable gets this (enable is one-click). Danger-styled; Cancel or backdrop dismisses. */}
      {disablingRule &&
        (() => {
          const row = ruleRow(
            disablingRule,
            groups,
            resources,
            members,
            sites,
            loaded,
          );
          const r = disablingRule;
          return (
            <Modal
              title="Disable rule?"
              danger
              onDismiss={() => setDisablingRule(null)}
              actions={
                <>
                  <Button
                    variant="ghost"
                    onClick={() => setDisablingRule(null)}
                  >
                    Cancel
                  </Button>
                  <Button
                    variant="danger"
                    onClick={async () => {
                      setDisablingRule(null);
                      await setEnabled(r.id, false);
                    }}
                  >
                    Disable
                  </Button>
                </>
              }
            >
              <p className="text-sm text-slate-300">
                {disableConfirmText(row.src.label, row.dst.label)}
              </p>
            </Modal>
          );
        })()}
    </Card>
  );
}

// ExtendGrantModal moves a temporary grant's window forward (S7.5.4). A LAPSED grant is
// refused by the server (409 grant_lapsed) — surfaced legibly here, not as a raw error;
// this is a WINDOW BUMP (PUT expires_at), never a delete+recreate.
function ExtendGrantModal({
  orgId,
  rule,
  onClose,
  onDone,
}: {
  orgId: string;
  rule: PolicyRule;
  onClose: () => void;
  onDone: () => void;
}) {
  const now = grantExpiry(rule, Date.now());
  const [when, setWhen] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setErr(null);
    const iso = new Date(when).toISOString();
    const { error } = await api.PUT(
      "/api/v1/organizations/{orgId}/policies/{ruleId}",
      {
        params: { path: { orgId, ruleId: rule.id } },
        body: { expires_at: iso },
      },
    );
    setBusy(false);
    if (error) return setErr(extendErrorCopy(apiErrorCode(error))); // 409 grant_lapsed / not_temporary → legible copy
    onDone();
  }

  return (
    <Modal
      title="Extend grant"
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button disabled={busy || !when} onClick={submit}>
            Extend
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <p className="text-xs text-slate-400">
          {now.state === "expired"
            ? `This grant ${now.label}. Extending a lapsed grant is refused. create a new grant instead.`
            : `This grant ${now.label}. Move its expiry to a later time (the grant is not re-created. only its window moves).`}
        </p>
        <Field label="New expiry">
          <Input
            type="datetime-local"
            value={when}
            onChange={(e) => setWhen(e.target.value)}
          />
        </Field>
        <ErrorText>{err}</ErrorText>
      </div>
    </Modal>
  );
}

function RefText({ label, broken }: { label: string; broken: boolean }) {
  return broken ? (
    <span className="text-amber-400">⚠ {label}</span>
  ) : (
    <span>{label}</span>
  );
}

// RuleFormModal creates OR edits a rule. Editing = CREATE-THEN-DELETE (D-a5) via swapRule —
// gap-free (allow-only union), never delete-first, with a LEGIBLE partial on delete-fail.
function RuleFormModal({
  orgId,
  groups,
  resources,
  members,
  sites,
  services,
  editing,
  onClose,
  onDone,
}: {
  orgId: string;
  groups: UserGroup[];
  resources: Resource[];
  members: Member[];
  sites: Site[];
  services: K8sService[];
  editing: PolicyRule | null;
  onClose: () => void;
  onDone: (staleRuleId?: string) => void;
}) {
  // S8.2c D5: the modal now CREATES site-source + site-dest rules too (was API-only). src_kind ∈
  // {group,user,site}; dst_kind ∈ {group,resource,site} — all through the same policies API (validation +
  // audit intact; the demo's raw DB insert was the anti-pattern this closes).
  // Review #4: when the org has sites but no groups, defaulting to "group" opens a modal that can't submit
  // (empty group select) until BOTH dropdowns are flipped — a dead end. Default to the kind that's actually
  // available so a fresh site-to-site org can Create immediately.
  const hasGroups = groups.length > 0;
  const [srcKind, setSrcKind] = useState<"group" | "user" | "site" | "cidr">(
    defaultSrcKind({
      editingKind:
        editing?.src_kind === "user"
          ? "user"
          : editing?.src_kind === "site"
            ? "site"
            : editing?.src_kind === "cidr"
              ? "cidr"
              : undefined,
      hasGroups,
      hasSites: sites.length > 0,
    }),
  );
  const [src, setSrc] = useState(editing?.src_group_id ?? groups[0]?.id ?? "");
  const [srcUser, setSrcUser] = useState(
    editing?.src_user_id ?? members[0]?.user_id ?? "",
  );
  const [srcSite, setSrcSite] = useState(
    editing?.src_site_id ?? sites[0]?.id ?? "",
  );
  const [srcCidr, setSrcCidr] = useState(editing?.src_cidr ?? ""); // S8.7: literal source CIDR (free-text)
  // Default to the first dst kind that HAS options (re-review #4: the src-side fix left the dst side able to
  // dead-end — a no-groups org with resources/sites opened on "group" with an empty select, un-submittable).
  const [dstKind, setDstKind] = useState<
    "group" | "resource" | "site" | "k8s_service"
  >(
    editing?.dst_kind === "k8s_service"
      ? "k8s_service"
      : defaultDstKind({
          editingKind:
            editing?.dst_kind === "resource"
              ? "resource"
              : editing?.dst_kind === "site"
                ? "site"
                : undefined,
          hasGroups,
          hasResources: resources.length > 0,
          hasSites: sites.length > 0,
        }),
  );
  const [dstGroup, setDstGroup] = useState(
    editing?.dst_group_id ?? groups[0]?.id ?? "",
  );
  const [dstResource, setDstResource] = useState(
    editing?.dst_resource_id ?? resources[0]?.id ?? "",
  );
  const [dstSite, setDstSite] = useState(
    editing?.dst_site_id ?? sites[0]?.id ?? "",
  );
  const [dstK8sService, setDstK8sService] = useState(
    editing?.dst_k8s_service_id ?? services[0]?.id ?? "",
  ); // S10.3
  // Temporary grant: an optional expiry (datetime-local). Empty = permanent.
  // Expiry is a CREATE-only field ([2]/[3] fix): editing a rule is create-then-delete, and a
  // same-(src,dst) edit carrying an expiry collides on the unique index (or resubmits a past
  // expiry). Changing a temporary grant's window goes through Extend (a window bump), not Edit.
  const [expiresAt, setExpiresAt] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  function bodyFor(): CreatePolicyRuleRequest {
    return ruleBody({
      srcKind,
      dstKind,
      src,
      srcUser,
      srcSite,
      srcCidr,
      dstGroup,
      dstResource,
      dstSite,
      dstK8sService,
      expiresAt,
      editing: !!editing,
    });
  }

  async function submit() {
    setBusy(true);
    setErr(null);
    // [8]: guard a 2xx-with-no-body — never let (data).id throw and strand busy=true.
    const create = async (): Promise<{ id: string } | { error: unknown }> => {
      const { data, error } = await api.POST(
        "/api/v1/organizations/{orgId}/policies",
        {
          params: { path: { orgId } },
          body: bodyFor(),
        },
      );
      if (error) return { error };
      const id = (data as PolicyRule | undefined)?.id;
      if (!id)
        return { error: { error: { message: "Server returned no rule id." } } };
      return { id };
    };

    if (!editing) {
      const created = await create();
      setBusy(false);
      if ("error" in created)
        return setErr(
          apiErrorMessage(created.error, "Could not create the rule."),
        );
      return onDone();
    }

    const out = await swapRule(editing.id, create, async (id) =>
      api.DELETE("/api/v1/organizations/{orgId}/policies/{ruleId}", {
        params: { path: { orgId, ruleId: id } },
      }),
    );
    setBusy(false);
    if (out.outcome === "create_failed")
      return setErr(
        apiErrorMessage(out.error, "Could not create the new rule."),
      );
    if (out.outcome === "partial") return onDone(out.oldId); // notice derived from the id (staleNoticeText)
    onDone();
  }

  return (
    <Modal
      title={editing ? "Edit rule" : "Add rule"}
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={
              busy ||
              (srcKind === "group"
                ? !src
                : srcKind === "user"
                  ? !srcUser
                  : srcKind === "cidr"
                    ? !srcCidr.trim()
                    : !srcSite) ||
              (dstKind === "group"
                ? !dstGroup
                : dstKind === "resource"
                  ? !dstResource
                  : dstKind === "k8s_service"
                    ? !dstK8sService
                    : !dstSite)
            }
            onClick={submit}
          >
            {editing ? "Save" : "Create"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {/* S8.3 CP layout: source + destination each read as a labeled panel (was a flat field list),
            so the "who → what" of a rule is legible at a glance. Layout only — no behavior change. */}
        <fieldset className="space-y-3 rounded-md border border-white/10 p-3">
          <legend className="px-1 text-[11px] uppercase tracking-wide text-slate-500">
            Source
          </legend>
          <Field label="Source type">
            <Select
              value={srcKind}
              onChange={(e) =>
                setSrcKind(e.target.value as "group" | "user" | "site" | "cidr")
              }
            >
              <option value="group">Group</option>
              <option value="user">User (a single person)</option>
              {sites.length > 0 && (
                <option value="site">Site (a LAN behind a gateway)</option>
              )}
              <option value="cidr">CIDR (a specific host or subnet)</option>
            </Select>
          </Field>
          {srcKind === "cidr" && (
            <Field label="Source CIDR">
              <Input
                value={srcCidr}
                onChange={(e) => setSrcCidr(e.target.value)}
                placeholder="172.31.17.64/32"
              />
            </Field>
          )}
          {srcKind === "group" ? (
            <Field label="Source group">
              <Select value={src} onChange={(e) => setSrc(e.target.value)}>
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </Select>
            </Field>
          ) : srcKind === "user" ? (
            // D1 constraint mirrored client-side: only CURRENT active members are offered,
            // so the picker never lets you build a rule the server would reject (user_not_member).
            <Field label="Source user">
              {members.length > 0 ? (
                <Select
                  value={srcUser}
                  onChange={(e) => setSrcUser(e.target.value)}
                >
                  {members.map((m) => (
                    <option key={m.user_id} value={m.user_id}>
                      {m.name || m.email}
                    </option>
                  ))}
                </Select>
              ) : (
                <Input
                  value=""
                  disabled
                  placeholder="No active members to grant"
                />
              )}
            </Field>
          ) : (
            <Field label="Source site">
              <Select
                value={srcSite}
                onChange={(e) => setSrcSite(e.target.value)}
              >
                {sites.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
        </fieldset>
        <fieldset className="space-y-3 rounded-md border border-white/10 p-3">
          <legend className="px-1 text-[11px] uppercase tracking-wide text-slate-500">
            Destination
          </legend>
          <Field label="Destination type">
            <Select
              value={dstKind}
              onChange={(e) =>
                setDstKind(
                  e.target.value as
                    "group" | "resource" | "site" | "k8s_service",
                )
              }
            >
              <option value="group">Group (device-to-device)</option>
              <option value="resource">Resource (CIDR / port)</option>
              {sites.length > 0 && (
                <option value="site">Site (a LAN behind a gateway)</option>
              )}
              {services.length > 0 && (
                <option value="k8s_service">
                  Kubernetes Service (in-cluster)
                </option>
              )}
            </Select>
          </Field>
          {dstKind === "group" ? (
            <Field label="Destination group">
              <Select
                value={dstGroup}
                onChange={(e) => setDstGroup(e.target.value)}
              >
                {groups.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.name}
                  </option>
                ))}
              </Select>
            </Field>
          ) : dstKind === "resource" ? (
            <Field label="Destination resource">
              <Select
                value={dstResource}
                onChange={(e) => setDstResource(e.target.value)}
              >
                {resources.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </Select>
            </Field>
          ) : dstKind === "k8s_service" ? (
            <Field label="Destination Kubernetes Service">
              <Select
                value={dstK8sService}
                onChange={(e) => setDstK8sService(e.target.value)}
              >
                {services.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.fqdn}
                  </option>
                ))}
              </Select>
            </Field>
          ) : (
            <Field label="Destination site">
              <Select
                value={dstSite}
                onChange={(e) => setDstSite(e.target.value)}
              >
                {sites.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.name}
                  </option>
                ))}
              </Select>
            </Field>
          )}
        </fieldset>
        {/* Temporary grant (CREATE only): set an expiry to auto-revoke; empty = permanent.
            Editing an existing rule changes its src/dst; change a temporary grant's window
            with Extend (a window bump), not Edit. */}
        {!editing && (
          <Field label="Expires (optional. leave empty for a permanent grant)">
            <Input
              type="datetime-local"
              value={expiresAt}
              onChange={(e) => setExpiresAt(e.target.value)}
            />
          </Field>
        )}
        <ErrorText>{err}</ErrorText>
      </div>
    </Modal>
  );
}

// ── Groups & Resources ──────────────────────────────────────────────────────────────
function GroupsResourcesSection({
  orgId,
  canManage,
  onSubjectsChanged,
}: {
  orgId: string;
  canManage: boolean;
  onSubjectsChanged: () => void;
}) {
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [groupsError, setGroupsError] = useState<string | null>(null);
  const [resourcesError, setResourcesError] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [newGroup, setNewGroup] = useState("");
  // Feature 1 (port-scoped resources): the resource MODEL + compiler + API already carry protocol + ports
  // end-to-end (resources.port_low/high, compiler.go:417-419); only this form omitted the port inputs — so a
  // rule targeting a resource could only ever grant ALL ports. Ports are OPTIONAL (empty = all ports for the
  // protocol); the server (createResource) is the authoritative validator (both-or-neither, low<=high).
  const [newRes, setNewRes] = useState({
    name: "",
    cidr: "",
    protocol: "any" as "any" | "tcp" | "udp",
    portLow: "",
    portHigh: "",
  });

  const load = useCallback(async () => {
    const [gr, resr] = await Promise.all([
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/groups", {
          params: { path: { orgId } },
        }),
      ),
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/resources", {
          params: { path: { orgId } },
        }),
      ),
    ]);
    // Per-column legibility: a failed groups load shows retry in the groups column, not
    // "No groups yet." ([4]); same for resources.
    setGroupsError(gr.ok ? null : gr.error);
    setResourcesError(resr.ok ? null : resr.error);
    if (gr.ok) setGroups(gr.data as UserGroup[]);
    if (resr.ok) setResources(resr.data as Resource[]);
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load]);

  async function addGroup() {
    if (!newGroup.trim()) return;
    const { error } = await api.POST("/api/v1/organizations/{orgId}/groups", {
      params: { path: { orgId } },
      body: { name: newGroup.trim() },
    });
    if (error)
      return setErr(apiErrorMessage(error, "Could not create the group."));
    setNewGroup("");
    load();
    onSubjectsChanged(); // S8.5: re-sync RulesSection's subject copy (the stale "Add rule" button)
  }
  async function delGroup(id: string) {
    const { error } = await api.DELETE(
      "/api/v1/organizations/{orgId}/groups/{groupId}",
      {
        params: { path: { orgId, groupId: id } },
      },
    );
    if (error)
      return setErr(apiErrorMessage(error, "Could not delete the group."));
    load();
    onSubjectsChanged();
  }
  async function addResource() {
    if (
      !newRes.name.trim() ||
      !newRes.cidr.trim() ||
      !resPortsValid(newRes.portLow, newRes.portHigh)
    )
      return;
    // Both-or-neither: a low with no high is a SINGLE port (high := low); both empty = all ports (omit).
    const loStr = newRes.portLow.trim();
    const hiStr = newRes.portHigh.trim();
    let port_low: number | undefined;
    let port_high: number | undefined;
    if (loStr !== "") {
      port_low = Number(loStr);
      port_high = hiStr === "" ? port_low : Number(hiStr);
    }
    const { error } = await api.POST(
      "/api/v1/organizations/{orgId}/resources",
      {
        params: { path: { orgId } },
        body: {
          name: newRes.name.trim(),
          cidr: newRes.cidr.trim(),
          protocol: newRes.protocol,
          port_low,
          port_high,
        },
      },
    );
    if (error)
      return setErr(apiErrorMessage(error, "Could not create the resource."));
    setNewRes({
      name: "",
      cidr: "",
      protocol: "any",
      portLow: "",
      portHigh: "",
    });
    load();
    onSubjectsChanged(); // resources are rule destinations — keep RulesSection's copy fresh too
  }
  async function delResource(id: string) {
    const { error } = await api.DELETE(
      "/api/v1/organizations/{orgId}/resources/{resourceId}",
      {
        params: { path: { orgId, resourceId: id } },
      },
    );
    if (error)
      return setErr(apiErrorMessage(error, "Could not delete the resource."));
    load();
    onSubjectsChanged();
  }

  return (
    <Card className="mt-4">
      <h2 className="text-sm font-semibold text-slate-300">
        Groups &amp; resources
      </h2>
      <ErrorText>{err}</ErrorText>
      <div className="mt-3 grid gap-4 sm:grid-cols-2">
        <div>
          {/* WF-OVPN-walk-2: "Groups of users" makes membership honest — a group holds USERS, not
              devices (a device inherits access from its owning user via a group/user rule). The old
              "Groups (… device-to-device targets)" label read as "add devices here", which has no path. */}
          <p className="text-xs font-medium text-slate-400">
            Groups of users (rule sources / device-to-device targets)
          </p>
          {groupsError ? (
            <LoadRetry error={groupsError} onRetry={load} />
          ) : (
            <>
              <ul className="mt-2 space-y-1">
                {groups.map((g) => (
                  <li
                    key={g.id}
                    className="flex items-center justify-between rounded-md bg-white/5 px-3 py-1.5 text-sm text-slate-200"
                  >
                    {g.name}
                    {canManage && (
                      <Button variant="danger" onClick={() => delGroup(g.id)}>
                        Delete
                      </Button>
                    )}
                  </li>
                ))}
                {groups.length === 0 && (
                  <li className="text-xs text-slate-500">No groups yet.</li>
                )}
              </ul>
              {canManage && (
                <div className="mt-2 flex gap-2">
                  <Input
                    placeholder="Group name"
                    value={newGroup}
                    onChange={(e) => setNewGroup(e.target.value)}
                  />
                  <Button onClick={addGroup}>Add</Button>
                </div>
              )}
            </>
          )}
        </div>
        <div>
          <p className="text-xs font-medium text-slate-400">
            Resources (CIDR : protocol : ports)
          </p>
          {resourcesError ? (
            <LoadRetry error={resourcesError} onRetry={load} />
          ) : (
            <>
              <ul className="mt-2 space-y-1">
                {resources.map((r) => (
                  <li
                    key={r.id}
                    className="flex items-center justify-between rounded-md bg-white/5 px-3 py-1.5 text-sm text-slate-200"
                  >
                    <span>
                      {r.name}{" "}
                      <span className="text-slate-500">
                        {r.cidr} · {r.protocol}/
                        {portLabel(r.port_low, r.port_high)}
                      </span>
                    </span>
                    {canManage && (
                      <Button
                        variant="danger"
                        onClick={() => delResource(r.id)}
                      >
                        Delete
                      </Button>
                    )}
                  </li>
                ))}
                {resources.length === 0 && (
                  <li className="text-xs text-slate-500">No resources yet.</li>
                )}
              </ul>
              {canManage && (
                <div className="mt-2 space-y-2">
                  <Input
                    placeholder="Name"
                    value={newRes.name}
                    onChange={(e) =>
                      setNewRes({ ...newRes, name: e.target.value })
                    }
                  />
                  <div className="flex gap-2">
                    <Input
                      placeholder="CIDR e.g. 10.0.5.0/24"
                      value={newRes.cidr}
                      onChange={(e) =>
                        setNewRes({ ...newRes, cidr: e.target.value })
                      }
                    />
                    <Select
                      value={newRes.protocol}
                      onChange={(e) =>
                        setNewRes({
                          ...newRes,
                          protocol: e.target.value as "any" | "tcp" | "udp",
                        })
                      }
                    >
                      <option value="any">any</option>
                      <option value="tcp">tcp</option>
                      <option value="udp">udp</option>
                    </Select>
                  </div>
                  {/* Feature 1: OPTIONAL port scope. Leave blank = all ports for the protocol; a low alone =
                      a single port; low+high = a range. Server is authoritative (createResource validates). */}
                  <div className="flex gap-2">
                    <Input
                      type="number"
                      min={1}
                      max={65535}
                      placeholder="Port (optional)"
                      value={newRes.portLow}
                      onChange={(e) =>
                        setNewRes({ ...newRes, portLow: e.target.value })
                      }
                    />
                    <Input
                      type="number"
                      min={1}
                      max={65535}
                      placeholder="to (range, optional)"
                      value={newRes.portHigh}
                      onChange={(e) =>
                        setNewRes({ ...newRes, portHigh: e.target.value })
                      }
                    />
                    <Button
                      onClick={addResource}
                      disabled={!resPortsValid(newRes.portLow, newRes.portHigh)}
                    >
                      Add
                    </Button>
                  </div>
                  {!resPortsValid(newRes.portLow, newRes.portHigh) && (
                    <p className="text-xs text-amber-400">
                      Ports must be 1–65535; leave both blank for all ports, or
                      set a low ≤ high.
                    </p>
                  )}
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </Card>
  );
}

// ── Device approval (folded S7.3 admin surface) ─────────────────────────────────────
function DeviceApprovalSection({
  orgId,
  canManage,
}: {
  orgId: string;
  canManage: boolean;
}) {
  const [mode, setMode] = useState<"off" | "on" | null>(null);
  const [modeError, setModeError] = useState<string | null>(null);
  const [pending, setPending] = useState<Device[]>([]);
  const [pendingError, setPendingError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [dr, pr] = await Promise.all([
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/device-approval", {
          params: { path: { orgId } },
        }),
      ),
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/devices/pending", {
          params: { path: { orgId } },
        }),
      ),
    ]);
    setModeError(dr.ok ? null : dr.error);
    if (dr.ok) setMode((dr.data as DeviceApproval).mode);
    // [3]: a failed pending fetch must NOT render "No devices awaiting approval" — that hides
    // a device blocked from connecting. Show retry.
    setPendingError(pr.ok ? null : pr.error);
    if (pr.ok) setPending(pr.data as Device[]);
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load]);

  async function setApproval(next: "off" | "on") {
    setBusy(true);
    setErr(null);
    const { error } = await api.PUT(
      "/api/v1/organizations/{orgId}/device-approval",
      {
        params: { path: { orgId } },
        body: { mode: next },
      },
    );
    setBusy(false);
    if (error)
      return setErr(
        apiErrorMessage(error, "Could not change device approval."),
      );
    load();
  }
  async function decide(deviceId: string, action: "approve" | "reject") {
    const path =
      action === "approve"
        ? "/api/v1/organizations/{orgId}/devices/{deviceId}/approve"
        : "/api/v1/organizations/{orgId}/devices/{deviceId}/reject";
    const { error } = await api.POST(path, {
      params: { path: { orgId, deviceId } },
    });
    if (error)
      return setErr(apiErrorMessage(error, `Could not ${action} the device.`));
    load();
  }

  return (
    <Card className="mt-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-300">
            Device approval
          </h2>
          <p className="mt-1 text-xs text-slate-500">
            {mode === "on"
              ? "On. new devices enroll pending and cannot connect until approved."
              : mode === "off"
                ? "Off. new devices are active on enrollment."
                : modeError
                  ? "n/a"
                  : "…"}
          </p>
        </div>
        {canManage && mode != null && !modeError && (
          <Button
            variant={mode === "on" ? "ghost" : "primary"}
            disabled={busy}
            onClick={() => setApproval(mode === "on" ? "off" : "on")}
          >
            {mode === "on" ? "Turn off" : "Require approval"}
          </Button>
        )}
      </div>
      {modeError && <LoadRetry error={modeError} onRetry={load} />}
      <ErrorText>{err}</ErrorText>

      <p className="mt-3 text-xs font-medium text-slate-400">Pending devices</p>
      {pendingError ? (
        <LoadRetry error={pendingError} onRetry={load} />
      ) : (
        <ul className="mt-2 space-y-1">
          {pending.map((d) => (
            <li
              key={d.id}
              className="flex items-center justify-between rounded-md bg-white/5 px-3 py-2 text-sm text-slate-200"
            >
              <span>
                {d.name} <span className="text-slate-500">{d.assigned_ip}</span>
              </span>
              {canManage && (
                <span className="flex gap-2">
                  <Button onClick={() => decide(d.id, "approve")}>
                    Approve
                  </Button>
                  <Button
                    variant="danger"
                    onClick={() => decide(d.id, "reject")}
                  >
                    Reject
                  </Button>
                </span>
              )}
            </li>
          ))}
          {pending.length === 0 && (
            <li className="text-xs text-slate-500">
              No devices awaiting approval.
            </li>
          )}
        </ul>
      )}
    </Card>
  );
}

// ── Device posture checks (S7.5.3) ───────────────────────────────────────────────────
// Per-check org opt-in (no configured check = off — the unlock-then-opt-in convention).
// Three legibility requirements (the slice-3 rider): (1) per-platform NON-coverage is
// visible (an os_version min for macOS only must SAY Windows is unconstrained), (2) a
// device that doesn't report shows as UNKNOWN, never as a pass (rendered on the Devices
// page), (3) the verbatim honesty line sits HERE, where an admin configures the checks.
function PostureChecksSection({
  orgId,
  canManage,
}: {
  orgId: string;
  canManage: boolean;
}) {
  const [checks, setChecks] = useState<HealthCheck[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [saveNote, setSaveNote] = useState<string | null>(null);
  // os_version editor state (min inputs live-preview the coverage indicator).
  const [osMode, setOsMode] = useState<CheckMode>("off");
  const [osMacos, setOsMacos] = useState("");
  const [osWindows, setOsWindows] = useState("");

  const load = useCallback(async () => {
    const r = await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/health-checks", {
        params: { path: { orgId } },
      }),
    );
    setLoadError(r.ok ? null : r.error);
    if (r.ok) {
      const list = r.data as HealthCheck[];
      setChecks(list);
      setOsMode(checkModeOf(list, "os_version"));
      const mins = osVersionMins(list.find((c) => c.kind === "os_version"));
      setOsMacos(mins.macos);
      setOsWindows(mins.windows);
    }
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load]);

  async function saveCheck(
    kind: HealthCheck["kind"],
    mode: CheckMode,
    param?: Record<string, unknown> | null,
  ) {
    setBusy(true);
    setErr(null);
    setSaveNote(null);
    if (mode === "off") {
      const { error } = await api.DELETE(
        "/api/v1/organizations/{orgId}/health-checks/{checkKind}",
        {
          params: { path: { orgId, checkKind: kind } },
        },
      );
      setBusy(false);
      if (error)
        return setErr(apiErrorMessage(error, "Could not turn the check off."));
      return load();
    }
    const { data, error } = await api.PUT(
      "/api/v1/organizations/{orgId}/health-checks/{checkKind}",
      {
        params: { path: { orgId, checkKind: kind } },
        body: {
          mode,
          param: (param ?? undefined) as Record<string, never> | undefined,
        },
      },
    );
    setBusy(false);
    if (error)
      return setErr(apiErrorMessage(error, "Could not save the check."));
    setSaveNote(
      wouldFailCopy(mode, (data as HealthCheck | undefined)?.would_fail_count),
    );
    load();
  }

  function saveOsVersion() {
    if (osMode === "off") return saveCheck("os_version", "off");
    const param = buildOsVersionParam({ macos: osMacos, windows: osWindows });
    if (!param)
      return setErr(
        "Set a minimum version for at least one platform, or turn the check off.",
      );
    return saveCheck("os_version", osMode, param);
  }

  const diskMode = checkModeOf(checks, "disk_encryption");
  const coverage = osVersionCoverage({
    macos: osMode === "off" ? "" : osMacos,
    windows: osMode === "off" ? "" : osWindows,
  });

  return (
    <Card className="mt-4">
      <h2 className="text-sm font-semibold text-slate-300">
        Device posture checks
      </h2>
      <p className="mt-1 text-xs text-slate-500">
        Per-check requirements evaluated on every device self-report.{" "}
        <span className="text-slate-400">warn</span> surfaces a warning;{" "}
        <span className="text-amber-300">require</span> disconnects a
        non-compliant device within seconds of its report.
      </p>
      {/* The honesty line — verbatim, at the point of configuration (D6, locked). */}
      <div className="mt-2 rounded-md border border-white/10 bg-white/5 px-3 py-2 text-xs text-slate-400">
        {POSTURE_HONESTY_LINE}
      </div>

      {loadError && <LoadRetry error={loadError} onRetry={load} />}
      <ErrorText>{err}</ErrorText>
      {saveNote && (
        <div className="mt-3 rounded-md border border-warn/30 bg-warn/5 px-3 py-2 text-xs text-amber-300">
          {saveNote}
        </div>
      )}

      {checks != null && !loadError && (
        <div className="mt-4 space-y-4">
          {/* Disk encryption */}
          <div className="rounded-md bg-white/5 px-3 py-3">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-200">Disk encryption</p>
                <p className="text-xs text-slate-500">
                  FileVault (macOS) / BitLocker (Windows), as reported by the
                  device.
                </p>
              </div>
              {canManage ? (
                <Select
                  className="w-32"
                  value={diskMode}
                  disabled={busy}
                  onChange={(e) =>
                    saveCheck("disk_encryption", e.target.value as CheckMode)
                  }
                >
                  <option value="off">Off</option>
                  <option value="warn">Warn</option>
                  <option value="require">Require</option>
                </Select>
              ) : (
                <span className="text-xs text-slate-400">{diskMode}</span>
              )}
            </div>
            {/* A device that reports the fact as ABSENT (couldn't read it) is UNKNOWN for this
                check — unknown never blocks, and it is not compliance. */}
          </div>

          {/* OS version */}
          <div className="rounded-md bg-white/5 px-3 py-3">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-200">Minimum OS version</p>
                <p className="text-xs text-slate-500">
                  Per-platform floors; leave a platform empty to not constrain
                  it.
                </p>
              </div>
              {canManage ? (
                <Select
                  className="w-32"
                  value={osMode}
                  disabled={busy}
                  onChange={(e) => setOsMode(e.target.value as CheckMode)}
                >
                  <option value="off">Off</option>
                  <option value="warn">Warn</option>
                  <option value="require">Require</option>
                </Select>
              ) : (
                <span className="text-xs text-slate-400">
                  {checkModeOf(checks, "os_version")}
                </span>
              )}
            </div>
            {osMode !== "off" && canManage && (
              <div className="mt-3 flex flex-wrap items-end gap-3">
                <Field label="macOS minimum">
                  <Input
                    value={osMacos}
                    onChange={(e) => setOsMacos(e.target.value)}
                    placeholder="e.g. 14.0"
                  />
                </Field>
                <Field label="Windows minimum">
                  <Input
                    value={osWindows}
                    onChange={(e) => setOsWindows(e.target.value)}
                    placeholder="e.g. 10.0.22631"
                  />
                </Field>
                <Button disabled={busy} onClick={saveOsVersion}>
                  Save
                </Button>
                {/* [6] Windows-version foot-gun: Win 11 reports major 10 (10.0.22000+),
                    so "11.0" would block the whole Windows fleet. Steer to build numbers. */}
                <p className="w-full text-xs text-slate-500">
                  Windows uses build numbers. Windows 11 reports as{" "}
                  <span className="font-mono text-slate-400">10.0.22000</span>,
                  not 11.0. Enter the build (e.g.{" "}
                  <span className="font-mono text-slate-400">10.0.22631</span>{" "}
                  for 23H2); run{" "}
                  <span className="font-mono text-slate-400">winver</span> to
                  check a device.
                </p>
              </div>
            )}
            {/* WF-OVPN-walk-3: "Off" hid the min-version inputs AND the Save button, so the setting
                could not be persisted from the UI (a dead-end). Off has nothing to configure, but it
                still needs its own Save affordance — saveOsVersion() already handles the off case. */}
            {osMode === "off" && canManage && (
              <div className="mt-3">
                <Button disabled={busy} onClick={saveOsVersion}>
                  Save
                </Button>
              </div>
            )}
            {/* THE coverage indicator (ratified rider): every reporting platform is named —
                a constrained platform shows its floor, an unconstrained one SAYS so. Never
                a silent gap. */}
            {osMode !== "off" && (
              <ul className="mt-2 space-y-0.5 text-xs">
                {coverage.map((c) => (
                  <li
                    key={c.platform}
                    className={c.covered ? "text-slate-400" : "text-amber-400"}
                  >
                    {c.label}
                  </li>
                ))}
              </ul>
            )}
          </div>
        </div>
      )}
    </Card>
  );
}
