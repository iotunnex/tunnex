import { useCallback, useEffect, useMemo, useState } from "react";
import { api, loadOne, type Loaded, type Node, type Org } from "../lib/api";
import { Gateways as EnrolCeremony } from "../components/Gateways";
import { LoadRetry } from "../components/LoadRetry";
import { Badge, DataTable, EmptyState, Panel } from "../components/ui";
import { badgeClass } from "../lib/healthview";
import { relativeAge } from "../lib/format";
import {
  applyGatewayFilter,
  gatewayFilterCounts,
  groupGateways,
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
  const [org, setOrg] = useState<Org | null>(null);
  const [nodes, setNodes] = useState<Node[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [filter, setFilter] = useState<GatewayFilter>("all");

  const reload = useCallback(async () => {
    setLoadError(null);
    const oRes = await loadOne(() => api.GET("/api/v1/organizations"));
    if (!oRes.ok) return setLoadError(oRes.error);
    const first = (oRes.data as Org[])[0];
    if (!first)
      return setLoadError("You are not a member of any organization yet.");
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
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const counts = useMemo(
    () => gatewayFilterCounts(nodes ?? []),
    [nodes],
  );
  const groups = useMemo(
    () => applyGatewayFilter(groupGateways(nodes ?? []), filter),
    [nodes, filter],
  );

  const columns = [
    {
      key: "name",
      header: "Gateway",
      cell: (r: GatewayRow) => (
        <span className="flex items-center gap-2">
          <span className="font-mono text-ink-primary">{r.name}</span>
          {r.isHub && <Badge tone="neutral">HUB</Badge>}
        </span>
      ),
    },
    {
      key: "health",
      header: "State",
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
      key: "ovpn",
      header: "OpenVPN",
      cell: (r: GatewayRow) =>
        r.ovpnHealth ? (
          // S9.1 4d: a SEPARATE axis. An opted-in gateway that is not serving says why rather than
          // reading green, and it says so in its own column so the two axes are never conflated.
          <Badge tone="warn">{r.ovpnHealth.replace(/^ovpn_/, "")}</Badge>
        ) : (
          <span className="text-ink-faint">n/a</span>
        ),
    },
    {
      key: "agent",
      header: "Agent",
      cell: (r: GatewayRow) => (
        <span className="font-mono text-micro text-ink-body">
          {r.agentVersion || "n/a"}
        </span>
      ),
    },
    {
      key: "seen",
      header: "Last seen",
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
                </Panel>
              ))}

              {/* ⛔ THE COLUMNS THAT ARE NOT HERE, AND WHY — once, at the panel, not per row.
                  Absence recorded is a decision; absence unrecorded gets re-proposed at the next review. */}
              <p className="text-micro text-ink-faint">
                The design also shows peers, cloud/region and egress capability
                per gateway. We serve none of those fields today. The peer count
                is its own slice: a hub&rsquo;s WireGuard peers include site
                links, so counting devices would under-report on exactly the
                gateway you are looking at hardest.
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
