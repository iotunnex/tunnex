import { useEffect, useState, type ReactNode } from "react";
import { Icon, type IconName } from "../components/Icon";
import { GLASS } from "../components/ui";
import { isEnterprise, type Edition } from "../lib/edition";
import { HealthStatus } from "../components/HealthStatus";
import { formatBytes, hubSetView } from "../lib/hubsetview";
import { assembleTopology } from "../lib/sitesview";
import { Donut, NodeLink } from "../components/viz";
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
  type Meta,
  type HubSet,
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
  linkTraffic,
  peerSlices,
  postureSplit,
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
  const [devicesRes, setDevicesRes] = useState<Loaded<Device[]> | null>(null);
  const [hubSetRes, setHubSetRes] = useState<Loaded<HubSet> | null>(null);
  const [ztRes, setZtRes] = useState<Loaded<ZeroTrustMode> | null>(null);
  // THE ONE GATING SEAM. `/meta`'s edition is the same value that decides whether every other enterprise
  // surface exists — read here, never re-derived from an error.
  const [edition, setEdition] = useState<Edition>("unknown");

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

        // ⛔ THE SEAM, AND IT DECIDES BEFORE IT FETCHES. Edition first; gated endpoints are called ONLY when
        // the edition has them. An open-edition org therefore never issues a request that can 403, so there
        // is no failure to mis-interpret — the render decision is taken at the seam rather than recovered
        // from an error at the call site. The interpretation is what drifted, twice.
        const metaRes = (await loadOne(() =>
          api.GET("/api/v1/meta"),
        )) as Loaded<Meta>;
        if (cancelled) return;
        const ed: Edition = metaRes.ok
          ? metaRes.data.edition === "enterprise"
            ? "enterprise"
            : "open"
          : "unknown";
        setEdition(ed);

        if (isEnterprise(ed)) {
          void loadOne(() =>
            api.GET("/api/v1/organizations/{orgId}/devices/pending", {
              params: { path: { orgId: org.id } },
            }),
          ).then((r) => !cancelled && setPendingRes(r as Loaded<Device[]>));
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
        }
        // Fired together, awaited independently: each sets its own state, so one failure degrades one card.
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/sites", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setSitesRes(r as Loaded<Site[]>));
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/nodes", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setNodesRes(r as Loaded<Node[]>));
        // Both OPEN endpoints — no gate needed, and the audit that cut them was wrong about the data.
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/devices", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setDevicesRes(r as Loaded<Device[]>));
        void loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/hub-set", {
            params: { path: { orgId: org.id } },
          }),
        ).then((r) => !cancelled && setHubSetRes(r as Loaded<HubSet>));
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
    // ⛔ THE PAGE ROOT CARRIES THE RHYTHM. This was a bare `<div>`, and every section inside it stacked with
    // ZERO spacing — the stat row touched Get started, which touched the panel row.
    //
    // The shell's `<main>` already sets `flex flex-col gap-3.5` (the README's page-body rhythm), but a flex gap
    // reaches only DIRECT children, and the whole page is a single child of main. The gap was correct and
    // applied to exactly one element. Every screen root must therefore repeat this — see docs/S14.4.
    <div className="flex flex-col gap-3.5">
      {/* README: PAGE HEADER = title + subtitle, its own block above the body. */}
      <div>
        <h1 className="text-[22px] font-semibold leading-tight text-ink-heading">
          Overview
        </h1>
        <p className="mt-1 text-cell text-ink-secondary">{orgName || "…"}</p>
      </div>
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
            // Edition is UNKNOWN until /meta answers; treat unknown as not-enterprise so a slow load never
            // flashes an enterprise-only surface. Absent-until-known, same rule as every count on this screen.
            const enterprise = isEnterprise(edition);

            // NEEDS ATTENTION is COMPOSED, not fetched — every item names the source that produced it, and an
            // item appears only when its source has been READ. A source still loading contributes nothing;
            // a source that FAILED contributes nothing either, because "nothing needs attention" and "we could
            // not check" must not render identically. The panel says "loading" until every source has answered.
            // ⛔ WAIT ONLY ON SOURCES THAT WILL ACTUALLY ARRIVE.
            //
            // `pendingRes` is enterprise-gated and is NEVER FETCHED on the open edition, so it stays `null`
            // forever — and `null` was the "still loading" signal. The panel hung on "Loading…" permanently,
            // waiting for a request that was deliberately never made.
            //
            // A DELIBERATE NON-FETCH AND AN IN-FLIGHT FETCH ARE THE SAME `null`, and that ambiguity was
            // created by the edition gate two hours after the gate was added. The fix is to ask each source
            // whether it is EXPECTED, not merely whether it has answered.
            const sources = enterprise
              ? ([nodesRes, pendingRes] as const)
              : ([nodesRes] as const);
            const attention: Array<{
              key: string;
              text: string;
              to: string;
            }> | null = sources.some((r) => r === null)
              ? null
              : [
                  ...(nodesRes?.ok
                    ? nodesRes.data
                        .filter((n) => n.policy_degraded)
                        .map((n) => ({
                          key: `gw-${n.id}`,
                          text: `${n.name}: ${policyHealthBadge(n)?.label ?? "degraded"}`,
                          to: "/sites",
                        }))
                    : []),
                  ...(enterprise && pendingRes?.ok && pendingRes.data.length > 0
                    ? [
                        {
                          key: "pending-devices",
                          text: `${pendingRes.data.length} device${pendingRes.data.length === 1 ? "" : "s"} awaiting approval`,
                          to: "/devices",
                        },
                      ]
                    : []),
                ];

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
              // The same reason, one level down: these three sections are siblings and need the page rhythm
              // between them, not zero.
              <div className="flex flex-col gap-3.5">
                {/* README: the Overview stat row is `repeat(12,1fr)` gap 12 — SIX cards at span 2.
                    Settled from the SOURCE prototype, not from the README's generic "4-up" sentence (which
                    describes the other screens) and not from the screenshot alone. */}
                {/* ⛔ THE ROW RE-SPANS TO FILL 12. RULED, and the argument matters more than the choice:
                    a fixed six-slot row leaves the open edition with five cards and a two-column hole, which
                    is the SAME ragged-row defect already fixed for the panels — and worse here, because a gap
                    in a stat row reads as a card that failed to render rather than as a capability the org
                    does not have. Absence should be invisible when the thing absent was never offered.
                    Six cards -> span 2 each. Five -> the row is a 5-column grid. Either way it fills. */}
                {/* ⛔ THE ROW RE-SPANS TO FILL, AND IT COLLAPSES BEFORE IT OVERFLOWS.
                    Six (or five) fixed columns at 390px gives ~60px per card and the page scrolls sideways —
                    which the viewport leg caught on its FIRST baseline (390px viewport, 455px capture).
                    The edition-dependent count only applies once there is room for it. */}
                <div
                  className={`grid grid-cols-2 gap-3 sm:grid-cols-3 ${enterprise ? "lg:grid-cols-6" : "lg:grid-cols-5"}`}
                >
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
                  {/* ⛔ ENTERPRISE. `/policies` and `/zero-trust-mode` are both gated, so on the open edition
                      this card is ABSENT — not "0", not "could not load" in red. It was the SECOND instance of
                      the same conflation in this slice, still live after the first was fixed at one call
                      site: only an enumeration finds the rest (src/lib/edition.ts). */}
                  {enterprise && (
                    <Stat
                      label="Access Rules"
                      icon="shield"
                      value={rules}
                      sub={zeroTrust === null ? null : zeroTrust}
                    />
                  )}
                  {/* Sixth card only where the capability exists. On the open edition the row is five wide —
                      which is the honest layout, not a gap where a broken card used to be. */}
                  {enterprise && (
                    <Stat
                      label="Pending approvals"
                      icon="user-plus"
                      value={pending}
                      sub="awaiting an admin"
                    />
                  )}
                </div>

                {/* Not in a grid — a sibling in the page column, so a `col-span-*` here would be a dead class. */}
                {fresh && (
                  <Panel title="Get started">
                    {/* The floating "Get started" widget is CUT — it becomes this. Rendered only when we KNOW
                        the org is empty: showing it because a fetch failed would tell a founder with a working
                        fleet that they have nothing. */}
                    <ol className="space-y-1.5 text-explainer leading-[1.55] text-ink-body">
                      <li>
                        1. Enroll a tunnex-node agent to serve WireGuard peers.
                      </li>
                      <li>2. Add your first device and download its config.</li>
                      <li>3. Define who may reach what under Access.</li>
                    </ol>
                    <Link
                      to="/devices"
                      className="mt-2.5 inline-block text-mono text-ink-emphasis hover:text-ink-heading"
                    >
                      Enroll a gateway →
                    </Link>
                  </Panel>
                )}

                {/* ⛔ THE BENTO: ONE grid, and EVERY ROW SUMS TO 12. A ragged row is the tell that a panel
                    was placed rather than composed — the design has no row that does not fill.

                    Row 2: Peer Connection Status 4 · Gateway Health 4 · Recent Activity 4
                    Row 3: Needs Attention 8 · System Health 4

                    CUT from the design, each with its reason (docs/S14.4-commit-one.md):
                      Site-Link Throughput — the spec forbids the field's use as a rate series
                      Device Posture       — deferred to the Devices section, which owns the posture vocabulary
                      Network map / HA Hub Set — no hub, generation, pin or handshake-age field exists on Site
                      Alerts               — composed from sources this screen does not own; Access Events' job
                      Fleet risk           — Tier-3, not built */}
                {/* README panel spans on the 12-column base: rows 4+4+4, 4+4+4, 8+4. */}
                {/* One column until there is room for twelve. A `col-span-4` panel on a 12-column grid at
                    390px is ~100px wide, and a 120px donut inside it pushes the page sideways. */}
                <div className="grid grid-cols-1 gap-3 lg:grid-cols-12">
                  <Panel title="Peer Connection Status" className="lg:col-span-4">
                    {/* ⚠ RE-SOURCED TO DEVICES. This counted GATEWAYS — a different and smaller population
                        than the one the panel is named for. A chart can be perfectly honest about the wrong
                        denominator, and nothing in the render would look wrong. */}
                    <Donut
                      label="Peer connection status"
                      source={{
                        endpoint: "/api/v1/organizations/{orgId}/devices",
                      }}
                      failed={devicesRes !== null && !devicesRes.ok}
                      slices={devicesRes?.ok ? peerSlices(devicesRes.data) : []}
                      centreLabel="devices"
                      empty="No devices enrolled yet."
                    />
                    {/* The design's caption, verbatim — it states the product's rule, not a decoration. */}
                    <p className="mt-2 text-explainer leading-[1.55] text-ink-tertiary">
                      Status derived from WireGuard handshake liveness. Never
                      green while dead.
                    </p>
                  </Panel>

                  <Panel title="Gateway Health" className="lg:col-span-4">
                    {nodesRes === null ? (
                      <Loading />
                    ) : !nodesRes.ok ? (
                      <ErrorText>Gateway health is unavailable.</ErrorText>
                    ) : nodesRes.data.length === 0 ? (
                      <EmptyState>No gateway enrolled yet.</EmptyState>
                    ) : (
                      <List label="Gateway health">
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
                            <span className="flex items-center justify-between gap-2">
                              <span className="truncate font-mono text-mono text-ink-primary">
                                {g.name}
                              </span>
                              <Badge tone={g.tone}>{g.label}</Badge>
                            </span>
                          </ListItem>
                        ))}
                      </List>
                    )}
                  </Panel>

                  <Panel title="Recent Activity" className="lg:col-span-4">
                    {data.recent_activity.length === 0 ? (
                      <EmptyState>No activity yet.</EmptyState>
                    ) : (
                      <List label="Recent activity">
                        {data.recent_activity.slice(0, 6).map((a, i) => (
                          <ListItem key={i}>
                            <span className="flex items-baseline justify-between gap-2">
                              <span className="truncate font-mono text-mono text-ink-primary">
                                {a.action}
                              </span>
                              <span className="shrink-0 text-micro text-ink-tertiary">
                                {relativeAge(a.created_at)}
                              </span>
                            </span>
                          </ListItem>
                        ))}
                      </List>
                    )}
                  </Panel>

                  <Panel title="Site-Link Traffic" className="lg:col-span-4">
                    {/* ⛔ THE PANEL IS CATEGORY ONE. THE WIREFRAME'S CHART IS CATEGORY TWO.
                        The counters exist; the TIME SERIES does not. `rx_bytes` is "a raw gauge since the
                        last handshake (display only, never summed as monotonic)" — it RESETS at every
                        handshake, so a 7-day line would look like throughput and not be throughput at any
                        data volume, forever.
                        S8.3 ruled metrics-L1: cumulative-since-handshake totals, labelled as exactly that,
                        no rate graphs and no sampling implied. So this renders NUMBERS.
                        The rate/time-series version is owed to S11.1, where it gets an endpoint. */}
                    {hubSetRes === null ? (
                      <Loading />
                    ) : !hubSetRes.ok ? (
                      <ErrorText>Link traffic is unavailable.</ErrorText>
                    ) : !hubSetRes.data?.members?.length ? (
                      <EmptyState>
                        No hub set yet. Pin two or more gateways to a site and
                        their link counters appear here.
                      </EmptyState>
                    ) : (
                      (() => {
                        const t = linkTraffic(hubSetRes.data.members);
                        return (
                          <>
                            <div className="flex gap-6">
                              <div>
                                <p className="text-[11px] font-medium text-ink-secondary">
                                  Inbound
                                </p>
                                <p className="mt-1 font-mono text-[20px] font-bold text-ink-heading">
                                  {formatBytes(t.rxBytes)}
                                </p>
                              </div>
                              <div>
                                <p className="text-[11px] font-medium text-ink-secondary">
                                  Outbound
                                </p>
                                <p className="mt-1 font-mono text-[20px] font-bold text-ink-heading">
                                  {formatBytes(t.txBytes)}
                                </p>
                              </div>
                            </div>
                            {t.silent > 0 && (
                              // Absent metrics are not an idle link, so the silence is STATED rather than
                              // folded into the totals as zero.
                              <p className="mt-2 text-[11px] text-warn">
                                {t.silent} of {t.reporting + t.silent} members
                                are not reporting. Their traffic is not
                                included.
                              </p>
                            )}
                            <p className="mt-2 text-explainer leading-[1.55] text-ink-tertiary">
                              Cumulative since each member&rsquo;s last
                              handshake, not a rate. These counters reset
                              whenever a peer re-handshakes, so they are a
                              running total and never a throughput figure.
                            </p>
                          </>
                        );
                      })()
                    )}
                  </Panel>

                  <Panel title="Device Posture" className="lg:col-span-4">
                    {devicesRes === null ? (
                      <Loading />
                    ) : !devicesRes.ok ? (
                      <ErrorText>Device posture is unavailable.</ErrorText>
                    ) : (
                      (() => {
                        const ps = postureSplit(devicesRes.data);
                        const none =
                          ps.compliant + ps.blocked + ps.unknown === 0;
                        if (none)
                          return (
                            <EmptyState>No devices enrolled yet.</EmptyState>
                          );
                        return (
                          <>
                            <Donut
                              label="Device posture"
                              source={{
                                endpoint:
                                  "/api/v1/organizations/{orgId}/devices",
                              }}
                              failed={false}
                              slices={[
                                {
                                  label: "Compliant",
                                  value: ps.compliant,
                                  tone: "ok",
                                },
                                {
                                  label: "Blocked",
                                  value: ps.blocked,
                                  tone: "danger",
                                },
                                {
                                  label: "Unknown",
                                  value: ps.unknown,
                                  tone: "neutral",
                                },
                              ]}
                              centreLabel="devices"
                              empty="No devices enrolled yet."
                            />
                            {/* ⛔ THE STATE, IN WORDS — not "n/a".
                                A big "n/a" is indistinguishable from a failed load at a glance, which is the
                                exact confusion this screen exists to remove. When nothing has reported there
                                is no percentage to state (0% would claim total non-compliance, 100% the
                                opposite, and neither was measured) — so the SENTENCE says what is true. */}
                            <p className="mt-2 text-explainer leading-[1.55] text-ink-tertiary">
                              {ps.percent === null
                                ? `No device has reported posture yet, so there is no compliance rate to show. Unknown is its own state: absence is not compliance.`
                                : `${ps.percent}% of the ${ps.compliant + ps.blocked} devices that have reported are compliant. The ${ps.unknown} that have not reported are excluded — absence is not compliance.`}
                            </p>
                          </>
                        );
                      })()
                    )}
                  </Panel>

                  <Panel title="HA Hub Set" className="lg:col-span-4">
                    {/* ⚠ THIS PANEL WAS CUT ON A WRONG MEASUREMENT. The audit checked the `Site` schema for
                        hub/generation/pin fields, found none, and declared the data absent — but the hub set
                        is its OWN endpoint and schema, and `hubsetview.ts` already projects it. An absence
                        found by looking in one place is not an absence (docs/laws.md). */}
                    {hubSetRes === null ? (
                      <Loading />
                    ) : !hubSetRes.ok ? (
                      <ErrorText>The hub set is unavailable.</ErrorText>
                    ) : (
                      (() => {
                        // Defensive: a served object without `members` must not throw the whole screen. One panel's
                        // bad shape taking the page down is a blast radius nobody chose.
                        const hv = hubSetRes.data?.members
                          ? hubSetView(hubSetRes.data, Date.now())
                          : null;
                        if (!hv)
                          return (
                            <EmptyState>
                              No HA hub set. Pin two or more gateways to create
                              one.
                            </EmptyState>
                          );
                        return (
                          <>
                            <Badge tone="neutral">GEN {hv.generation}</Badge>
                            <List label="Hub set">
                              {hv.members.map((m) => {
                                // The row carries a nodeId; the NAME lives on /nodes, so the two are joined
                                // here. An unjoinable id renders as the id rather than as a blank — an
                                // unnamed member is still a member, and hiding it would understate the set.
                                const node = nodesRes?.ok
                                  ? nodesRes.data.find((n) => n.id === m.nodeId)
                                  : undefined;
                                return (
                                  <ListItem key={m.nodeId}>
                                    <span className="flex items-center justify-between gap-2">
                                      <span className="truncate font-mono text-mono text-ink-primary">
                                        {node?.name ?? m.nodeId.slice(0, 8)}
                                      </span>
                                      <span className="shrink-0 text-micro text-ink-tertiary">
                                        {m.demoted ? "demoted" : m.role}
                                        {/* `hubsetview` returns a dash glyph for "not reporting". Rendering
                                            that would be both an em-dash and a silence where the honest word
                                            exists — and "absent metrics is not an idle link" is this panel's
                                            own rule. */}
                                        {m.handshakeAge === "—"
                                          ? " · not reporting"
                                          : ` · hs ${m.handshakeAge}`}
                                      </span>
                                    </span>
                                  </ListItem>
                                );
                              })}
                            </List>
                            <p className="mt-2 text-explainer leading-[1.55] text-ink-tertiary">
                              Pinned gateways form the hub set. members[0] is
                              the acting primary, and the generation bumps on
                              every promotion. Absent metrics are not an idle
                              link.
                            </p>
                          </>
                        );
                      })()
                    )}
                  </Panel>

                  <Panel title="Network map" className="lg:col-span-4">
                    {/* ⚠ ALSO A RETRACTED CUT. Cut as "no SiteLink schema" — true, and beside the point:
                        `assembleTopology()` already projects sites + their gateways from data this screen
                        fetches. CATEGORY ONE, not category three: the capability exists and has no data yet,
                        so it gets an EMPTY STATE rather than absence (docs/EPIC-14, the three-way test). */}
                    {sitesRes === null || nodesRes === null ? (
                      <Loading />
                    ) : !sitesRes.ok || !nodesRes.ok ? (
                      <ErrorText>The topology is unavailable.</ErrorText>
                    ) : (
                      <NodeLink
                        label="Site topology"
                        source={{
                          endpoint: "/api/v1/organizations/{orgId}/sites",
                        }}
                        failed={false}
                        nodes={assembleTopology(
                          sitesRes.data,
                          {},
                          nodesRes.data,
                        ).map((c) => ({
                          id: c.id,
                          label: c.name,
                          kind: c.gateways.some((g) => g.isHub)
                            ? ("hub" as const)
                            : ("spoke" as const),
                        }))}
                        links={[]}
                        empty="No sites configured yet. Bind a gateway to a site to build the mesh."
                      />
                    )}
                  </Panel>

                  <Panel title="Needs Attention" className="lg:col-span-4">
                    {attention === null ? (
                      <Loading />
                    ) : attention.length === 0 ? (
                      <EmptyState>Nothing needs attention.</EmptyState>
                    ) : (
                      <List label="Needs attention">
                        {attention.map((a) => (
                          <ListItem key={a.key}>
                            <span className="flex items-center justify-between gap-2">
                              <span className="text-cell text-ink-body">
                                {a.text}
                              </span>
                              <Link
                                to={a.to}
                                className="shrink-0 text-mono text-ink-emphasis hover:text-ink-heading"
                              >
                                Review
                              </Link>
                            </span>
                          </ListItem>
                        ))}
                      </List>
                    )}
                    <p className="mt-2 text-explainer leading-[1.55] text-ink-tertiary">
                      Server refusals are shown verbatim. No client-side
                      re-validation.
                    </p>
                  </Panel>

                  <Panel title="System Health" className="lg:col-span-4">
                    <List label="System health">
                      <ListItem>
                        <span className="flex items-center justify-between gap-2">
                          <span className="text-cell text-ink-body">
                            Control Plane
                          </span>
                          <HealthStatus />
                        </span>
                      </ListItem>
                    </List>
                    {/* ⛔ ONE ROW, NOT FIVE. The design lists Control Plane · Database · WireGuard Service ·
                        IdP Sync · Access-log retention. `/healthz` says of itself: "Reports process liveness.
                        NO EXTERNAL DEPENDENCIES ARE CHECKED." Rendering "Database ● Healthy" from it would
                        claim a check that never ran — a green light for a thing nobody looked at, which is
                        the render-floor violation in its most dangerous form. IdP Sync and Access-log
                        retention are enterprise-only and absent on this edition. */}
                    <p className="mt-2 text-explainer leading-[1.55] text-ink-tertiary">
                      Liveness only. The control plane does not probe its
                      dependencies, so nothing else is claimed here.
                    </p>
                  </Panel>
                </div>
              </div>
            );
          })()}
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
    // Composes GLASS rather than restating it — the divergence between this card and Panel is exactly what
    // produced a screenshot with glass stat cards above flat panels.
    // ⛔ role="group" + aria-label MAKES THE CARD ADDRESSABLE BY NAME.
    //
    // The e2e specs read a stat's value with `getByText('Members').locator('xpath=preceding-sibling::div[1]')`
    // — the value happened to be the div BEFORE the label. The design puts the icon+label row first and the
    // value second, so that xpath now points at nothing, and three specs failed on a layout change that was
    // asked for.
    //
    // Re-pointing the xpath would preserve the coupling. A NAMED GROUP survives any internal rearrangement,
    // which is the same fix the DataTable conversion needed and the same lesson: when adding semantics breaks
    // a query, the query was weak (docs/laws.md).
    <div
      role="group"
      aria-label={label}
      className={`${GLASS} flex flex-col gap-2 p-3.5`}
    >
      <div className="flex items-center gap-[9px]">
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
          {value.state === "failed" ? "n/a" : "…"}
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
