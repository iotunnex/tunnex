import { useCallback, useEffect, useMemo, useState } from "react";
import { useOrg } from "../lib/useOrg";
import {
  api,
  loadOne,
  type Loaded,
  type Node,
  type Org,
  type Site,
} from "../lib/api";
import { Gateways as EnrolCeremony } from "../components/Gateways";
import { LoadRetry } from "../components/LoadRetry";
import { Badge, DataTable, EmptyState, Panel } from "../components/ui";
import { badgeClass } from "../lib/healthview";
import { relativeAge } from "../lib/format";
import {
  applyGatewayFilter,
  gatewayFilterCounts,
  groupGateways,
  groupNotes,
  type GatewayFilter,
  type GatewayRow,
} from "../lib/gatewaysview";

// ── S14.6 — GATEWAYS, THE SECTION PASS ──────────────────────────────────────────────────────────────────
//
// Slice 1 promoted this from a component buried in Devices into a screen. This is the layout.
//
// ⛔ `Fleet risk` IS CUT (epic open) — the handoff's biggest panel here is a bubble plot of agent version ×
// peer load, and risk scoring is an unbuilt Tier-3 name. Its ruled replacement is the HEALTH-GROUPED LIST,
// which is what the left column is.
//
// ⛔ AND THREE OF THE HANDOFF TABLE'S FIVE COLUMNS HAVE NO DATA BEHIND THEM. `PEERS`, `cloud · region` and
// `egress ✓` are not fields we serve. They are absent WITH THEIR REASON on the panel grid rather than
// silently dropped — "redesign the gateway table" sounds like layout work and is actually a column-by-column
// availability audit.
//
// SCALE: one row per gateway, constant height, grouped by what is wrong. A 200-gateway fleet reads the same
// as a 5-gateway one, and the teaching text renders ONCE rather than per row.

const GROUP_LABEL: Record<string, string> = {
  degraded: "Needs attention",
  healthy: "Healthy",
  revoked: "Revoked",
};

function FilterChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={`rounded-full border px-3 py-1 text-cell ${
        active
          ? "border-white/40 bg-white/[.16] text-ink-heading"
          : "border-line text-ink-tertiary hover:text-ink-body"
      }`}
    >
      {label} ({count})
    </button>
  );
}

export default function GatewaysPage() {
  const { org: currentOrg, loading: orgLoading, failed: orgFailed } = useOrg();
  const [org, setOrg] = useState<Org | null>(null);
  const [nodes, setNodes] = useState<Node[] | null>(null);
  // Site NAMES for the gateway sub-line. NON-FATAL: a failed sites read leaves the sub-line absent rather
  // than blanking the fleet — the gateway list is this screen's subject and the site name is a courtesy.
  const [siteNames, setSiteNames] = useState<Record<string, string>>({});
  const [loadError, setLoadError] = useState<string | null>(null);
  const [filter, setFilter] = useState<GatewayFilter>("all");

  const reload = useCallback(async () => {
    setLoadError(null);
    // ⛔ THE ORG COMES FROM THE SEAM, NOT FROM INDEX ZERO (S12.5). This used to fetch the org list here and
    // take `[0]`, which meant a user in two organizations could reach only one of them and the switcher in
    // the header would have had nothing to switch.
    // ⛔ LOADING IS NOT ABSENCE (S12.5). See the note in Dashboard.tsx — three states, not two: still
    // loading (say nothing), the read failed (say THAT), genuinely no membership (say that).
    if (orgLoading) return;
    const first = currentOrg;
    if (!first)
      return setLoadError(
        orgFailed
          ? "Could not load your organizations."
          : "You are not a member of any organization yet.",
      );
    setOrg(first);
    const nRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/nodes", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Node[]>;
    // ⛔ A FAILED LOAD IS NOT AN EMPTY FLEET. `[].length === 0` is how "we could not read the gateways"
    // becomes a confident "you have none", on the screen whose job is telling you what is running.
    if (!nRes.ok) return setLoadError(nRes.error);
    setNodes(nRes.data);
    const sRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/sites", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Site[]>;
    if (sRes.ok) {
      setSiteNames(Object.fromEntries(sRes.data.map((x) => [x.id, x.name])));
    }
    // ⚠ currentOrg IS A DEPENDENCY, AND THAT IS THE HALF THAT MAKES THE SWITCHER WORK. Without it the
    // page keeps rendering the org it mounted with — the control moves, the data does not, and the user is
    // looking at one tenant's screen labelled with another's name.
  }, [currentOrg, orgLoading, orgFailed]);

  useEffect(() => {
    void reload();
  }, [reload]);

  const counts = useMemo(() => gatewayFilterCounts(nodes ?? []), [nodes]);
  const groups = useMemo(
    () => applyGatewayFilter(groupGateways(nodes ?? [], siteNames), filter),
    [nodes, filter, siteNames],
  );

  const columns = [
    {
      key: "name",
      header: "Gateway",
      // ⚠ THE SITE NAME IS SEARCHABLE THOUGH IT IS A SUB-LINE, and "HUB" is searchable though it is a badge.
      // A term an operator can SEE on the row must be a term that finds the row.
      sortValue: (r: GatewayRow) =>
        `${r.name} ${r.siteName ?? ""}${r.isHub ? " hub" : ""}`,
      cell: (r: GatewayRow) => (
        <span className="flex flex-col gap-0.5">
          <span className="flex items-center gap-2">
            <span className="font-mono text-ink-primary">{r.name}</span>
            {r.isHub && <Badge tone="neutral">HUB</Badge>}
          </span>
          {/* ⛔ THE SERVEABLE THIRD OF A SUB-LINE I CUT WHOLESALE. The handoff shows
              `AWS · ap-southeast-1 · site: ap-lan`; cloud and region are genuinely absent, and I took
              `site` with them. It IS served (`Node.site_id`) and it is what connects this screen to Sites. */}
          {r.siteName && (
            <span className="font-mono text-micro text-ink-faint">
              site: {r.siteName}
            </span>
          )}
        </span>
      ),
    },
    {
      key: "health",
      header: "State",
      // ⛔ THE STATE AS TEXT — the cell is a Badge, so without this a search for "revoked" finds nothing.
      sortValue: (r: GatewayRow) =>
        r.status === "revoked" ? "revoked" : (r.health?.label ?? "healthy"),
      cell: (r: GatewayRow) =>
        r.status === "revoked" ? (
          // WF-S11-10: `revoked` IS the state. No health badge beside it.
          <Badge tone="neutral">revoked</Badge>
        ) : r.health ? (
          <span className={badgeClass(r.health.tone)}>{r.health.label}</span>
        ) : (
          <Badge tone="ok">healthy</Badge>
        ),
    },
    {
      key: "agent",
      header: "Agent",
      sortValue: (r: GatewayRow) => r.agentVersion || "n/a",
      cell: (r: GatewayRow) => (
        <span className="font-mono text-micro text-ink-body">
          {r.agentVersion || "n/a"}
        </span>
      ),
    },
    {
      key: "seen",
      header: "Last seen",
      // ⚠ SORTS BY THE TIMESTAMP, SEARCHES BY THE WORDS. A never-connected gateway sorts to one end rather
      // than into the middle of a lexicographic jumble of "3h ago" / "17m ago".
      sortValue: (r: GatewayRow) =>
        r.lastSeenAt ? Date.parse(r.lastSeenAt) : 0,
      cell: (r: GatewayRow) => (
        <span className="text-micro text-ink-tertiary" data-volatile>
          {r.lastSeenAt ? relativeAge(r.lastSeenAt) : "never connected"}
        </span>
      ),
    },
  ];

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="text-[22px] font-semibold text-ink-heading">
            Gateways
          </h1>
          <p className="text-cell text-ink-tertiary">{org ? org.name : "…"}</p>
        </div>
      </div>

      {loadError && <LoadRetry error={loadError} onRetry={reload} />}
      {!loadError && (org === null || nodes === null) && (
        <p className="text-cell text-ink-faint">Loading…</p>
      )}

      {!loadError && org && nodes && (
        <>
          {/* The handoff's chips: All / Healthy / Degraded, counts derived from the SAME grouping the table
              renders below, so the two can never disagree. */}
          <div className="flex flex-wrap items-center gap-2">
            <FilterChip
              label="All"
              count={counts.all}
              active={filter === "all"}
              onClick={() => setFilter("all")}
            />
            <FilterChip
              label="Healthy"
              count={counts.healthy}
              active={filter === "healthy"}
              onClick={() => setFilter("healthy")}
            />
            <FilterChip
              label="Needs attention"
              count={counts.degraded}
              active={filter === "degraded"}
              onClick={() => setFilter("degraded")}
            />
            {counts.revoked > 0 && (
              // Stated rather than left to arithmetic: `All` includes revoked and the other two do not, so
              // healthy + degraded < all, which reads as a bug unless the screen says why.
              <span className="text-micro text-ink-faint">
                {counts.revoked} revoked, shown under All
              </span>
            )}
          </div>

          <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-[8fr_4fr]">
            <div className="flex min-w-0 flex-col gap-3">
              {groups.map((g) => (
                <Panel
                  key={g.key}
                  title={`${GROUP_LABEL[g.key]} (${g.rows.length})`}
                >
                  <DataTable
                    caption={`${GROUP_LABEL[g.key]} gateways`}
                    columns={columns}
                    rows={g.rows}
                    rowKey={(r: GatewayRow) => r.id}
                    empty={
                      g.key === "degraded"
                        ? "Nothing needs attention."
                        : g.key === "revoked"
                          ? "No revoked gateways."
                          : "No healthy gateways."
                    }
                    // The page blanks to a retry on any failed load, so reaching this render means the
                    // read succeeded.
                    failed={false}
                  />
                  {/* ⛔ THE NOTES — the epic's KEEP list, rendered ONCE PER GROUP rather than per row.
                      The badge names the state; these say what it MEANS. They are a property of the health
                      KIND, so four `site link down` rows would otherwise carry four copies of one sentence. */}
                  {groupNotes(g.rows).map((n) => (
                    <p key={n} className="text-micro text-ink-tertiary">
                      {n}
                    </p>
                  ))}
                </Panel>
              ))}

              {/* ⛔ THE COLUMNS THAT ARE NOT HERE, AND WHY — once, at the panel, not per row.
                  Absence recorded is a decision; absence unrecorded gets re-proposed at the next review. */}
              <p className="text-micro text-ink-faint">
                Not shown, and why: <strong>peers</strong> is its own slice (a
                hub&rsquo;s WireGuard peers include site links, so counting
                devices would under-report on exactly the gateway you are
                looking at hardest). <strong>Cloud and region</strong> are not
                fields we serve, nor is <strong>egress capability</strong>. The{" "}
                <strong>subtitle</strong> the design carries here is held behind
                a separate ruling on where control-plane health is stated, since
                the page header would be its third appearance.
              </p>
            </div>

            <div className="flex min-w-0 flex-col gap-3">
              {/* The enrolment ceremony, with its one-time join token. The list is suppressed because this
                  page owns it above. */}
              <EnrolCeremony
                org={org}
                nodes={nodes}
                onNodesChanged={reload}
                renderList={false}
              />

              {/* ⛔ CONDITIONAL ON OPT-IN, not six `n/a` cells. The per-row column was dropped with this:
                  an org that never opted in has no service to report, and the same four values belong in ONE
                  place rather than repeated per gateway. Below the threshold the panel names the precondition
                  instead of offering the surface. */}
              <Panel title="OpenVPN service">
                {!org.ovpn_enabled ? (
                  <EmptyState>
                    This organization has not opted into OpenVPN, so there is no
                    service to report. Enable it in Org Settings.
                  </EmptyState>
                ) : nodes.filter((n) => n.ovpn_health).length === 0 ? (
                  <EmptyState>
                    Every OpenVPN-enabled gateway is serving.
                  </EmptyState>
                ) : (
                  <ul className="flex flex-col gap-1.5">
                    {nodes
                      .filter((n) => n.ovpn_health)
                      .map((n) => (
                        <li
                          key={n.id}
                          className="flex items-center justify-between gap-2 rounded-lg border border-line bg-ink-800 px-2.5 py-2"
                        >
                          <span className="font-mono text-cell text-ink-body">
                            {n.name}
                          </span>
                          <Badge tone="warn">
                            {String(n.ovpn_health).replace(/^ovpn_/, "")}
                          </Badge>
                        </li>
                      ))}
                  </ul>
                )}
                <p className="text-micro text-ink-faint">
                  A separate axis from policy health. An opted-in gateway that
                  is not serving says why rather than reading green.
                </p>
              </Panel>

              <Panel title="Deployment requirement">
                {/* Carried VERBATIM from the handoff. It is the honest NAT-traversal statement the epic's
                    KEEP list specifically protects, and softening it would be the reassuring-copy defect. */}
                <p className="text-cell text-ink-body">
                  Gateways need public reachability or a port-forward. Tunnex
                  ships no relay fleet.
                </p>
                <p className="text-micro text-ink-faint">
                  A gateway behind NAT with no forwarded port can still reach
                  the control plane, but peers cannot dial it, so it cannot
                  carry site transit.
                </p>
              </Panel>
            </div>
          </div>
        </>
      )}

      {!loadError && org && nodes && nodes.length === 0 && (
        <EmptyState>
          No gateways enrolled yet. Use the enrolment panel to add the first
          one.
        </EmptyState>
      )}
    </div>
  );
}
