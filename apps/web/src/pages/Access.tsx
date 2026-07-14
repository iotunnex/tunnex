import { useCallback, useEffect, useState } from "react";
import {
  api,
  apiErrorMessage,
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
} from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button, Card, ErrorText, Field, Input, Modal, Select } from "../components/ui";
import {
  modeEnableConfirm,
  policyGate,
  ruleRow,
  swapRule,
  swapPartialMessage,
  type LoadState,
} from "../lib/policyview";
// swapRule + swapPartialMessage power the create-then-delete rule edit (D-a5) in RuleFormModal.

// Access is the Zero Trust admin UI (S7.4a): one page, stacked Card sections —
// mode · rules · groups+resources · device-approval queue. Enterprise-gated; the
// consequential logic (mode-confirm copy, rule label joins, create-then-delete swap)
// lives in ../lib/policyview and is unit-tested there.
export default function Access() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  const emailVerified = state.status === "authed" && state.user.email_verified;
  const [meta, setMeta] = useState<Meta | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [myRole, setMyRole] = useState<Role | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [{ data: m }, { data: orgs, error: orgErr }] = await Promise.all([
          api.GET("/api/v1/meta"),
          api.GET("/api/v1/organizations"),
        ]);
        if (cancelled) return;
        if (m) setMeta(m);
        if (orgErr) return setError(apiErrorMessage(orgErr, "Could not load your organizations."));
        const first = orgs?.[0];
        if (!first) return setError("You are not a member of any organization yet.");
        setOrg(first);
        const { data: members } = await api.GET("/api/v1/organizations/{orgId}/members", {
          params: { path: { orgId: first.id } },
        });
        if (!cancelled) setMyRole((members as Member[] | undefined)?.find((mm) => mm.user_id === myId)?.role);
      } catch {
        if (!cancelled) setError("Could not reach the API.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [myId]);

  const gate = policyGate({ role: myRole, emailVerified, edition: meta?.edition });

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Access policies</h1>
      <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>
      <ErrorText>{error}</ErrorText>

      {!gate.isEnterprise && (
        <Card className="mt-6">
          <h2 className="text-sm font-semibold text-slate-300">Zero Trust access</h2>
          <p className="mt-1 text-xs text-slate-500">
            Policy rules, device approval, and default-deny enforcement are a Tunnex Enterprise feature.
          </p>
        </Card>
      )}

      {gate.isEnterprise && !gate.canView && (
        <Card className="mt-6">
          <p className="text-sm text-slate-400">Access policies are managed by owners and admins.</p>
        </Card>
      )}

      {org && gate.canView && (
        <>
          <ModeSection orgId={org.id} canManage={gate.canManagePolicy} />
          <RulesSection orgId={org.id} canManage={gate.canManagePolicy} />
          <GroupsResourcesSection orgId={org.id} canManage={gate.canManagePolicy} />
          <DeviceApprovalSection orgId={org.id} canManage={gate.canManageDevices} />
        </>
      )}
    </div>
  );
}

// ── Zero Trust mode ─────────────────────────────────────────────────────────────────
function ModeSection({ orgId, canManage }: { orgId: string; canManage: boolean }) {
  const [mode, setMode] = useState<"off" | "enforcing" | null>(null);
  const [ruleCount, setRuleCount] = useState(0);
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [affected, setAffected] = useState<AffectedDevice[] | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [{ data: zt }, { data: rules }] = await Promise.all([
      api.GET("/api/v1/organizations/{orgId}/zero-trust-mode", { params: { path: { orgId } } }),
      api.GET("/api/v1/organizations/{orgId}/policies", { params: { path: { orgId } } }),
    ]);
    if (zt) setMode((zt as ZeroTrustMode).mode);
    setRuleCount((rules as PolicyRule[] | undefined)?.length ?? 0);
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load]);

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
      // Post-hoc authoritative blast radius (D-A1) — never a client-computed one.
      if (zt.affected_full_tunnel_devices?.length) setAffected(zt.affected_full_tunnel_devices);
    }
  }

  const confirm = modeEnableConfirm(ruleCount);

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
                : "…"}
          </p>
        </div>
        {canManage && mode != null && (
          <Button
            variant={mode === "enforcing" ? "ghost" : "primary"}
            disabled={busy}
            onClick={() => (mode === "enforcing" ? setModeTo("off") : setConfirming(true))}
          >
            {mode === "enforcing" ? "Disable" : "Enable enforcing"}
          </Button>
        )}
      </div>
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
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<PolicyRule | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [{ data: rs }, { data: gs, error: gErr }, { data: res, error: rErr }] = await Promise.all([
      api.GET("/api/v1/organizations/{orgId}/policies", { params: { path: { orgId } } }),
      api.GET("/api/v1/organizations/{orgId}/groups", { params: { path: { orgId } } }),
      api.GET("/api/v1/organizations/{orgId}/resources", { params: { path: { orgId } } }),
    ]);
    setRules((rs as PolicyRule[] | undefined) ?? []);
    setGroups((gs as UserGroup[] | undefined) ?? []);
    setResources((res as Resource[] | undefined) ?? []);
    // Track load SUCCESS per set so the label can tell deleted from unresolved (D-a6).
    setLoaded({ groupsLoaded: !gErr, resourcesLoaded: !rErr });
    if (gErr || rErr) setErr("Some groups/resources failed to load — rule names may show as unresolved. Refresh.");
  }, [orgId]);
  useEffect(() => {
    load();
  }, [load]);

  async function del(id: string) {
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/policies/{ruleId}", {
      params: { path: { orgId, ruleId: id } },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not delete the rule."));
    load();
  }

  return (
    <Card className="mt-4">
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold text-slate-300">Rules</h2>
        {canManage && (
          <Button onClick={() => setCreating(true)} disabled={groups.length === 0}>
            Add rule
          </Button>
        )}
      </div>
      <p className="mt-1 text-xs text-slate-500">Allow rules: a source group may reach a destination group or resource.</p>
      {groups.length === 0 && <p className="mt-2 text-xs text-slate-500">Create a group first — every rule&rsquo;s source is a group.</p>}
      <ErrorText>{err}</ErrorText>
      {notice && <p className="mt-2 text-xs text-amber-300">{notice}</p>}

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
        {rules.length === 0 && <li className="text-xs text-slate-500">No rules — under Enforcing, all device-to-device traffic is denied.</li>}
      </ul>

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
          onDone={(msg) => {
            setNotice(msg ?? null);
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

// RuleFormModal creates OR edits a rule (src group → resource|group). Because there is no
// updatePolicyRule, an edit is a CREATE-THEN-DELETE swap (D-a5): create the new rule first,
// then delete the old — gap-free (allow-only union), never delete-first. A create-ok/delete-
// fail is surfaced as a LEGIBLE partial (both rules remain, retry the removal) via swapRule.
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
  onDone: (notice?: string) => void;
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
    const create = async () => {
      const { data, error } = await api.POST("/api/v1/organizations/{orgId}/policies", {
        params: { path: { orgId } },
        body: bodyFor(),
      });
      return error ? { error } : { id: (data as PolicyRule).id };
    };

    if (!editing) {
      const created = await create();
      setBusy(false);
      if ("error" in created) return setErr(apiErrorMessage(created.error, "Could not create the rule."));
      return onDone();
    }

    // EDIT = create-then-delete (D-a5). swapRule guarantees the ordering + a legible partial.
    const out = await swapRule(
      editing.id,
      create,
      async (id) =>
        api.DELETE("/api/v1/organizations/{orgId}/policies/{ruleId}", { params: { path: { orgId, ruleId: id } } }),
    );
    setBusy(false);
    if (out.outcome === "create_failed") return setErr(apiErrorMessage(out.error, "Could not create the new rule."));
    if (out.outcome === "partial") return onDone(swapPartialMessage(out.oldId.slice(0, 8)));
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
  const [err, setErr] = useState<string | null>(null);
  const [newGroup, setNewGroup] = useState("");
  const [newRes, setNewRes] = useState({ name: "", cidr: "", protocol: "any" as "any" | "tcp" | "udp" });

  const load = useCallback(async () => {
    const [{ data: gs }, { data: res }] = await Promise.all([
      api.GET("/api/v1/organizations/{orgId}/groups", { params: { path: { orgId } } }),
      api.GET("/api/v1/organizations/{orgId}/resources", { params: { path: { orgId } } }),
    ]);
    setGroups((gs as UserGroup[] | undefined) ?? []);
    setResources((res as Resource[] | undefined) ?? []);
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
        </div>
        <div>
          <p className="text-xs font-medium text-slate-400">Resources (CIDR : protocol : ports)</p>
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
        </div>
      </div>
    </Card>
  );
}

// ── Device approval (folded S7.3 admin surface) ─────────────────────────────────────
function DeviceApprovalSection({ orgId, canManage }: { orgId: string; canManage: boolean }) {
  const [mode, setMode] = useState<"off" | "on" | null>(null);
  const [pending, setPending] = useState<Device[]>([]);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const load = useCallback(async () => {
    const [{ data: da }, { data: pend }] = await Promise.all([
      api.GET("/api/v1/organizations/{orgId}/device-approval", { params: { path: { orgId } } }),
      api.GET("/api/v1/organizations/{orgId}/devices/pending", { params: { path: { orgId } } }),
    ]);
    if (da) setMode((da as DeviceApproval).mode);
    setPending((pend as Device[] | undefined) ?? []);
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
                : "…"}
          </p>
        </div>
        {canManage && mode != null && (
          <Button variant={mode === "on" ? "ghost" : "primary"} disabled={busy} onClick={() => setApproval(mode === "on" ? "off" : "on")}>
            {mode === "on" ? "Turn off" : "Require approval"}
          </Button>
        )}
      </div>
      <ErrorText>{err}</ErrorText>

      <p className="mt-3 text-xs font-medium text-slate-400">Pending devices</p>
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
    </Card>
  );
}
