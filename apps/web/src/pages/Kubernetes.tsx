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
import {
  Button,
  Card,
  ErrorText,
  Field,
  Input,
  Modal,
  Select,
} from "../components/ui";
import { LoadRetry } from "../components/LoadRetry";
import { roleFromMembers } from "../lib/policyview";
import {
  assembleClusters,
  clusterReachability,
  k8sGate,
  managedEditWarning,
  objectControls,
  serviceRowClass,
  statTiles,
  type ClusterCard,
} from "../lib/k8sview";
// ⛔ EXPLICIT IMPORT, and it is load-bearing: without it `Node` resolves to the DOM's global `Node`, so
// `site_id` and `policy_degraded_kind` "do not exist" with no hint that a different type was found.
import type { Node } from "../lib/api";
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
  // D9: gateways, for the reachability qualification. A cluster's Services must not read as reachable when a
  // gateway fronting its site has no endpoint view.
  nodes: Node[];
  // NULL = the read failed. Distinct from 0, which means "we looked and there are none".
  machineCreds: number | null;
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
    if (!first)
      return setLoadError("You are not a member of any organization yet.");
    setOrgId(first.id);
    const memRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/members", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Member[]>;
    setMyRole(roleFromMembers(memRes, myId).role);
    const cRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/k8s/clusters", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<K8sCluster[]>;
    if (!cRes.ok) return setLoadError(cRes.error);
    const svcRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/k8s/services", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<K8sService[]>;
    if (!svcRes.ok) return setLoadError(svcRes.error);
    const sRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/sites", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Site[]>;
    // ⛔ TWO SECOND-CLASS READS. Both enrich a screen that is already correct, so a failure degrades a cell
    // rather than blanking the page — and `null` is carried through rather than collapsed to 0/[].
    const nRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/nodes", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Node[]>;
    const mcRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/machine-credentials", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<unknown[]>;
    setRaw({
      clusters: cRes.data,
      services: svcRes.data,
      sites: sRes.ok ? sRes.data : [],
      nodes: nRes.ok ? nRes.data : [],
      // NULL, not 0 — "we could not look" is a different fact from "there are none", and the tile says which.
      machineCreds: mcRes.ok ? mcRes.data.length : null,
    });
  }, [myId]);
  useEffect(() => {
    reload();
  }, [reload]);

  const gate = k8sGate({ role: myRole, emailVerified });
  const cards: ClusterCard[] = useMemo(
    () => (raw ? assembleClusters(raw.clusters, raw.services) : []),
    [raw],
  );
  const siteName = useMemo(
    () => new Map((raw?.sites ?? []).map((x) => [x.id, x.name])),
    [raw],
  );
  // D9 inputs: which gateways front which site, and whether each has an endpoint view.
  const gateways = useMemo(
    () =>
      (raw?.nodes ?? []).map((n: Node) => ({
        siteId: n.site_id ?? null,
        endpointsUnavailable:
          n.policy_degraded_kind === "k8s_endpoints_unavailable",
      })),
    [raw],
  );
  const tiles = useMemo(
    () => statTiles(cards, raw?.machineCreds ?? null),
    [cards, raw],
  );

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Kubernetes</h1>
          <p className="mt-1 text-sm text-slate-400">
            Clusters, exposed Services and the VIPs clients reach them at. A
            Service is reached by name over the tunnel, never by its ClusterIP.
          </p>
        </div>
        {raw && gate.canManage && raw.sites.length > 0 && (
          <Button onClick={() => setRegistering(true)}>Register cluster</Button>
        )}
      </div>

      {loadError && <LoadRetry error={loadError} onRetry={reload} />}

      {raw && !loadError && (
        <>
          {raw.sites.length === 0 && (
            <p className="mt-4 text-sm text-slate-500">
              Register a site gateway first — a cluster is fronted by one site's
              gateway.
            </p>
          )}
          {cards.length === 0 && raw.sites.length > 0 && (
            <p className="mt-4 text-sm text-slate-500">
              No clusters yet. Register one to start exposing Services.
            </p>
          )}
          {cards.length > 0 && (
            <ul className="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-3">
              {tiles.map((t: { label: string; value: number | null; hint: string }) => (
                <li
                  key={t.label}
                  className="rounded-card border border-line bg-surface px-3.5 py-3"
                >
                  <p className="text-micro uppercase tracking-wide text-ink-tertiary">
                    {t.label}
                  </p>
                  <p className="mt-1 text-[22px] font-semibold text-ink-heading">
                    {/* ⛔ A NULL VALUE RENDERS AS A DASH, NEVER AS 0. A zero standing in for "we could not
                        look" is the reassuring-empty defect in numeric form. */}
                    {/* A LONE DASH IS A NULL MARKER, NOT PROSE, so the em-dash sweep leaves it: it is the
                        clearest "no value" glyph in a numeric slot, and the hint beneath says WHY. */}
                    {t.value === null ? "—" : t.value}
                  </p>
                  <p className="text-micro text-ink-faint">{t.hint}</p>
                </li>
              ))}
            </ul>
          )}

          <div className="mt-4 space-y-4">
            {cards.map((c) => (
              <ClusterCardView
                key={c.id}
                orgId={orgId ?? ""}
                card={c}
                canManage={gate.canManage}
                onDone={reload}
                siteName={siteName.get(c.siteId) ?? null}
                reach={clusterReachability({ siteId: c.siteId, gateways })}
              />
            ))}
          </div>

          {cards.length > 0 && (
            <div className="mt-4 grid grid-cols-1 items-start gap-3 lg:grid-cols-2">
              <Card>
                <h2 className="text-sm font-semibold text-ink-heading">
                  How a client reaches a Service
                </h2>
                {/* STATIC COPY, and its content is verified against the code rather than copied from the
                    wireframe: the DNAT target is a READY POD (dnat_linux.go), and the grant matches
                    `ct original ip daddr` so enforcement keys the PRE-DNAT VIP. */}
                <ol className="mt-2 space-y-1.5 text-cell text-ink-body">
                  <li>1. The client resolves the FQDN at the cluster&rsquo;s reserved DNS VIP.</li>
                  <li>2. It connects to the Service&rsquo;s synthetic VIP.</li>
                  <li>3. The gateway DNATs that VIP to a ready pod endpoint.</li>
                </ol>
                <p className="mt-2 text-micro text-ink-tertiary">
                  Not a ClusterIP DNAT. netfilter applies one destination NAT per
                  prerouting pass, so kube-proxy&rsquo;s ClusterIP rule would be a
                  no-op after ours and the packet would die addressed to the
                  ClusterIP. The gateway targets a ready pod directly, fed by an
                  EndpointSlice watch, and fails closed on every fault.
                </p>
                <p className="text-micro text-ink-faint">
                  Enforcement keys the pre-DNAT VIP: the grant matches the
                  original destination, so a broad rule cannot slip past and a
                  bare destination match cannot miss the post-DNAT pod IP.
                </p>
              </Card>

              <Card>
                <h2 className="text-sm font-semibold text-ink-heading">
                  Refusals this surface reports verbatim
                </h2>
                <dl className="mt-2 space-y-2 text-micro">
                  <div>
                    <dt className="font-mono text-ink-body">409 vip_range_overlap</dt>
                    <dd className="text-ink-tertiary">
                      A VIP range must be disjoint from the device pool, every
                      site subnet, and other clusters&rsquo; ranges. Disjointness
                      is an org-wide fact, so the control plane owns it.
                    </dd>
                  </div>
                  <div>
                    <dt className="font-mono text-ink-body">409 vip_range_exhausted</dt>
                    <dd className="text-ink-tertiary">
                      No address left to allocate. Unexposing frees a VIP for
                      immediate reuse.
                    </dd>
                  </div>
                  <div>
                    <dt className="font-mono text-ink-body">409 service_exists</dt>
                    <dd className="text-ink-tertiary">
                      That namespace and name pair is already exposed: one stable
                      identity per Service.
                    </dd>
                  </div>
                </dl>
              </Card>

              <Card>
                <h2 className="text-sm font-semibold text-ink-heading">
                  Installing the operator
                </h2>
                {/* ⛔ NAMED AS COPY, NOT A CAPABILITY. This screen installs nothing; these are commands a human
                    runs elsewhere, and implying otherwise would be a control that does not exist. */}
                <p className="mt-1 text-micro text-ink-tertiary">
                  Reference only. Run these yourself; this screen does not
                  install anything.
                </p>
                <pre className="mt-2 overflow-x-auto rounded-input border border-line bg-surface-inset p-2.5 text-micro text-ink-body">
{`helm install gw tunnex/tunnex-gateway \
  --set joinToken.secretRef=tunnex-join
helm install op tunnex/operator \
  --set machineToken.secretRef=tunnex-machine`}
                </pre>
                <p className="mt-1 text-micro text-ink-faint">
                  Both secrets are one-time ceremonies you create, never chart
                  values. The gateway pod runs with a read-only role on services
                  and endpointslices: it cannot read Secrets, write, or escalate.
                </p>
              </Card>

              <Card>
                <h2 className="text-sm font-semibold text-ink-heading">
                  Not shown, and why
                </h2>
                {/* Absence recorded is a decision; absence unrecorded gets re-proposed at the next review. */}
                <ul className="mt-2 space-y-1.5 text-micro text-ink-tertiary">
                  <li>
                    <strong>The GitOps CR panel.</strong> Reconcile time,
                    per-kind ready counts and refused grants are not fields we
                    serve, and neither is the operator&rsquo;s version. Every
                    value on it would be invented.
                  </li>
                  <li>
                    <strong>A per-Service ready state.</strong> The agent does
                    watch endpoints, so readiness is observed, but it is not
                    reported per Service. The node-level view is on Gateways.
                  </li>
                  <li>
                    <strong>A state column.</strong> The API returns live
                    Services only, so the column would carry one value forever. A
                    grant pointing at a removed Service is flagged on Access
                    Policies, which is where that fact is served.
                  </li>
                </ul>
              </Card>
            </div>
          )}
        </>
      )}

      {registering && orgId && raw && (
        <RegisterClusterModal
          orgId={orgId}
          sites={raw.sites}
          onClose={() => setRegistering(false)}
          onDone={reload}
        />
      )}
    </div>
  );
}

function ClusterCardView({
  orgId,
  card,
  canManage,
  onDone,
  siteName,
  reach,
}: {
  orgId: string;
  card: ClusterCard;
  canManage: boolean;
  onDone: () => void;
  /** FRONTED BY. Null when the sites read failed — a courtesy, never a reason to blank the card. */
  siteName: string | null;
  reach: { reachable: boolean; why: string | null };
}) {
  const [exposing, setExposing] = useState(false);
  const [deregistering, setDeregistering] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function unexpose(serviceId: string) {
    setErr(null);
    const { error } = await api.DELETE(
      "/api/v1/organizations/{orgId}/k8s/services/{serviceId}",
      {
        params: { path: { orgId, serviceId } },
      },
    );
    if (error)
      return setErr(apiErrorMessage(error, "Could not unexpose the Service."));
    onDone();
  }

  return (
    <Card>
      {/* ⛔ D9 — REACHABILITY STATED, NOT INFERRED FROM STYLING ALONE. Without this line a reader sees dimmed
          rows and concludes something about the SERVICES; the fact is about the gateway that serves them. The
          copy never says "the cluster is down": the kind is also true for an RBAC denial and an unsynced
          watch, and naming a cause we cannot know would be the reassuring-comment defect in user copy. */}
      {!reach.reachable && reach.why !== null && (
        <p className="mb-2 text-micro text-warn">{reach.why}</p>
      )}
      <div className="flex items-start justify-between">
        <div>
          <h2 className="text-sm font-semibold text-slate-200">
            {card.name}
            {card.managedByOperator && <ManagedBadge />}
          </h2>
          <p className="mt-1 text-xs text-slate-500">
            {/* FRONTED BY — the column the handoff leads the cluster table with, and the link between this
                screen and Sites. Absent (not "unknown") when the sites read failed. */}
            {siteName !== null && (
              <>
                site <span className="text-slate-400">{siteName}</span> ·{" "}
              </>
            )}
            {card.dnsVip !== null && (
              <>
                DNS VIP{" "}
                <span className="font-mono text-slate-400">{card.dnsVip}</span>{" "}
                (reserved, never handed to a Service) ·{" "}
              </>
            )}
            zone <span className="text-slate-400">{card.dnsZone}</span> · VIP
            range <span className="text-slate-400">{card.vipRange}</span> ·
            Service CIDR{" "}
            <span className="text-slate-400">{card.serviceCidr}</span>
            {/* The DNS VIP is printed ONCE, at the head of this line, WITH its reservation reason. The
                second copy that used to sit here said the same address with less meaning. */}
          </p>
        </div>
        {canManage && (
          <span className="flex gap-2">
            <Button onClick={() => setExposing(true)}>Expose Service</Button>
            {objectControls(card.managedByOperator).withheld ? (
              // D2 cond 1: refuse the destructive dashboard edit on a GitOps-managed cluster — warn, don't
              // silently revert on the next reconcile. aria-label carries the full guidance (L1).
              <span
                className="self-center text-xs text-amber-400/90"
                title={managedEditWarning("cluster")}
                aria-label={managedEditWarning("cluster")}
              >
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
        /* ⛔ N=1 CLUSTER WITH ZERO SERVICES IS ITS OWN STATE, AND IT IS NOT A FAULT. A registered cluster with
           nothing exposed is a working cluster, so the copy names the next action rather than reading as
           something being broken. */
        <p className="mt-3 text-xs text-slate-500">
          No Services exposed yet. Exposing one allocates a VIP from{" "}
          {card.vipRange} and gives it a name clients can reach.
        </p>
      ) : (
        <ul className="mt-3 space-y-1">
          {card.services.map((s) => (
            <li
              key={s.id}
              /* Recession is the honest encoding for a degraded state: an unreachable cluster's rows recede
                 rather than disappearing, because the Services DO exist — they just cannot be reached. */
              className={`flex items-center justify-between rounded-md bg-white/5 px-3 py-2 text-sm ${serviceRowClass(reach.reachable)}`}
            >
              <span className="text-slate-200">
                <span className="font-mono text-xs text-slate-300">
                  {s.fqdn}
                </span>
                <span className="ml-2 text-slate-500">
                  {s.vip} · {s.protocol}/{s.ports}
                </span>
                {s.managedByOperator && <ManagedBadge />}
              </span>
              {canManage &&
                (objectControls(s.managedByOperator).withheld ? (
                  <span
                    className="text-xs text-amber-400/90"
                    title={managedEditWarning("Service")}
                    aria-label={managedEditWarning("Service")}
                  >
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

      {exposing && (
        <ExposeServiceModal
          orgId={orgId}
          clusterId={card.id}
          onClose={() => setExposing(false)}
          onDone={onDone}
        />
      )}
      {deregistering && (
        <DeregisterClusterModal
          orgId={orgId}
          card={card}
          onClose={() => setDeregistering(false)}
          onDone={onDone}
        />
      )}
    </Card>
  );
}

function RegisterClusterModal({
  orgId,
  sites,
  onClose,
  onDone,
}: {
  orgId: string;
  sites: Site[];
  onClose: () => void;
  onDone: () => void;
}) {
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
    const { error } = await api.POST(
      "/api/v1/organizations/{orgId}/k8s/clusters",
      {
        params: { path: { orgId } },
        body: {
          site_id: siteId,
          name,
          vip_range: vipRange,
          service_cidr: serviceCidr,
          dns_zone: dnsZone,
        },
      },
    );
    setBusy(false);
    if (error)
      return setErr(apiErrorMessage(error, "Could not register the cluster."));
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
          <Button
            onClick={submit}
            disabled={
              busy ||
              !siteId ||
              name.trim() === "" ||
              vipRange.trim() === "" ||
              dnsZone.trim() === ""
            }
          >
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
      <Field label="Cluster name (a DNS label: it becomes part of every Service hostname)">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. prod"
          autoFocus
        />
      </Field>
      <Field label="Synthetic VIP range (CIDR, disjoint from your pool, your sites, and other clusters)">
        <Input
          value={vipRange}
          onChange={(e) => setVipRange(e.target.value)}
          placeholder="e.g. 100.64.0.0/16"
        />
      </Field>
      <Field label="Kubernetes Service CIDR (where the cluster's ClusterIPs live)">
        <Input
          value={serviceCidr}
          onChange={(e) => setServiceCidr(e.target.value)}
          placeholder="e.g. 10.96.0.0/12"
        />
      </Field>
      <Field label="DNS zone (your domain suffix; need not be publicly registered)">
        <Input
          value={dnsZone}
          onChange={(e) => setDnsZone(e.target.value)}
          placeholder="e.g. k8s.acme.com"
        />
      </Field>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function ExposeServiceModal({
  orgId,
  clusterId,
  onClose,
  onDone,
}: {
  orgId: string;
  clusterId: string;
  onClose: () => void;
  onDone: () => void;
}) {
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
  const portValid =
    Number.isInteger(portNum) && portNum >= 1 && portNum <= 65535;

  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.POST(
      "/api/v1/organizations/{orgId}/k8s/clusters/{clusterId}/services",
      {
        params: { path: { orgId, clusterId } },
        // Single specific port: port_low == port_high (ranges are refused). Server stays authoritative.
        body: {
          name,
          namespace,
          protocol,
          port_low: portNum,
          port_high: portNum,
        },
      },
    );
    setBusy(false);
    if (error)
      return setErr(apiErrorMessage(error, "Could not expose the Service."));
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
          <Button
            onClick={submit}
            disabled={
              busy ||
              name.trim() === "" ||
              namespace.trim() === "" ||
              !portValid
            }
          >
            Expose
          </Button>
        </>
      }
    >
      <Field label="Service name">
        <Input
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="e.g. api"
          autoFocus
        />
      </Field>
      <Field label="Namespace">
        <Input
          value={namespace}
          onChange={(e) => setNamespace(e.target.value)}
          placeholder="e.g. prod"
        />
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
        {port !== "" && !portValid && (
          <p className="mt-1 text-xs text-amber-400">
            Enter a single port between 1 and 65535.
          </p>
        )}
      </Field>
      <Field label="Protocol">
        <Select
          value={protocol}
          onChange={(e) => setProtocol(e.target.value as "tcp" | "udp")}
        >
          <option value="tcp">tcp</option>
          <option value="udp">udp</option>
        </Select>
      </Field>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}

function DeregisterClusterModal({
  orgId,
  card,
  onClose,
  onDone,
}: {
  orgId: string;
  card: ClusterCard;
  onClose: () => void;
  onDone: () => void;
}) {
  const [typed, setTyped] = useState("");
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    setErr(null);
    const { error } = await api.DELETE(
      "/api/v1/organizations/{orgId}/k8s/clusters/{clusterId}",
      {
        params: { path: { orgId, clusterId: card.id } },
      },
    );
    setBusy(false);
    if (error)
      return setErr(
        apiErrorMessage(error, "Could not deregister the cluster."),
      );
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
          <Button
            variant="danger"
            onClick={submit}
            disabled={busy || typed !== card.name}
          >
            Deregister
          </Button>
        </>
      }
    >
      <p className="text-sm text-slate-400">
        This removes the cluster, unexposes all {card.services.length} of its
        Services, and deletes any rule that reached one. Its VIP range and DNS
        zone are freed for reuse. Type the cluster name{" "}
        <span className="font-mono text-slate-300">{card.name}</span> to
        confirm.
      </p>
      <div className="mt-3">
        <Field label="Cluster name">
          <Input
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
            placeholder={card.name}
            autoFocus
          />
        </Field>
      </div>
      <ErrorText>{err}</ErrorText>
    </Modal>
  );
}
