import { useCallback, useEffect, useState } from "react";
import {
  api,
  loadOne,
  type Loaded,
  type Meta,
  type Member,
  type Org,
  type Role,
  type Site,
  type SiteSubnet,
  type Node,
} from "../lib/api";
import { useAuth } from "../lib/auth";
import { Card } from "../components/ui";
import { badgeClass } from "../lib/healthview";
import { roleFromMembers } from "../lib/policyview";
import { assembleTopology, siteGate, sitesView, type GatewayView, type SiteCard } from "../lib/sitesview";

// Sites (S8.3 Slice 2): the read-only topology. Every value on screen traces to a wire field — the
// render-floor law (no animation, nothing implied). Hub designation is backend-derived (node.is_site_hub),
// health is the S7.4b/S8.2 badge, a site's gateways are a LIST (CH). Mutations + the pending queue are
// Slice 3; this slice renders truth and gates by edition/role.

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

export default function Sites() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  const emailVerified = state.status === "authed" && state.user.email_verified;
  const [meta, setMeta] = useState<Meta | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [myRole, setMyRole] = useState<Role | undefined>(undefined);
  const [cards, setCards] = useState<SiteCard[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    setLoadError(null);
    setCards(null);
    const mRes = await loadOne(() => api.GET("/api/v1/meta"));
    if (!mRes.ok) return setLoadError(mRes.error);
    setMeta(mRes.data as Meta);
    const oRes = await loadOne(() => api.GET("/api/v1/organizations"));
    if (!oRes.ok) return setLoadError(oRes.error);
    const first = (oRes.data as Org[])[0];
    if (!first) return setLoadError("You are not a member of any organization yet.");
    setOrg(first);
    // Role (for the manage gate) — only meaningful for enterprise; a member still sees the topology.
    const memRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/members", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Member[]>;
    setMyRole(roleFromMembers(memRes, myId).role);

    if ((mRes.data as Meta).edition !== "enterprise") return; // upsell view; no site fetches
    // The topology join: sites + nodes + per-site subnets. A single failed fetch is a legible retry,
    // never a reassuring empty topology (the S7.4a no-false-empty rule).
    const sRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/sites", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Site[]>;
    if (!sRes.ok) return setLoadError(sRes.error);
    const nRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/nodes", { params: { path: { orgId: first.id } } }),
    )) as Loaded<Node[]>;
    if (!nRes.ok) return setLoadError(nRes.error);
    const subnetsBySite: Record<string, SiteSubnet[]> = {};
    for (const site of sRes.data) {
      const subRes = (await loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/sites/{siteId}/subnets", {
          params: { path: { orgId: first.id, siteId: site.id } },
        }),
      )) as Loaded<SiteSubnet[]>;
      if (!subRes.ok) return setLoadError(subRes.error);
      subnetsBySite[site.id] = subRes.data;
    }
    setCards(assembleTopology(sRes.data, subnetsBySite, nRes.data));
  }, [myId]);
  useEffect(() => {
    reload();
  }, [reload]);

  const gate = siteGate({ role: myRole, emailVerified, edition: meta?.edition });
  const view = sitesView({ editionReady: meta != null && org != null, loadError: loadError != null, isEnterprise: gate.isEnterprise });

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Sites</h1>
      <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>

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

      {view === "body" && cards != null && <Topology cards={cards} canManage={gate.canManage} />}
      {view === "body" && cards == null && <p className="mt-6 text-sm text-slate-500">Loading…</p>}
    </div>
  );
}

function Topology({ cards, canManage }: { cards: SiteCard[]; canManage: boolean }) {
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
        <SiteCardView key={c.id} card={c} />
      ))}
    </div>
  );
}

function SiteCardView({ card }: { card: SiteCard }) {
  return (
    <Card>
      <div className="flex items-baseline justify-between">
        <h2 className="text-sm font-semibold text-white">{card.name}</h2>
        <span className="text-xs text-slate-500">
          {card.gateways.length === 1 ? "1 gateway" : `${card.gateways.length} gateways`}
        </span>
      </div>

      {/* Gateways — a LIST (CH), usually one in v1. A site with no bound gateway is stated, not hidden. */}
      {card.gateways.length === 0 ? (
        <p className="mt-2 text-xs text-slate-500">No gateway bound.</p>
      ) : (
        <ul className="mt-2 space-y-1">
          {card.gateways.map((g) => (
            <GatewayRow key={g.id} g={g} />
          ))}
        </ul>
      )}

      {/* Subnets with their REAL approval state (never assumed approved). */}
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
            </span>
          ))}
        </div>
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
