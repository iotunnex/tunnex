import { useEffect, useState, type ReactNode } from "react";
import { Icon, type IconName } from "../components/Icon";
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
  type PolicyRule,
  type ZeroTrustMode,
} from "../lib/api";
import { relativeAge } from "../lib/format";
import {
  Badge,
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
  const [rulesRes, setRulesRes] = useState<Loaded<PolicyRule[]> | null>(null);
  const [ztRes, setZtRes] = useState<Loaded<ZeroTrustMode> | null>(null);

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
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/policies", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setRulesRes(r as Loaded<PolicyRule[]>));
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/zero-trust-mode", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setZtRes(r as Loaded<ZeroTrustMode>));
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
            const rules = statFrom(rulesRes, (r: PolicyRule[]) => r.length);

            // Sub-lines are QUALIFICATIONS, and each is `null` when there is nothing honest to say. A sub-line
            // is never filler: an unqualified number is a smaller claim than a wrongly-qualified one.
            const degraded = nodesRes?.ok
              ? nodesRes.data.filter((n) => n.policy_degraded).length
              : null;
            const pendingInvites = null; // no endpoint for pending invites — the slot stays empty, not invented
            const siteSub = sitesRes?.ok
              ? sitesRes.data.length === 0
                ? "none configured"
                : `${sitesRes.data.length} in the mesh`
              : null;
            const zeroTrust = ztRes?.ok
              ? ztRes.data.mode === "enforcing"
                ? "enforcing"
                : "not enforced"
              : null;
            const fresh = isFreshOrg(gateways, devices, members);

            return (
              <>
                {/* README: the Overview stat row is `repeat(12,1fr)` gap 12 — SIX cards at span 2.
                    Settled from the SOURCE prototype, not from the README's generic "4-up" sentence (which
                    describes the other screens) and not from the screenshot alone. */}
                <div className="grid grid-cols-12 gap-12">
                  <Stat
                    label="Members"
                    icon="users"
                    value={members}
                    sub={
                      pendingInvites === null
                        ? null
                        : `${pendingInvites} pending invite${pendingInvites === 1 ? "" : "s"}`
                    }
                  />
                  <Stat
                    label="Devices"
                    icon="laptop"
                    value={devices}
                    sub={
                      pending.state === "ok"
                        ? `${pending.value} awaiting approval`
                        : null
                    }
                  />
                  <Stat
                    label="Gateways"
                    icon="server"
                    value={gateways}
                    sub={
                      degraded === null
                        ? null
                        : `${degraded} reporting degraded kinds`
                    }
                  />
                  {/* ⛔ THE HONEST LABEL, FOUNDER-RULED. `online` is derived from LAST-HANDSHAKE RECENCY
                      (S3.6), not a live session. The design labels this "Online Peers" and puts the
                      qualification in the sub-line; the ruling keeps the qualification in the LABEL, which is
                      the safer of two honest compositions. A render-floor violation in a WORD is still one. */}
                  <Stat
                    label="Seen in last 3 min"
                    icon="waves"
                    value={seen}
                    sub="derived from WireGuard handshake liveness"
                    tone={
                      seen.state === "ok" && seen.value > 0 ? "ok" : undefined
                    }
                  />
                  <Stat
                    label="Sites"
                    icon="network"
                    value={sites}
                    sub={siteSub}
                  />
                  <Stat
                    label="Access Rules"
                    icon="shield"
                    value={rules}
                    sub={zeroTrust === null ? null : zeroTrust}
                  />
                </div>

                {fresh && (
                  <Panel title="Get started" className="col-span-12">
                    {/* The floating "Get started" widget is CUT — it becomes this. Rendered only when we KNOW
                        the org is empty: showing it because a fetch failed would tell a founder with a working
                        fleet that they have nothing. */}
                    <ol className="space-y-6 text-explainer leading-[1.55] text-ink-body">
                      <li>
                        1. Enroll a tunnex-node agent to serve WireGuard peers.
                      </li>
                      <li>2. Add your first device and download its config.</li>
                      <li>3. Define who may reach what under Access.</li>
                    </ol>
                    <Link
                      to="/devices"
                      className="mt-10 inline-block text-mono text-ink-emphasis hover:text-ink-heading"
                    >
                      Enroll a gateway →
                    </Link>
                  </Panel>
                )}

                {/* README panel spans on the 12-col base: Peer Connection Status 3 · Gateway Health 3 ·
                    Recent Activity 3. Site-Link Throughput (6), Device Posture, Needs Attention, System
                    Health, Network map, HA Hub Set and Alerts are CUT or DEFERRED — see docs/S14.4. */}
                <div className="grid grid-cols-12 gap-12">
                  <Panel title="Gateway Health" className="col-span-4">
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
                </div>
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
          <div className="grid grid-cols-12 gap-12">
            <Panel title="Peer Connection Status" className="col-span-5">
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
              {/* The design's own caption, kept verbatim — it states the product's rule, not a decoration. */}
              <p className="mt-8 text-explainer leading-[1.55] text-ink-tertiary">
                Status derived from WireGuard handshake liveness — never
                green-while-dead.
              </p>
            </Panel>

            <Panel title="Recent Activity" className="col-span-7">
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
          </div>
        </>
      )}
    </div>
  );
}

/**
 * A stat card — the README's composition, exactly:
 *
 *   [30px icon tile]  LABEL 500 11px
 *   VALUE 700 26px
 *   SUB-LINE 10px
 *
 * ⛔ `value` IS A STATE, NOT A NUMBER. `number` would let a caller write `data?.members ?? 0` — one keystroke,
 * typechecks, looks reasonable, and renders a confident zero for an org whose fetch failed. The three states
 * mean different things: loading (not learned yet) · failed (tried, could not learn) · ok (this is the number).
 *
 * ⛔ THE SUB-LINE IS STRUCTURAL, NOT DECORATION. In the design every card carries one and it holds the
 * QUALIFICATION — "seen in last 3 min", "3 awaiting approval". A card with a bare number states more than it
 * knows; the sub-line is where the number is told what it means.
 */
function Stat({
  label,
  icon,
  value,
  sub,
  tone,
}: {
  label: string;
  icon: IconName;
  value: StatState;
  /** The qualification. `null` when there is nothing honest to say — never filler. */
  sub?: ReactNode;
  tone?: "ok";
}) {
  const text = statText(value);
  return (
    <div className="col-span-2 flex flex-col gap-8 rounded-card border border-line bg-surface p-14 shadow-card backdrop-blur-[24px] backdrop-saturate-[1.4]">
      <div className="flex items-center gap-9">
        <span className="flex h-[30px] w-[30px] shrink-0 items-center justify-center rounded-inset border border-white/[.2] bg-white/[.09] text-ink-emphasis">
          <Icon name={icon} size={15} />
        </span>
        <span className="text-cell font-medium text-ink-secondary">
          {label}
        </span>
      </div>
      {text === null ? (
        <span
          className="text-stat font-bold leading-none text-ink-secondary"
          title={
            value.state === "failed" ? "Could not load this count." : "Loading…"
          }
        >
          {value.state === "failed" ? "—" : "…"}
        </span>
      ) : (
        <span
          className={`text-stat font-bold leading-none ${tone === "ok" ? "text-ok" : "text-ink-heading"}`}
        >
          {text}
        </span>
      )}
      <span className="text-mono font-medium text-ink-tertiary">
        {value.state === "failed" ? (
          <span className="text-danger">could not load</span>
        ) : (
          sub
        )}
      </span>
    </div>
  );
}
