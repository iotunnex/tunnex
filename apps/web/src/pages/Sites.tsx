import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  apiErrorMessage,
  loadOne,
  type Loaded,
  type Meta,
  type Member,
  type Org,
  type Role,
  type Site,
  type SiteSubnet,
  type SiteReferences,
  type Node,
} from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button, Card, ErrorText, Field, Input, Modal, Select } from "../components/ui";
import { LoadRetry } from "../components/LoadRetry";
import { badgeClass } from "../lib/healthview";
import { roleFromMembers } from "../lib/policyview";
import {
  assembleTopology,
  crossesMultiSiteThreshold,
  disjointRefusal,
  nameMatchesExactly,
  siteGate,
  sitesView,
  subCeilingGateways,
  type GatewayView,
  type SiteCard,
} from "../lib/sitesview";

// Sites (S8.3): the topology + its mutation surfaces. Reads render wire-truth only (render-floor law);
// mutations all go through the AUDITED service endpoints (Slice-3 condition 4 — nothing routed around the
// audit trail). The pending queue + every mutation affordance are canManage-gated (D5: a member sees the
// read-only topology, never the queue).

interface Raw {
  sites: Site[];
  nodes: Node[];
  subnetsBySite: Record<string, SiteSubnet[]>;
}

export default function Sites() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  const emailVerified = state.status === "authed" && state.user.email_verified;
  const [meta, setMeta] = useState<Meta | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [myRole, setMyRole] = useState<Role | undefined>(undefined);
  const [raw, setRaw] = useState<Raw | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [registering, setRegistering] = useState(false);

  const reload = useCallback(async () => {
    setLoadError(null);
    setRaw(null);
    const mRes = await loadOne(() => api.GET("/api/v1/meta"));
    if (!mRes.ok) return setLoadError(mRes.error);
    setMeta(mRes.data as Meta);
    const oRes = await loadOne(() => api.GET("/api/v1/organizations"));
    if (!oRes.ok) return setLoadError(oRes.error);
    const first = (oRes.data as Org[])[0];
    if (!first) return setLoadError("You are not a member of any organization yet.");
    setOrg(first);
    const memRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/members", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Member[]>;
    setMyRole(roleFromMembers(memRes, myId).role);

    if ((mRes.data as Meta).edition !== "enterprise") return;
    const sRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/sites", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Site[]>;
    if (!sRes.ok) return setLoadError(sRes.error);
    const nRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/nodes", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Node[]>;
    if (!nRes.ok) return setLoadError(nRes.error);
    // Per-site subnet fetches are independent → run them in PARALLEL (review #6: was a serial for-await
    // that stalled N round-trips deep on an N-site org).
    const subResults = (await Promise.all(
      sRes.data.map((site) =>
        loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/sites/{siteId}/subnets", { params: { path: { orgId: first.id, siteId: site.id } } }),
        ),
      ),
    )) as Loaded<SiteSubnet[]>[];
    const subnetsBySite: Record<string, SiteSubnet[]> = {};
    for (let i = 0; i < sRes.data.length; i++) {
      const subRes = subResults[i];
      if (!subRes.ok) return setLoadError(subRes.error); // any failed subnet load → legible retry, not a partial topology
      subnetsBySite[sRes.data[i].id] = subRes.data;
    }
    setRaw({ sites: sRes.data, nodes: nRes.data, subnetsBySite });
  }, [myId]);
  useEffect(() => {
    reload();
  }, [reload]);

  const gate = siteGate({ role: myRole, emailVerified, edition: meta?.edition });
  const view = sitesView({ editionReady: meta != null && org != null, loadError: loadError != null, isEnterprise: gate.isEnterprise });

  const cards: SiteCard[] = useMemo(() => (raw ? assembleTopology(raw.sites, raw.subnetsBySite, raw.nodes) : []), [raw]);
  // Approved-subnet count per site — the CW threshold input. Unbound nodes — the bind picker. All gateways
  // (nodes bound to any site) — the CW sub-ceiling naming input. All derived from wire data.
  const approvedCountBySite = useMemo(() => {
    const m: Record<string, number> = {};
    if (raw) for (const [sid, subs] of Object.entries(raw.subnetsBySite)) m[sid] = subs.filter((s) => s.status === "approved").length;
    return m;
  }, [raw]);
  const unboundNodes = useMemo(() => (raw ? raw.nodes.filter((n) => !n.site_id && n.status === "active") : []), [raw]);
  const allGateways = useMemo(() => cards.flatMap((c) => c.gateways), [cards]);

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Sites</h1>
          <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>
        </div>
        {view === "body" && gate.canManage && <Button onClick={() => setRegistering(true)}>Register site</Button>}
      </div>

      {view === "load_retry" && <LoadRetry error={loadError ?? "Couldn't load."} onRetry={reload} />}
      {view === "loading" && <p className="mt-6 text-sm text-slate-500">Loading…</p>}

      {view === "upsell" && (
        <Card className="mt-6">
          <h2 className="text-sm font-semibold text-slate-300">Site-to-site networking</h2>
          <p className="mt-1 text-xs text-slate-500">
            Connecting your sites and routing traffic between them is a Tunnex Enterprise feature.
          </p>
        </Card>
      )}

      {view === "body" && raw != null && org != null && (
        <>
          {gate.canManage && (
            <PendingQueue
              orgId={org.id}
              approvedCountBySite={approvedCountBySite}
              allGateways={allGateways}
              ceiling={meta?.protocol_version ?? 0}
              onDone={reload}
            />
          )}
          <Topology
            cards={cards}
            canManage={gate.canManage}
            orgId={org.id}
            unboundNodes={unboundNodes}
            onDone={reload}
          />
        </>
      )}
      {view === "body" && raw == null && <p className="mt-6 text-sm text-slate-500">Loading…</p>}

      {registering && org && <RegisterSiteModal orgId={org.id} onDone={reload} onClose={() => setRegistering(false)} />}
    </div>
  );
}

// ── the read-only topology + per-site mutation affordances ───────────────────────────
function Topology({
  cards,
  canManage,
  orgId,
  unboundNodes,
  onDone,
}: {
  cards: SiteCard[];
  canManage: boolean;
  orgId: string;
  unboundNodes: Node[];
  onDone: () => void;
}) {
  if (cards.length === 0) {
    return (
      <Card className="mt-6">
        <p className="text-sm text-slate-400">
          No sites yet.{canManage ? " Register a site to connect a gateway." : " An owner or admin can register one."}
        </p>
      </Card>
    );
  }
  return (
    <div className="mt-6 space-y-4">
      {cards.map((c) => (
        <SiteCardView key={c.id} card={c} canManage={canManage} orgId={orgId} unboundNodes={unboundNodes} onDone={onDone} />
      ))}
    </div>
  );
}

function SiteCardView({
  card,
  canManage,
  orgId,
  unboundNodes,
  onDone,
}: {
  card: SiteCard;
  canManage: boolean;
  orgId: string;
  unboundNodes: Node[];
  onDone: () => void;
}) {
  const [modal, setModal] = useState<"subnet" | "bind" | "unbind" | "delete" | null>(null);
  const [removing, setRemoving] = useState<{ id: string; cidr: string; status: string } | null>(null); // WF-5
  const hasGateway = card.gateways.length > 0;
  return (
    <Card>
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-semibold text-white">{card.name}</h2>
        <span className="text-xs text-slate-500">{card.gateways.length === 1 ? "1 gateway" : `${card.gateways.length} gateways`}</span>
      </div>

      {card.gateways.length === 0 ? (
        <p className="mt-2 text-xs text-slate-500">No gateway bound.</p>
      ) : (
        <ul className="mt-2 space-y-1">
          {card.gateways.map((g) => (
            <GatewayRow key={g.id} g={g} />
          ))}
        </ul>
      )}

      {card.subnets.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-2">
          {card.subnets.map((s) => (
            <span
              key={s.id}
              className={`rounded px-2 py-0.5 text-xs ${
                s.status === "approved" ? "bg-white/5 text-slate-300" : "border border-amber-500/30 text-amber-300"
              }`}
              title={s.status === "approved" ? "Approved — routed" : "Pending approval — not yet routed"}
            >
              {s.cidr}
              {s.status === "pending" && " · pending"}
              {canManage && (
                <button
                  type="button"
                  className="ml-1.5 text-slate-500 hover:text-rose-400"
                  aria-label={`Remove ${s.cidr}`}
                  title="Remove this subnet (un-advertise)"
                  onClick={() => setRemoving({ id: s.id, cidr: s.cidr, status: s.status })}
                >
                  ✕
                </button>
              )}
            </span>
          ))}
        </div>
      )}

      {/* WF-3: guided cloud-fabric setup, SURFACED IN-UI (not docs-only). STATIC per cloud — the SDN
          steps that get a behind-host's packet to this gateway are un-codeable, so we show the copy-paste
          the operator applies in ONE cloud-console visit (the Zero-Touch Law boundary clause). No
          cloud-detection this pass (that rides S8.5); doc link for the full reference. */}
      {hasGateway && card.subnets.some((s) => s.status === "approved") && (
        <details className="mt-3 rounded-lg border border-white/5 bg-ink-900/60 px-3 py-2 text-xs text-slate-400">
          <summary className="cursor-pointer text-slate-300">Cloud fabric setup — one console visit per side (why a behind-host may not reach yet)</summary>
          <div className="mt-2 space-y-2">
            <p>
              A gateway VM forwards for hosts on its LAN, but the cloud SDN must (1) let the VM forward and (2)
              route the REMOTE site's CIDR to this gateway. Apply once, in the cloud console — never on the gateway.
            </p>
            <p>
              <span className="font-semibold text-slate-300">Both clouds:</span> enable <span className="font-mono">IP forwarding</span> on this gateway VM's NIC.
            </p>
            <p>
              <span className="font-semibold text-slate-300">Azure:</span> route table on the behind-hosts' subnet → add
              <span className="font-mono"> &lt;REMOTE_CIDR&gt;</span> → next hop <span className="font-mono">Virtual appliance</span> → this gateway's private IP.
            </p>
            <p>
              <span className="font-semibold text-slate-300">AWS:</span> disable <span className="font-mono">source/dest check</span> on the gateway ENI; route table → add
              <span className="font-mono"> &lt;REMOTE_CIDR&gt;</span> → target = the gateway instance/ENI.
            </p>
            <p className="text-slate-500">Full reference: <span className="font-mono">docs/deploy-cloud-gateway.md</span>.</p>
          </div>
        </details>
      )}

      {canManage && (
        <div className="mt-4 flex flex-wrap gap-2">
          <Button variant="ghost" onClick={() => setModal("subnet")}>Advertise subnet</Button>
          {hasGateway ? (
            <Button variant="ghost" onClick={() => setModal("unbind")}>Unbind gateway</Button>
          ) : (
            <Button variant="ghost" onClick={() => setModal("bind")} disabled={unboundNodes.length === 0}>
              Bind gateway
            </Button>
          )}
          <Button variant="danger" onClick={() => setModal("delete")}>Delete site</Button>
        </div>
      )}

      {modal === "subnet" && <AddSubnetModal orgId={orgId} siteId={card.id} onDone={onDone} onClose={() => setModal(null)} />}
      {modal === "bind" && <BindGatewayModal orgId={orgId} siteId={card.id} nodes={unboundNodes} onDone={onDone} onClose={() => setModal(null)} />}
      {modal === "unbind" && <UnbindConfirm orgId={orgId} siteId={card.id} onDone={onDone} onClose={() => setModal(null)} />}
      {modal === "delete" && <DeleteSiteModal orgId={orgId} site={card} onDone={onDone} onClose={() => setModal(null)} />}
      {removing && (
        <RemoveSubnetConfirm
          orgId={orgId}
          subnet={removing}
          onDone={() => {
            setRemoving(null);
            onDone();
          }}
          onClose={() => setRemoving(null)}
        />
      )}
    </Card>
  );
}

function GatewayRow({ g }: { g: GatewayView }) {
  return (
    <li className="flex items-center gap-2 text-sm">
      <span className="text-slate-200">{g.name}</span>
      {g.isHub && <span className="rounded bg-sky-500/10 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-sky-300">hub</span>}
      {g.status === "revoked" && <span className="text-xs text-rose-400">revoked</span>}
      {g.health && <span className={`text-xs ${badgeClass(g.health.tone)}`}>{g.health.label}</span>}
      <span className="ml-auto text-[11px] text-slate-500">
        {g.agentVersion}
        {g.maxPolicyVersion != null && ` · policy v${g.maxPolicyVersion}`}
      </span>
    </li>
  );
}

// ── mutation modals (all hit the audited service endpoints) ──────────────────────────
function RegisterSiteModal({ orgId, onDone, onClose }: { orgId: string; onDone: () => void; onClose: () => void }) {
  const [name, setName] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.POST("/api/v1/organizations/{orgId}/sites", { params: { path: { orgId } }, body: { name } });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not register the site."));
    onClose();
    onDone();
  }
  return (
    <Modal
      title="Register a site"
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={submit} disabled={busy || name.trim() === ""}>Register</Button>
        </>
      }
    >
      <Field label="Site name">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Mumbai office" autoFocus />
      </Field>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function AddSubnetModal({ orgId, siteId, onDone, onClose }: { orgId: string; siteId: string; onDone: () => void; onClose: () => void }) {
  const [cidr, setCidr] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.POST("/api/v1/organizations/{orgId}/sites/{siteId}/subnets", {
      params: { path: { orgId, siteId } },
      body: { cidr },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not advertise the subnet."));
    onClose();
    onDone();
  }
  return (
    <Modal
      title="Advertise a subnet"
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={submit} disabled={busy || cidr.trim() === ""}>Advertise</Button>
        </>
      }
    >
      <p className="text-xs text-slate-500">The subnet is advertised as PENDING — an owner or admin must approve it before it routes.</p>
      <div className="mt-2">
        <Field label="LAN CIDR">
          <Input value={cidr} onChange={(e) => setCidr(e.target.value)} placeholder="10.20.0.0/24" autoFocus />
        </Field>
      </div>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function BindGatewayModal({
  orgId,
  siteId,
  nodes,
  onDone,
  onClose,
}: {
  orgId: string;
  siteId: string;
  nodes: Node[];
  onDone: () => void;
  onClose: () => void;
}) {
  const [nodeId, setNodeId] = useState(nodes[0]?.id ?? "");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.POST("/api/v1/organizations/{orgId}/sites/{siteId}/bind", {
      params: { path: { orgId, siteId } },
      body: { node_id: nodeId },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not bind the gateway."));
    onClose();
    onDone();
  }
  return (
    <Modal
      title="Bind a gateway"
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={submit} disabled={busy || nodeId === ""}>Bind</Button>
        </>
      }
    >
      <Field label="Gateway node">
        <Select value={nodeId} onChange={(e) => setNodeId(e.target.value)}>
          {nodes.map((n) => (
            <option key={n.id} value={n.id}>{n.name}</option>
          ))}
        </Select>
      </Field>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function UnbindConfirm({ orgId, siteId, onDone, onClose }: { orgId: string; siteId: string; onDone: () => void; onClose: () => void }) {
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/sites/{siteId}/bind", { params: { path: { orgId, siteId } } });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not unbind the gateway."));
    onClose();
    onDone();
  }
  return (
    <Modal
      title="Unbind the gateway?"
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button onClick={submit} disabled={busy}>Unbind</Button>
        </>
      }
    >
      <p className="text-sm text-slate-400">
        The gateway's site-link peers and routes are swept. The site and its subnets are kept — bind a replacement to restore routing.
      </p>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

// WF-5: un-advertise / remove a single subnet — no longer needs a whole-site delete. The confirm STATES
// the full-sweep consequence for an approved subnet (route withdrawn from every gateway).
function RemoveSubnetConfirm({
  orgId,
  subnet,
  onDone,
  onClose,
}: {
  orgId: string;
  subnet: { id: string; cidr: string; status: string };
  onDone: () => void;
  onClose: () => void;
}) {
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/site-subnets/{subnetId}", {
      params: { path: { orgId, subnetId: subnet.id } },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not remove the subnet."));
    onClose();
    onDone();
  }
  return (
    <Modal
      title={`Remove ${subnet.cidr}?`}
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="danger" className="bg-danger hover:bg-danger" onClick={submit} disabled={busy}>Remove</Button>
        </>
      }
    >
      <p className="text-sm text-slate-400">
        {subnet.status === "approved" ? (
          <>
            This subnet is approved and routed. Removing it <span className="font-semibold">withdraws its route from every gateway</span> on
            the next reconcile — behind-hosts on other sites will no longer reach <span className="font-mono">{subnet.cidr}</span>.
          </>
        ) : (
          <>This pending subnet is not yet routed — removing it just un-advertises it.</>
        )}
      </p>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function DeleteSiteModal({ orgId, site, onDone, onClose }: { orgId: string; site: SiteCard; onDone: () => void; onClose: () => void }) {
  const [refs, setRefs] = useState<SiteReferences | null>(null);
  const [refErr, setRefErr] = useState<string | null>(null);
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    (async () => {
      const r = (await loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/sites/{siteId}", { params: { path: { orgId, siteId: site.id } } }),
      )) as Loaded<SiteReferences>;
      if (r.ok) setRefs(r.data);
      else setRefErr(r.error);
    })();
  }, [orgId, site.id]);

  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/sites/{siteId}", { params: { path: { orgId, siteId: site.id } } });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not delete the site."));
    onClose();
    onDone();
  }

  return (
    <Modal
      title={`Delete “${site.name}”?`}
      danger
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>Cancel</Button>
          <Button variant="primary" className="bg-danger hover:bg-danger" onClick={submit} disabled={busy || !nameMatchesExactly(typed, site.name)}>
            Delete site
          </Button>
        </>
      }
    >
      {/* PRESENT-TENSE cascade preview (the ratified copy — advisory, not a promise; the audit records the
          actual counts). */}
      {refErr && <p className="text-xs text-amber-300">Couldn’t read what this affects ({refErr}). Deleting still cascades.</p>}
      {refs && (
        <p className="text-sm text-slate-400">
          This deletes the site and cascades what currently references it: <strong>{refs.rule_count}</strong>{" "}
          {refs.rule_count === 1 ? "rule" : "rules"} and <strong>{refs.subnet_count}</strong>{" "}
          {refs.subnet_count === 1 ? "subnet" : "subnets"}; the gateway is unbound.
        </p>
      )}
      <p className="mt-3 text-xs text-slate-500">Type the site name to confirm.</p>
      <div className="mt-1">
        <Input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={site.name} autoFocus />
      </div>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

// ── the pending-approval queue (admin-only, D5) + the CW upgrade confirm ──────────────
function PendingQueue({
  orgId,
  approvedCountBySite,
  allGateways,
  ceiling,
  onDone,
}: {
  orgId: string;
  approvedCountBySite: Record<string, number>;
  allGateways: GatewayView[];
  ceiling: number;
  onDone: () => void;
}) {
  const [pending, setPending] = useState<SiteSubnet[] | null>(null);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<{ subnet: SiteSubnet; gateways: { id: string; name: string }[] } | null>(null);
  const [rowErr, setRowErr] = useState<string | null>(null);

  const loadQueue = useCallback(async () => {
    setLoadErr(null);
    const r = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/site-subnets/pending", { params: { path: { orgId } } }),
    )) as Loaded<SiteSubnet[]>;
    if (r.ok) setPending(r.data);
    else setLoadErr(r.error);
  }, [orgId]);
  useEffect(() => {
    loadQueue();
  }, [loadQueue]);

  // approve does the actual POST + shared error handling (verbatim refusal). Called directly for a
  // non-crossing approval, or from the CW confirm's onConfirm.
  async function approve(subnet: SiteSubnet) {
    setRowErr(null);
    const { error } = await api.POST("/api/v1/organizations/{orgId}/site-subnets/{subnetId}/approve", {
      params: { path: { orgId, subnetId: subnet.id } },
    });
    if (error) {
      // D3: a disjointness refusal renders VERBATIM (the API names the class + colliding range). No
      // client-side re-check.
      const refusal = disjointRefusal(error);
      return setRowErr(refusal ?? apiErrorMessage(error, "Could not approve the subnet."));
    }
    setConfirm(null);
    await loadQueue();
    onDone(); // refresh the topology (a newly-approved subnet now routes)
  }

  // onApproveClick decides whether this approval crosses the multi-site threshold with sub-ceiling
  // gateways present — if so it opens the CW confirm naming them; otherwise it approves directly.
  function onApproveClick(subnet: SiteSubnet) {
    const gateways = subCeilingGateways(allGateways, ceiling);
    if (crossesMultiSiteThreshold(subnet.site_id, approvedCountBySite) && gateways.length > 0) {
      setConfirm({ subnet, gateways });
    } else {
      approve(subnet);
    }
  }

  if (loadErr) return <Card className="mt-6"><LoadRetry error={loadErr} onRetry={loadQueue} /></Card>;
  if (pending == null) return null; // queue still loading — the topology below renders regardless
  if (pending.length === 0) return null; // nothing awaiting approval → no queue section

  return (
    <Card className="mt-6">
      <h2 className="text-sm font-semibold text-slate-300">Pending subnet approvals</h2>
      <p className="mt-1 text-xs text-slate-500">Advertised subnets route only once approved (disjointness is checked on approval).</p>
      <ul className="mt-3 space-y-2">
        {pending.map((s) => (
          <li key={s.id} className="flex items-center gap-3 text-sm">
            <span className="font-mono text-slate-200">{s.cidr}</span>
            <Button variant="ghost" className="ml-auto" onClick={() => onApproveClick(s)}>Approve</Button>
          </li>
        ))}
      </ul>
      <ErrorText>{rowErr}</ErrorText>

      {confirm && (
        <Modal
          title="Enable cross-site routing?"
          danger
          onDismiss={() => setConfirm(null)}
          actions={
            <>
              <Button variant="ghost" onClick={() => setConfirm(null)}>Cancel</Button>
              <Button onClick={() => approve(confirm.subnet)}>Approve anyway</Button>
            </>
          }
        >
          <p className="text-sm text-slate-400">
            Approving this subnet enables site-to-site routing, which requires policy version {ceiling}. These gateways cannot apply it
            and will <strong>deny all traffic</strong> until upgraded:
          </p>
          <ul className="mt-2 list-disc pl-5 text-sm text-rose-300">
            {confirm.gateways.map((g) => (
              <li key={g.id}>{g.name}</li>
            ))}
          </ul>
        </Modal>
      )}
    </Card>
  );
}
