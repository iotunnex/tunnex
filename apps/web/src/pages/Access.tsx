import { useCallback, useEffect, useState } from "react";
import {
  api,
  apiErrorMessage,
  loadOne,
  type Loaded,
  type Meta,
  type Org,
  type Member,
  type Role,
  type UserGroup,
  type Resource,
  type PolicyRule,
  type ZeroTrustMode,
  type AffectedDevice,
  type DeviceApproval,
  type Device,
  type HealthCheck,
} from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button, Card, ErrorText, Field, Input, Modal, Select } from "../components/ui";
import {
  accessView,
  modeEnableConfirm,
  policyGate,
  roleFromMembers,
  ruleRow,
  sectionRender,
  staleNoticeText,
  pruneStaleRuleIds,
  swapRule,
  type LoadState,
} from "../lib/policyview";
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
// reassuring empty state.

// LoadRetry replaces a reassuring-empty render when a load FAILED — a transient API error
// must be legible + retryable, not shown as "none / not an admin".
function LoadRetry({ error, onRetry }: { error: string; onRetry: () => void }) {
  return (
    <div className="mt-2 rounded-md border border-warn/30 bg-warn/5 px-3 py-2 text-xs text-amber-300">
      {error}{" "}
      <button className="underline underline-offset-2 hover:text-amber-200" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

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
    if (!first) return setFatal("You are not a member of any organization yet.");
    setOrg(first);
    const memRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/members", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Member[]>;
    const resolved = roleFromMembers(memRes, myId);
    if (resolved.failed) return setRoleError(memRes.ok ? "Couldn't determine your role." : memRes.error);
    setMyRole(resolved.role);
    setRoleResolved(true);
  }, [myId]);
  useEffect(() => {
    reload();
  }, [reload]);

  const gate = policyGate({ role: myRole, emailVerified, edition: meta?.edition });
  const view = accessView({
    fatal: fatal != null,
    loadError: loadError != null,
    editionReady: meta != null && org != null,
    isEnterprise: gate.isEnterprise,
    roleError: roleError != null,
    roleResolved,
    canView: gate.canView,
  });

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Access policies</h1>
      <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>

      {view === "fatal" && <ErrorText>{fatal}</ErrorText>}
      {view === "load_retry" && <LoadRetry error={loadError ?? "Couldn't load."} onRetry={reload} />}
      {view === "role_retry" && <LoadRetry error={roleError ?? "Couldn't determine your role."} onRetry={reload} />}
      {(view === "loading" || view === "role_loading") && <p className="mt-6 text-sm text-slate-500">Loading…</p>}

      {view === "upsell" && (
        <Card className="mt-6">
          <h2 className="text-sm font-semibold text-slate-300">Zero Trust access</h2>
          <p className="mt-1 text-xs text-slate-500">
            Policy rules, device approval, and default-deny enforcement are a Tunnex Enterprise feature.
          </p>
        </Card>
      )}

      {view === "member_gate" && (
        <Card className="mt-6">
          <p className="text-sm text-slate-400">Access policies are managed by owners and admins.</p>
        </Card>
      )}

      {view === "admin_body" && org && (
        <>
          <ModeSection orgId={org.id} canManage={gate.canManagePolicy} />
          <RulesSection orgId={org.id} canManage={gate.canManagePolicy} />
          <GroupsResourcesSection orgId={org.id} canManage={gate.canManagePolicy} />
          <DeviceApprovalSection orgId={org.id} canManage={gate.canManageDevices} />
          <PostureChecksSection orgId={org.id} canManage={gate.canManageDeviceHealth} />
        </>
      )}
    </div>
  );
}

// ── Zero Trust mode ─────────────────────────────────────────────────────────────────
function ModeSection({ orgId, canManage }: { orgId: string; canManage: boolean }) {
  const [mode, setMode] = useState<"off" | "enforcing" | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [confirming, setConfirming] = useState(false);
  const [confirmCount, setConfirmCount] = useState(0);
  const [busy, setBusy] = useState(false);
  const [affected, setAffected] = useState<AffectedDevice[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    const r = await loadOne(() => api.GET("/api/v1/organizations/{orgId}/zero-trust-mode", { params: { path: { orgId } } }));
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
    const r = await loadOne(() => api.GET("/api/v1/organizations/{orgId}/policies", { params: { path: { orgId } } }));
    if (!r.ok) return setErr("Couldn't verify the current rule count — retry.");
    setConfirmCount((r.data as PolicyRule[]).length);
    setConfirming(true);
  }

  async function setModeTo(next: "off" | "enforcing") {
    setBusy(true);
    setErr(null);
    setAffected(null);
    const { data, error } = await api.PUT("/api/v1/organizations/{orgId}/zero-trust-mode", {
      params: { path: { orgId } },
      body: { mode: next },
    });
    setBusy(false);
    setConfirming(false);
    if (error) return setErr(apiErrorMessage(error, "Could not change the mode."));
    const zt = data as ZeroTrustMode | undefined;
    if (zt) {
      setMode(zt.mode);
      if (zt.affected_full_tunnel_devices?.length) setAffected(zt.affected_full_tunnel_devices);
    }
  }

  const confirm = modeEnableConfirm(confirmCount);

  return (
    <Card className="mt-6">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-300">Zero Trust mode</h2>
          <p className="mt-1 text-xs text-slate-500">
            {mode === "enforcing"
              ? "Enforcing — default-deny; only your allow rules pass."
              : mode === "off"
                ? "Off — legacy full-mesh (all devices reach all devices)."
                : loadError
                  ? "—"
                  : "…"}
          </p>
        </div>
        {canManage && mode != null && !loadError && (
          <Button
            variant={mode === "enforcing" ? "ghost" : "primary"}
            disabled={busy}
            onClick={() => (mode === "enforcing" ? setModeTo("off") : openEnableConfirm())}
          >
            {mode === "enforcing" ? "Disable" : "Enable enforcing"}
          </Button>
        )}
      </div>
      {loadError && <LoadRetry error={loadError} onRetry={load} />}
      <ErrorText>{err}</ErrorText>

      {affected && (
        <div className="mt-3 rounded-md border border-warn/30 bg-warn/5 px-3 py-2 text-xs text-amber-300">
          Now enforcing. {affected.length} full-tunnel device(s) lost internet egress until a rule allows it:
          <span className="text-amber-200"> {affected.map((d) => d.name).join(", ")}</span>
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
              <Button variant={confirm.danger ? "danger" : "primary"} disabled={busy} onClick={() => setModeTo("enforcing")}>
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
function RulesSection({ orgId, canManage }: { orgId: string; canManage: boolean }) {
  const [rules, setRules] = useState<PolicyRule[]>([]);
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [loaded, setLoaded] = useState<LoadState>({ groupsLoaded: false, resourcesLoaded: false });
  const [loadError, setLoadError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<PolicyRule | null>(null);
  // SINGLE source of truth for the partial-swap warning: the SET of rule ids a create-then-
  // delete left un-deleted. The notice is DERIVED (staleNoticeText) — no separate state to
  // desync ([291]/[309]/[371]). Pruned ONLY on a successful load (amendment A), per-id (B).
  const [staleRuleIds, setStaleRuleIds] = useState<string[]>([]);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    setErr(null); // [310]: never carry a stale partial-load/mutation error into a fresh load
    const [rr, gr, resr] = await Promise.all([
      loadOne(() => api.GET("/api/v1/organizations/{orgId}/policies", { params: { path: { orgId } } })),
      loadOne(() => api.GET("/api/v1/organizations/{orgId}/groups", { params: { path: { orgId } } })),
      loadOne(() => api.GET("/api/v1/organizations/{orgId}/resources", { params: { path: { orgId } } })),
    ]);
    // The RULES fetch failing means the section cannot render truthfully — show retry, NOT
    // the reassuring "No rules — enforcing denies everything" ([2]). Amendment A: on this
    // FAILED path the stale-rule set is left untouched (the warning persists).
    if (!rr.ok) return setLoadError(rr.error);
    setLoadError(null);
    const freshRules = rr.data as PolicyRule[];
    setRules(freshRules);
    setGroups((gr.ok ? (gr.data as UserGroup[]) : []) as UserGroup[]);
    setResources((resr.ok ? (resr.data as Resource[]) : []) as Resource[]);
    // D-a6 loaded flags come from the SAME source: a set that FAILED to load → its refs are
    // "unresolved", not "deleted".
    setLoaded({ groupsLoaded: gr.ok, resourcesLoaded: resr.ok });
    setErr(gr.ok && resr.ok ? null : "Some groups/resources failed to load — names may show as unresolved. Refresh.");
    // The ONLY clear path (amendment A: gated on this successful load): drop stale ids no
    // longer present, keep the rest (B).
    setStaleRuleIds((prev) => pruneStaleRuleIds(prev, true, freshRules));
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load]);

  const notice = staleNoticeText(staleRuleIds); // DERIVED — no notice state

  async function del(id: string) {
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/policies/{ruleId}", {
      params: { path: { orgId, ruleId: id } },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not delete the rule."));
    load();
  }

  const view = sectionRender(loadError, notice);

  return (
    <Card className="mt-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold text-slate-300">Rules</h2>
        {canManage && !view.showRetry && (
          <Button onClick={() => setCreating(true)} disabled={groups.length === 0}>
            Add rule
          </Button>
        )}
      </div>
      <p className="mt-1 text-xs text-slate-500">Allow rules: a source group may reach a destination group or resource.</p>

      {/* [291] legibility signals COMPOSE: the partial-swap notice + a mutation error render at
          TOP LEVEL — a load failure replaces the LIST (content), never a warning. */}
      {view.showNotice && <p className="mt-2 text-xs text-amber-300">{notice}</p>}
      <ErrorText>{err}</ErrorText>
      {view.showRetry && <LoadRetry error={loadError ?? "Couldn't load rules."} onRetry={load} />}

      {view.showContent && (
        <>
          {groups.length === 0 && loaded.groupsLoaded && (
            <p className="mt-2 text-xs text-slate-500">Create a group first — every rule&rsquo;s source is a group.</p>
          )}
          <ul className="mt-3 space-y-1">
            {rules.map((r) => {
              const row = ruleRow(r, groups, resources, loaded);
              return (
                <li key={r.id} className="flex items-center justify-between rounded-md bg-white/5 px-3 py-2 text-sm">
                  <span className="text-slate-200">
                    <RefText label={row.src.label} broken={row.src.state !== "ok"} /> <span className="text-slate-500">→</span>{" "}
                    <RefText label={row.dst.label} broken={row.dst.state !== "ok"} />
                  </span>
                  {canManage && (
                    <span className="flex gap-2">
                      <Button variant="ghost" onClick={() => setEditing(r)}>
                        Edit
                      </Button>
                      <Button variant="danger" onClick={() => del(r.id)}>
                        Delete
                      </Button>
                    </span>
                  )}
                </li>
              );
            })}
            {rules.length === 0 && (
              <li className="text-xs text-slate-500">No rules — under Enforcing, all device-to-device traffic is denied.</li>
            )}
          </ul>
        </>
      )}

      {(creating || editing) && (
        <RuleFormModal
          orgId={orgId}
          groups={groups}
          resources={resources}
          editing={editing}
          onClose={() => {
            setCreating(false);
            setEditing(null);
          }}
          onDone={(staleId) => {
            // A partial swap adds the un-deleted rule id to the set; a clean create adds
            // nothing (so it can never drop a live warning — [371]).
            if (staleId) setStaleRuleIds((prev) => (prev.includes(staleId) ? prev : [...prev, staleId]));
            setCreating(false);
            setEditing(null);
            load();
          }}
        />
      )}
    </Card>
  );
}

function RefText({ label, broken }: { label: string; broken: boolean }) {
  return broken ? <span className="text-amber-400">⚠ {label}</span> : <span>{label}</span>;
}

// RuleFormModal creates OR edits a rule. Editing = CREATE-THEN-DELETE (D-a5) via swapRule —
// gap-free (allow-only union), never delete-first, with a LEGIBLE partial on delete-fail.
function RuleFormModal({
  orgId,
  groups,
  resources,
  editing,
  onClose,
  onDone,
}: {
  orgId: string;
  groups: UserGroup[];
  resources: Resource[];
  editing: PolicyRule | null;
  onClose: () => void;
  onDone: (staleRuleId?: string) => void;
}) {
  const [src, setSrc] = useState(editing?.src_group_id ?? groups[0]?.id ?? "");
  const [dstKind, setDstKind] = useState<"group" | "resource">(editing?.dst_kind ?? "group");
  const [dstGroup, setDstGroup] = useState(editing?.dst_group_id ?? groups[0]?.id ?? "");
  const [dstResource, setDstResource] = useState(editing?.dst_resource_id ?? resources[0]?.id ?? "");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  function bodyFor() {
    return dstKind === "group"
      ? { src_group_id: src, dst_kind: "group" as const, dst_group_id: dstGroup }
      : { src_group_id: src, dst_kind: "resource" as const, dst_resource_id: dstResource };
  }

  async function submit() {
    setBusy(true);
    setErr(null);
    // [8]: guard a 2xx-with-no-body — never let (data).id throw and strand busy=true.
    const create = async (): Promise<{ id: string } | { error: unknown }> => {
      const { data, error } = await api.POST("/api/v1/organizations/{orgId}/policies", {
        params: { path: { orgId } },
        body: bodyFor(),
      });
      if (error) return { error };
      const id = (data as PolicyRule | undefined)?.id;
      if (!id) return { error: { error: { message: "Server returned no rule id." } } };
      return { id };
    };

    if (!editing) {
      const created = await create();
      setBusy(false);
      if ("error" in created) return setErr(apiErrorMessage(created.error, "Could not create the rule."));
      return onDone();
    }

    const out = await swapRule(editing.id, create, async (id) =>
      api.DELETE("/api/v1/organizations/{orgId}/policies/{ruleId}", { params: { path: { orgId, ruleId: id } } }),
    );
    setBusy(false);
    if (out.outcome === "create_failed") return setErr(apiErrorMessage(out.error, "Could not create the new rule."));
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
          <Button disabled={busy || !src || (dstKind === "group" ? !dstGroup : !dstResource)} onClick={submit}>
            {editing ? "Save" : "Create"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <Field label="Source group">
          <Select value={src} onChange={(e) => setSrc(e.target.value)}>
            {groups.map((g) => (
              <option key={g.id} value={g.id}>
                {g.name}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="Destination type">
          <Select value={dstKind} onChange={(e) => setDstKind(e.target.value as "group" | "resource")}>
            <option value="group">Group (device-to-device)</option>
            <option value="resource">Resource (CIDR / port)</option>
          </Select>
        </Field>
        {dstKind === "group" ? (
          <Field label="Destination group">
            <Select value={dstGroup} onChange={(e) => setDstGroup(e.target.value)}>
              {groups.map((g) => (
                <option key={g.id} value={g.id}>
                  {g.name}
                </option>
              ))}
            </Select>
          </Field>
        ) : (
          <Field label="Destination resource">
            <Select value={dstResource} onChange={(e) => setDstResource(e.target.value)}>
              {resources.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name}
                </option>
              ))}
            </Select>
          </Field>
        )}
        <ErrorText>{err}</ErrorText>
      </div>
    </Modal>
  );
}

// ── Groups & Resources ──────────────────────────────────────────────────────────────
function GroupsResourcesSection({ orgId, canManage }: { orgId: string; canManage: boolean }) {
  const [groups, setGroups] = useState<UserGroup[]>([]);
  const [resources, setResources] = useState<Resource[]>([]);
  const [groupsError, setGroupsError] = useState<string | null>(null);
  const [resourcesError, setResourcesError] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [newGroup, setNewGroup] = useState("");
  const [newRes, setNewRes] = useState({ name: "", cidr: "", protocol: "any" as "any" | "tcp" | "udp" });

  const load = useCallback(async () => {
    const [gr, resr] = await Promise.all([
      loadOne(() => api.GET("/api/v1/organizations/{orgId}/groups", { params: { path: { orgId } } })),
      loadOne(() => api.GET("/api/v1/organizations/{orgId}/resources", { params: { path: { orgId } } })),
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
    if (error) return setErr(apiErrorMessage(error, "Could not create the group."));
    setNewGroup("");
    load();
  }
  async function delGroup(id: string) {
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/groups/{groupId}", {
      params: { path: { orgId, groupId: id } },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not delete the group."));
    load();
  }
  async function addResource() {
    if (!newRes.name.trim() || !newRes.cidr.trim()) return;
    const { error } = await api.POST("/api/v1/organizations/{orgId}/resources", {
      params: { path: { orgId } },
      body: { name: newRes.name.trim(), cidr: newRes.cidr.trim(), protocol: newRes.protocol },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not create the resource."));
    setNewRes({ name: "", cidr: "", protocol: "any" });
    load();
  }
  async function delResource(id: string) {
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/resources/{resourceId}", {
      params: { path: { orgId, resourceId: id } },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not delete the resource."));
    load();
  }

  return (
    <Card className="mt-4">
      <h2 className="text-sm font-semibold text-slate-300">Groups &amp; resources</h2>
      <ErrorText>{err}</ErrorText>
      <div className="mt-3 grid gap-4 sm:grid-cols-2">
        <div>
          <p className="text-xs font-medium text-slate-400">Groups (rule sources / device-to-device targets)</p>
          {groupsError ? (
            <LoadRetry error={groupsError} onRetry={load} />
          ) : (
            <>
              <ul className="mt-2 space-y-1">
                {groups.map((g) => (
                  <li key={g.id} className="flex items-center justify-between rounded-md bg-white/5 px-3 py-1.5 text-sm text-slate-200">
                    {g.name}
                    {canManage && (
                      <Button variant="danger" onClick={() => delGroup(g.id)}>
                        Delete
                      </Button>
                    )}
                  </li>
                ))}
                {groups.length === 0 && <li className="text-xs text-slate-500">No groups yet.</li>}
              </ul>
              {canManage && (
                <div className="mt-2 flex gap-2">
                  <Input placeholder="Group name" value={newGroup} onChange={(e) => setNewGroup(e.target.value)} />
                  <Button onClick={addGroup}>Add</Button>
                </div>
              )}
            </>
          )}
        </div>
        <div>
          <p className="text-xs font-medium text-slate-400">Resources (CIDR : protocol : ports)</p>
          {resourcesError ? (
            <LoadRetry error={resourcesError} onRetry={load} />
          ) : (
            <>
              <ul className="mt-2 space-y-1">
                {resources.map((r) => (
                  <li key={r.id} className="flex items-center justify-between rounded-md bg-white/5 px-3 py-1.5 text-sm text-slate-200">
                    <span>
                      {r.name} <span className="text-slate-500">{r.cidr}</span>
                    </span>
                    {canManage && (
                      <Button variant="danger" onClick={() => delResource(r.id)}>
                        Delete
                      </Button>
                    )}
                  </li>
                ))}
                {resources.length === 0 && <li className="text-xs text-slate-500">No resources yet.</li>}
              </ul>
              {canManage && (
                <div className="mt-2 space-y-2">
                  <Input placeholder="Name" value={newRes.name} onChange={(e) => setNewRes({ ...newRes, name: e.target.value })} />
                  <div className="flex gap-2">
                    <Input placeholder="CIDR e.g. 10.0.5.0/24" value={newRes.cidr} onChange={(e) => setNewRes({ ...newRes, cidr: e.target.value })} />
                    <Select value={newRes.protocol} onChange={(e) => setNewRes({ ...newRes, protocol: e.target.value as "any" | "tcp" | "udp" })}>
                      <option value="any">any</option>
                      <option value="tcp">tcp</option>
                      <option value="udp">udp</option>
                    </Select>
                    <Button onClick={addResource}>Add</Button>
                  </div>
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
function DeviceApprovalSection({ orgId, canManage }: { orgId: string; canManage: boolean }) {
  const [mode, setMode] = useState<"off" | "on" | null>(null);
  const [modeError, setModeError] = useState<string | null>(null);
  const [pending, setPending] = useState<Device[]>([]);
  const [pendingError, setPendingError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [dr, pr] = await Promise.all([
      loadOne(() => api.GET("/api/v1/organizations/{orgId}/device-approval", { params: { path: { orgId } } })),
      loadOne(() => api.GET("/api/v1/organizations/{orgId}/devices/pending", { params: { path: { orgId } } })),
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
    const { error } = await api.PUT("/api/v1/organizations/{orgId}/device-approval", {
      params: { path: { orgId } },
      body: { mode: next },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not change device approval."));
    load();
  }
  async function decide(deviceId: string, action: "approve" | "reject") {
    const path =
      action === "approve"
        ? "/api/v1/organizations/{orgId}/devices/{deviceId}/approve"
        : "/api/v1/organizations/{orgId}/devices/{deviceId}/reject";
    const { error } = await api.POST(path, { params: { path: { orgId, deviceId } } });
    if (error) return setErr(apiErrorMessage(error, `Could not ${action} the device.`));
    load();
  }

  return (
    <Card className="mt-4">
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-300">Device approval</h2>
          <p className="mt-1 text-xs text-slate-500">
            {mode === "on"
              ? "On — new devices enroll pending and cannot connect until approved."
              : mode === "off"
                ? "Off — new devices are active on enrollment."
                : modeError
                  ? "—"
                  : "…"}
          </p>
        </div>
        {canManage && mode != null && !modeError && (
          <Button variant={mode === "on" ? "ghost" : "primary"} disabled={busy} onClick={() => setApproval(mode === "on" ? "off" : "on")}>
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
            <li key={d.id} className="flex items-center justify-between rounded-md bg-white/5 px-3 py-2 text-sm text-slate-200">
              <span>
                {d.name} <span className="text-slate-500">{d.assigned_ip}</span>
              </span>
              {canManage && (
                <span className="flex gap-2">
                  <Button onClick={() => decide(d.id, "approve")}>Approve</Button>
                  <Button variant="danger" onClick={() => decide(d.id, "reject")}>
                    Reject
                  </Button>
                </span>
              )}
            </li>
          ))}
          {pending.length === 0 && <li className="text-xs text-slate-500">No devices awaiting approval.</li>}
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
function PostureChecksSection({ orgId, canManage }: { orgId: string; canManage: boolean }) {
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
    const r = await loadOne(() => api.GET("/api/v1/organizations/{orgId}/health-checks", { params: { path: { orgId } } }));
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

  async function saveCheck(kind: HealthCheck["kind"], mode: CheckMode, param?: Record<string, unknown> | null) {
    setBusy(true);
    setErr(null);
    setSaveNote(null);
    if (mode === "off") {
      const { error } = await api.DELETE("/api/v1/organizations/{orgId}/health-checks/{checkKind}", {
        params: { path: { orgId, checkKind: kind } },
      });
      setBusy(false);
      if (error) return setErr(apiErrorMessage(error, "Could not turn the check off."));
      return load();
    }
    const { data, error } = await api.PUT("/api/v1/organizations/{orgId}/health-checks/{checkKind}", {
      params: { path: { orgId, checkKind: kind } },
      body: { mode, param: (param ?? undefined) as Record<string, never> | undefined },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not save the check."));
    setSaveNote(wouldFailCopy(mode, (data as HealthCheck | undefined)?.would_fail_count));
    load();
  }

  function saveOsVersion() {
    if (osMode === "off") return saveCheck("os_version", "off");
    const param = buildOsVersionParam({ macos: osMacos, windows: osWindows });
    if (!param) return setErr("Set a minimum version for at least one platform, or turn the check off.");
    return saveCheck("os_version", osMode, param);
  }

  const diskMode = checkModeOf(checks, "disk_encryption");
  const coverage = osVersionCoverage({ macos: osMode === "off" ? "" : osMacos, windows: osMode === "off" ? "" : osWindows });

  return (
    <Card className="mt-4">
      <h2 className="text-sm font-semibold text-slate-300">Device posture checks</h2>
      <p className="mt-1 text-xs text-slate-500">
        Per-check requirements evaluated on every device self-report. <span className="text-slate-400">warn</span> surfaces a
        warning; <span className="text-amber-300">require</span> disconnects a non-compliant device within seconds of its report.
      </p>
      {/* The honesty line — verbatim, at the point of configuration (D6, locked). */}
      <div className="mt-2 rounded-md border border-white/10 bg-white/5 px-3 py-2 text-xs text-slate-400">{POSTURE_HONESTY_LINE}</div>

      {loadError && <LoadRetry error={loadError} onRetry={load} />}
      <ErrorText>{err}</ErrorText>
      {saveNote && <div className="mt-3 rounded-md border border-warn/30 bg-warn/5 px-3 py-2 text-xs text-amber-300">{saveNote}</div>}

      {checks != null && !loadError && (
        <div className="mt-4 space-y-4">
          {/* Disk encryption */}
          <div className="rounded-md bg-white/5 px-3 py-3">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-slate-200">Disk encryption</p>
                <p className="text-xs text-slate-500">FileVault (macOS) / BitLocker (Windows), as reported by the device.</p>
              </div>
              {canManage ? (
                <Select className="w-32" value={diskMode} disabled={busy} onChange={(e) => saveCheck("disk_encryption", e.target.value as CheckMode)}>
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
                <p className="text-xs text-slate-500">Per-platform floors; leave a platform empty to not constrain it.</p>
              </div>
              {canManage ? (
                <Select className="w-32" value={osMode} disabled={busy} onChange={(e) => setOsMode(e.target.value as CheckMode)}>
                  <option value="off">Off</option>
                  <option value="warn">Warn</option>
                  <option value="require">Require</option>
                </Select>
              ) : (
                <span className="text-xs text-slate-400">{checkModeOf(checks, "os_version")}</span>
              )}
            </div>
            {osMode !== "off" && canManage && (
              <div className="mt-3 flex flex-wrap items-end gap-3">
                <Field label="macOS minimum">
                  <Input value={osMacos} onChange={(e) => setOsMacos(e.target.value)} placeholder="e.g. 14.0" />
                </Field>
                <Field label="Windows minimum">
                  <Input value={osWindows} onChange={(e) => setOsWindows(e.target.value)} placeholder="e.g. 10.0.22631" />
                </Field>
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
                  <li key={c.platform} className={c.covered ? "text-slate-400" : "text-amber-400"}>
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
