import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  apiErrorMessage,
  loadOne,
  type Loaded,
  type Member,
  type Org,
  type Role,
  type Site,
  type K8sCluster,
  type K8sService,
} from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button, Card, ErrorText, Field, Input, Modal, Select } from "../components/ui";
import { LoadRetry } from "../components/LoadRetry";
import { roleFromMembers } from "../lib/policyview";
import { assembleClusters, k8sGate, managedEditWarning, objectControls, type ClusterCard } from "../lib/k8sview";
import { ManagedBadge } from "../components/ManagedBadge";

// Kubernetes (S10.3): the in-cluster connectivity surface — register a cluster (a synthetic VIP range fronted
// by a site gateway) and expose its Services to the fabric. CONNECTIVITY is CORE (all editions): this whole
// page is k8s:manage-gated but never edition-gated; the GRANT that reaches an exposed Service (Access page)
// is the enterprise governance gate. Every rendered field is wire-truth; the FQDN is READ from the server
// (never constructed in the client — "copy, don't construct").

interface Raw {
  clusters: K8sCluster[];
  services: K8sService[];
  sites: Site[]; // the register-cluster site picker (one gateway = one site)
}

export default function Kubernetes() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  const emailVerified = state.status === "authed" && state.user.email_verified;
  const [orgId, setOrgId] = useState<string | null>(null);
  const [myRole, setMyRole] = useState<Role | undefined>(undefined);
  const [raw, setRaw] = useState<Raw | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [registering, setRegistering] = useState(false);

  const reload = useCallback(async () => {
    setLoadError(null);
    setRaw(null);
    const oRes = await loadOne(() => api.GET("/api/v1/organizations"));
    if (!oRes.ok) return setLoadError(oRes.error);
    const first = (oRes.data as Org[])[0];
    if (!first) return setLoadError("You are not a member of any organization yet.");
    setOrgId(first.id);
    const memRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/members", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Member[]>;
    setMyRole(roleFromMembers(memRes, myId).role);
    const cRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/k8s/clusters", { params: { path: { orgId: first.id } } }),
    )) as Loaded<K8sCluster[]>;
    if (!cRes.ok) return setLoadError(cRes.error);
    const svcRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/k8s/services", { params: { path: { orgId: first.id } } }),
    )) as Loaded<K8sService[]>;
    if (!svcRes.ok) return setLoadError(svcRes.error);
    const sRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/sites", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Site[]>;
    setRaw({ clusters: cRes.data, services: svcRes.data, sites: sRes.ok ? sRes.data : [] });
  }, [myId]);
  useEffect(() => {
    reload();
  }, [reload]);

  const gate = k8sGate({ role: myRole, emailVerified });
  const cards: ClusterCard[] = useMemo(() => (raw ? assembleClusters(raw.clusters, raw.services) : []), [raw]);

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Kubernetes</h1>
          <p className="mt-1 text-sm text-slate-400">Expose in-cluster Services to the fabric — clients reach them by a stable name over the tunnel.</p>
        </div>
        {raw && gate.canManage && raw.sites.length > 0 && <Button onClick={() => setRegistering(true)}>Register cluster</Button>}
      </div>

      {loadError && <LoadRetry error={loadError} onRetry={reload} />}

      {raw && !loadError && (
        <>
          {raw.sites.length === 0 && (
            <p className="mt-4 text-sm text-slate-500">Register a site gateway first — a cluster is fronted by one site's gateway.</p>
          )}
          {cards.length === 0 && raw.sites.length > 0 && (
            <p className="mt-4 text-sm text-slate-500">No clusters yet. Register one to start exposing Services.</p>
          )}
          <div className="mt-4 space-y-4">
            {cards.map((c) => (
              <ClusterCardView key={c.id} orgId={orgId ?? ""} card={c} canManage={gate.canManage} onDone={reload} />
            ))}
          </div>
        </>
      )}

      {registering && orgId && raw && (
        <RegisterClusterModal orgId={orgId} sites={raw.sites} onClose={() => setRegistering(false)} onDone={reload} />
      )}
    </div>
  );
}

function ClusterCardView({ orgId, card, canManage, onDone }: { orgId: string; card: ClusterCard; canManage: boolean; onDone: () => void }) {
  const [exposing, setExposing] = useState(false);
  const [deregistering, setDeregistering] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function unexpose(serviceId: string) {
    setErr(null);
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/k8s/services/{serviceId}", {
      params: { path: { orgId, serviceId } },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not unexpose the Service."));
    onDone();
  }

  return (
    <Card>
      <div className="flex items-start justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-200">
            {card.name}
            {card.managedByOperator && <ManagedBadge />}
          </h2>
          <p className="mt-1 text-xs text-slate-500">
            zone <span className="text-slate-400">{card.dnsZone}</span> · VIP range <span className="text-slate-400">{card.vipRange}</span> · Service CIDR{" "}
            <span className="text-slate-400">{card.serviceCidr}</span>
            {card.dnsVip && <> · DNS {card.dnsVip}</>}
          </p>
        </div>
        {canManage && (
          <span className="flex gap-2">
            <Button onClick={() => setExposing(true)}>Expose Service</Button>
            {objectControls(card.managedByOperator).withheld ? (
              // D2 cond 1: refuse the destructive dashboard edit on a GitOps-managed cluster — warn, don't
              // silently revert on the next reconcile. aria-label carries the full guidance (L1).
              <span className="self-center text-xs text-amber-400/90" title={managedEditWarning("cluster")} aria-label={managedEditWarning("cluster")}>
                edit the CR
              </span>
            ) : (
              <Button variant="danger" onClick={() => setDeregistering(true)}>
                Deregister
              </Button>
            )}
          </span>
        )}
      </div>

      <ErrorText>{err}</ErrorText>

      {card.services.length === 0 ? (
        <p className="mt-3 text-xs text-slate-500">No Services exposed in this cluster yet.</p>
      ) : (
        <ul className="mt-3 space-y-1">
          {card.services.map((s) => (
            <li key={s.id} className="flex items-center justify-between rounded-md bg-white/5 px-3 py-2 text-sm">
              <span className="text-slate-200">
                <span className="font-mono text-xs text-slate-300">{s.fqdn}</span>
                <span className="ml-2 text-slate-500">
                  {s.vip} · {s.protocol}/{s.ports}
                </span>
                {s.managedByOperator && <ManagedBadge />}
              </span>
              {canManage &&
                (objectControls(s.managedByOperator).withheld ? (
                  <span className="text-xs text-amber-400/90" title={managedEditWarning("Service")} aria-label={managedEditWarning("Service")}>
                    edit the CR
                  </span>
                ) : (
                  <Button variant="ghost" onClick={() => unexpose(s.id)}>
                    Unexpose
                  </Button>
                ))}
            </li>
          ))}
        </ul>
      )}

      {exposing && <ExposeServiceModal orgId={orgId} clusterId={card.id} onClose={() => setExposing(false)} onDone={onDone} />}
      {deregistering && (
        <DeregisterClusterModal orgId={orgId} card={card} onClose={() => setDeregistering(false)} onDone={onDone} />
      )}
    </Card>
  );
}

function RegisterClusterModal({ orgId, sites, onClose, onDone }: { orgId: string; sites: Site[]; onClose: () => void; onDone: () => void }) {
  const [siteId, setSiteId] = useState(sites[0]?.id ?? "");
  const [name, setName] = useState("");
  const [vipRange, setVipRange] = useState("");
  const [serviceCidr, setServiceCidr] = useState("10.96.0.0/12");
  const [dnsZone, setDnsZone] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.POST("/api/v1/organizations/{orgId}/k8s/clusters", {
      params: { path: { orgId } },
      body: { site_id: siteId, name, vip_range: vipRange, service_cidr: serviceCidr, dns_zone: dnsZone },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not register the cluster."));
    onClose();
    onDone();
  }

  return (
    <Modal
      title="Register a Kubernetes cluster"
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || !siteId || name.trim() === "" || vipRange.trim() === "" || dnsZone.trim() === ""}>
            Register
          </Button>
        </>
      }
    >
      <Field label="Fronting site gateway">
        <Select value={siteId} onChange={(e) => setSiteId(e.target.value)}>
          {sites.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </Select>
      </Field>
      <Field label="Cluster name (a DNS label — part of every Service hostname)">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. prod" autoFocus />
      </Field>
      <Field label="Synthetic VIP range (CIDR — disjoint from your pool, sites, and other clusters)">
        <Input value={vipRange} onChange={(e) => setVipRange(e.target.value)} placeholder="e.g. 100.64.0.0/16" />
      </Field>
      <Field label="Kubernetes Service CIDR (where the cluster's ClusterIPs live)">
        <Input value={serviceCidr} onChange={(e) => setServiceCidr(e.target.value)} placeholder="e.g. 10.96.0.0/12" />
      </Field>
      <Field label="DNS zone (your domain suffix; need not be publicly registered)">
        <Input value={dnsZone} onChange={(e) => setDnsZone(e.target.value)} placeholder="e.g. k8s.acme.com" />
      </Field>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function ExposeServiceModal({ orgId, clusterId, onClose, onDone }: { orgId: string; clusterId: string; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState("");
  const [namespace, setNamespace] = useState("default");
  // WF-K5 M8/M9: an exposure needs a SINGLE specific port + a protocol — the gateway DNATs VIP:port ->
  // podIP:targetPort, so all-ports/ranges are refused server-side. The form must offer the port the refusal
  // teaches the user to supply (offering the refusal without the field would make the dashboard structurally
  // unable to produce a valid exposure). Protocol is tcp/udp (no "any" — a ported DNAT needs an L4 proto).
  const [port, setPort] = useState("");
  const [protocol, setProtocol] = useState<"tcp" | "udp">("tcp");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // Client-side UX validation ONLY — the server's ExposeService is the authoritative validator (one-validator):
  // its typed refusals (service_port_required / service_port_range_unsupported) render verbatim via apiErrorMessage.
  const portNum = Number(port);
  const portValid = Number.isInteger(portNum) && portNum >= 1 && portNum <= 65535;

  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.POST("/api/v1/organizations/{orgId}/k8s/clusters/{clusterId}/services", {
      params: { path: { orgId, clusterId } },
      // Single specific port: port_low == port_high (ranges are refused). Server stays authoritative.
      body: { name, namespace, protocol, port_low: portNum, port_high: portNum },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not expose the Service."));
    onClose();
    onDone();
  }

  return (
    <Modal
      title="Expose a Service"
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={busy || name.trim() === "" || namespace.trim() === "" || !portValid}>
            Expose
          </Button>
        </>
      }
    >
      <Field label="Service name">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. api" autoFocus />
      </Field>
      <Field label="Namespace">
        <Input value={namespace} onChange={(e) => setNamespace(e.target.value)} placeholder="e.g. prod" />
      </Field>
      <Field label="Port">
        <Input
          type="number"
          min={1}
          max={65535}
          value={port}
          onChange={(e) => setPort(e.target.value)}
          placeholder="the Service port clients dial, e.g. 80"
        />
        {port !== "" && !portValid && <p className="mt-1 text-xs text-amber-400">Enter a single port between 1 and 65535.</p>}
      </Field>
      <Field label="Protocol">
        <Select value={protocol} onChange={(e) => setProtocol(e.target.value as "tcp" | "udp")}>
          <option value="tcp">tcp</option>
          <option value="udp">udp</option>
        </Select>
      </Field>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function DeregisterClusterModal({ orgId, card, onClose, onDone }: { orgId: string; card: ClusterCard; onClose: () => void; onDone: () => void }) {
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/k8s/clusters/{clusterId}", {
      params: { path: { orgId, clusterId: card.id } },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not deregister the cluster."));
    onClose();
    onDone();
  }

  return (
    <Modal
      title={`Deregister ${card.name}`}
      onDismiss={onClose}
      actions={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="danger" onClick={submit} disabled={busy || typed !== card.name}>
            Deregister
          </Button>
        </>
      }
    >
      <p className="text-sm text-slate-400">
        This removes the cluster, unexposes all {card.services.length} of its Services, and deletes any rule that reached one. Its VIP range and DNS
        zone are freed for reuse. Type the cluster name <span className="font-mono text-slate-300">{card.name}</span> to confirm.
      </p>
      <div className="mt-3">
        <Field label="Cluster name">
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={card.name} autoFocus />
        </Field>
      </div>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}
