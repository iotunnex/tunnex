import { useEffect, useState } from "react";
import { Donut } from "../components/viz";
import { Link } from "react-router-dom";
import {
  api,
  apiErrorMessage,
  loadOne,
  type Device,
  type Loaded,
  type Node,
  type OrgOverview,
  type Site,
} from "../lib/api";
import { relativeAge } from "../lib/format";
import {
  Badge,
  Card,
  EmptyState,
  ErrorText,
  List,
  ListItem,
  Loading,
  Panel,
} from "../components/ui";
import { policyHealthBadge } from "../lib/healthview";
import {
  isFreshOrg,
  sortGateways,
  statFrom,
  statText,
  type GatewayRow,
  type StatState,
} from "../lib/overviewview";
import { TunnelControl } from "../components/TunnelControl";
import { desktop } from "../lib/desktop";

export default function Dashboard() {
  const [orgName, setOrgName] = useState("");
  const [data, setData] = useState<OrgOverview | null>(null);
  const [error, setError] = useState<string | null>(null);
  // WF-2 (Deck D Leg 10): bump to refetch the overview. The CP count is correct the moment a device is
  // revoked (CountActiveDevicesByOrg excludes it) — the stale number was THIS view's mount-once fetch.
  const [refresh, setRefresh] = useState(0);
  // S14.4: the six stat cards come from THREE endpoints and RESOLVE INDEPENDENTLY.
  //
  // `/overview` supplies four (members, devices, nodes, online). Sites and Pending approvals are not in that
  // response. An aggregate field was REFUSED deliberately: an API change driven by a layout converts three
  // independent failures into one blast radius — one failure would blank six cards instead of two. Screens
  // compose endpoints; endpoints do not compose themselves for screens.
  const [sitesRes, setSitesRes] = useState<Loaded<Site[]> | null>(null);
  const [pendingRes, setPendingRes] = useState<Loaded<Device[]> | null>(null);
  const [nodesRes, setNodesRes] = useState<Loaded<Node[]> | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const { data: orgs, error: orgErr } = await api.GET(
          "/api/v1/organizations",
        );
        if (cancelled) return;
        if (orgErr)
          return setError(
            apiErrorMessage(orgErr, "Could not load your organizations."),
          );
        const org = orgs?.[0];
        if (!org)
          return setError("You are not a member of any organization yet.");
        setOrgName(org.name);
        const { data: ov, error: ovErr } = await api.GET(
          "/api/v1/organizations/{orgId}/overview",
          {
            params: { path: { orgId: org.id } },
          },
        );
        if (cancelled) return;
        if (ovErr || !ov)
          return setError(
            apiErrorMessage(ovErr, "Could not load the overview."),
          );
        setData(ov);
        // Fired together, awaited independently: each sets its own state, so one failure degrades one card.
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/sites", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setSitesRes(r as Loaded<Site[]>));
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/devices/pending", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setPendingRes(r as Loaded<Device[]>));
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/nodes", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setNodesRes(r as Loaded<Node[]>));
      } catch {
        if (!cancelled) setError("Could not reach the API.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [refresh]);

  // WF-2: desktop only — the refetch rides the RevocationMonitor's EXISTING signal (the same status
  // event that flips TunnelControl's banner). On the transition EDGE into "revoked", re-pull the
  // overview so the first number an admin sees stops counting the device the server just swept.
  // Browser build: desktop() is null → no subscription, zero web-tier change.
  useEffect(() => {
    const d = desktop();
    if (!d) return;
    let prev = "";
    return d.tunnel.onStatusChanged((s) => {
      if (s.state === "revoked" && prev !== "revoked") setRefresh((r) => r + 1);
      prev = s.state;
    });
  }, []);

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Overview</h1>
      <p className="text-sm text-slate-400">{orgName || "…"}</p>
      <ErrorText>{error}</ErrorText>

      {/* Desktop only: the VPN connect surface (no-op/hidden in the browser). */}
      <TunnelControl />

      {data && (
        <>
          {(() => {
            // The six cards, each carrying its OWN state. `statFrom(null, …)` = still loading.
            const members = statFrom<OrgOverview>(
              { ok: true, data },
              (d) => d.members,
            );
            const devices = statFrom<OrgOverview>(
              { ok: true, data },
              (d) => d.devices,
            );
            const gateways = statFrom<OrgOverview>(
              { ok: true, data },
              (d) => d.nodes,
            );
            const seen = statFrom<OrgOverview>(
              { ok: true, data },
              (d) => d.online,
            );
            const sites = statFrom(sitesRes, (r: Site[]) => r.length);
            const pending = statFrom(pendingRes, (r: Device[]) => r.length);
            const fresh = isFreshOrg(gateways, devices, members);

            return (
              <>
                <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-3 lg:grid-cols-6">
                  <Stat label="Members" value={members} />
                  <Stat label="Devices" value={devices} />
                  <Stat label="Gateways" value={gateways} />
                  {/* ⛔ THE HONEST LABEL, RULED. `online` is derived from LAST-HANDSHAKE RECENCY (S3.6), not
                      from a live session. The wireframe says "Online Peers" — and its OWN caption says
                      "never green-while-dead", so the artifact contradicts itself. A render-floor violation
                      in a WORD is still a render-floor violation, and a label is a claim with no chart to
                      inspect, which makes it the easiest kind to ship (docs/laws.md). */}
                  <Stat
                    label="Seen in last 3 min"
                    value={seen}
                    tone={
                      seen.state === "ok" && seen.value > 0 ? "ok" : undefined
                    }
                  />
                  <Stat label="Sites" value={sites} />
                  <Stat label="Pending approvals" value={pending} />
                </div>

                {fresh && (
                  <Card className="mt-4">
                    {/* The floating "Get started" widget is CUT — it becomes this. Rendered only when we KNOW
                        the org is empty: showing it because a fetch failed would tell a founder with a working
                        fleet that they have nothing. */}
                    <h2 className="text-sm font-semibold text-slate-300">
                      Get started
                    </h2>
                    <ol className="mt-2 space-y-1 text-xs text-slate-400">
                      <li>
                        1. Enroll a tunnex-node agent to serve WireGuard peers.
                      </li>
                      <li>2. Add your first device and download its config.</li>
                      <li>3. Define who may reach what under Access.</li>
                    </ol>
                    <Link
                      to="/devices"
                      className="mt-3 inline-block text-xs text-accent-400 hover:text-accent-500"
                    >
                      Enroll a gateway →
                    </Link>
                  </Card>
                )}

                <Panel title="Gateway health" className="mt-4">
                  {nodesRes === null ? (
                    <Loading />
                  ) : !nodesRes.ok ? (
                    <ErrorText>Gateway health is unavailable.</ErrorText>
                  ) : nodesRes.data.length === 0 ? (
                    <EmptyState>No gateway enrolled yet.</EmptyState>
                  ) : (
                    <List label="Gateway health">
                      {/* ONE interpreter for the 14-value policy_degraded_kind enum: policyHealthBadge, the
                          existing tested view-model. A second copy of the health vocabulary is how the two
                          drift, and the drift would be invisible because both would still render. */}
                      {sortGateways(
                        nodesRes.data.map((n): GatewayRow => {
                          const b = policyHealthBadge(n);
                          return {
                            id: n.id,
                            name: n.name,
                            label: b ? b.label : "healthy",
                            tone: b
                              ? b.tone === "unknown"
                                ? "neutral"
                                : b.tone
                              : "ok",
                          };
                        }),
                      ).map((g) => (
                        <ListItem key={g.id}>
                          <span className="flex items-center justify-between">
                            <span className="text-sm text-slate-200">
                              {g.name}
                            </span>
                            <Badge tone={g.tone}>{g.label}</Badge>
                          </span>
                        </ListItem>
                      ))}
                    </List>
                  )}
                </Panel>
              </>
            );
          })()}

          {/* S14.3 slice C — the gateway liveness donut. THE FIRST VIZ CONSUMER, wired in the same slice as
              the primitive, because a viz primitive with no consumer is dormant machinery and this epic has
              already ruled that dormant machinery cannot be proven.

              SOURCE: /api/v1/organizations/{orgId}/overview — a CURRENT-STATE PROPORTION, which is the only
              reading this data permits. It is deliberately NOT a trend: there is no time-series endpoint in
              the API, and `rx_bytes`/`tx_bytes` are described by the spec itself as "raw gauge since the last
              handshake (display only, never summed as monotonic)". The field exists and its own description
              forbids the chart — which is why the Overview area chart is ROADMAP, not built.

              `failed` carries the load state so a failed fetch draws NOTHING — never a zero baseline, which
              would render "we could not read it" identically to "nothing is online". */}
          <Card className="mt-4">
            <h2 className="text-sm font-semibold text-slate-300">
              Gateway liveness
            </h2>
            <div className="mt-3">
              <Donut
                label="Gateway liveness"
                source={{ endpoint: "/api/v1/organizations/{orgId}/overview" }}
                failed={error != null}
                slices={[
                  {
                    label: "seen in last 3 min",
                    value: data.online,
                    tone: "ok",
                  },
                  {
                    label: "not seen recently",
                    value: Math.max(0, data.nodes - data.online),
                    tone: "neutral",
                  },
                ]}
                empty="No gateways enrolled yet."
              />
            </div>
          </Card>

          {/* The two per-condition onboarding cards were FOLDED INTO the single "Get started" panel above.
              Three separate empty states on one screen said the same thing three ways and, on a fresh org,
              stacked into a column of apologies. One panel, rendered only when the org is KNOWN to be empty. */}

          <Panel title="Recent activity" className="mt-4">
            {data.recent_activity.length === 0 ? (
              <EmptyState>No activity yet.</EmptyState>
            ) : (
              <List label="Recent activity">
                {data.recent_activity.map((a, i) => (
                  <ListItem key={i}>
                    <span className="flex items-center justify-between">
                      <span className="text-sm text-slate-300">
                        <span className="font-mono text-xs text-slate-400">
                          {a.action}
                        </span>
                        {a.target_type && (
                          <span className="ml-2 text-xs text-slate-500">
                            {a.target_type}
                          </span>
                        )}
                      </span>
                      <span className="text-xs text-slate-500">
                        {relativeAge(a.created_at)}
                      </span>
                    </span>
                  </ListItem>
                ))}
              </List>
            )}
          </Panel>
        </>
      )}
    </div>
  );
}

/**
 * A stat card.
 *
 * ⛔ `value` IS A STATE, NOT A NUMBER, and that is the whole point of this slice. `number` would let a caller
 * write `data?.members ?? 0` — one keystroke, typechecks, looks reasonable, and renders a confident zero for
 * an org whose fetch failed. The three states are distinct because they mean different things:
 *
 *   loading  -> we have not learned this yet
 *   failed   -> we tried and could not learn it
 *   ok       -> this is the number
 *
 * A failed card says UNAVAILABLE. It never says 0, and it is never silently blank either — a blank card in a
 * row of six reads as a rendering bug, which sends the reader looking in the wrong place.
 */
function Stat({
  label,
  value,
  tone,
}: {
  label: string;
  value: StatState;
  tone?: "ok";
}) {
  const text = statText(value);
  return (
    <div className="rounded-xl border border-white/5 bg-ink-800 p-4">
      {text === null ? (
        <div
          className="font-mono text-2xl font-semibold text-slate-600"
          title={
            value.state === "failed" ? "Could not load this count." : "Loading…"
          }
        >
          {value.state === "failed" ? "—" : "…"}
        </div>
      ) : (
        <div
          className={`font-mono text-2xl font-semibold ${tone === "ok" ? "text-ok" : "text-white"}`}
        >
          {text}
        </div>
      )}
      <div className="mt-1 text-xs text-slate-500">
        {label}
        {value.state === "failed" && (
          <span className="ml-1 text-danger">unavailable</span>
        )}
      </div>
    </div>
  );
}
