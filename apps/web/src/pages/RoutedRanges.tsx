import { useCallback, useEffect, useMemo, useState } from "react";
import {
  api,
  loadOne,
  type DNSForward,
  type Loaded,
  type Org,
  type Site,
  type SiteSubnet,
} from "../lib/api";
import { LoadRetry } from "../components/LoadRetry";
import { DataTable, EmptyState, Panel } from "../components/ui";
import { AddressSpaceMap, MAP_LIST_MAX } from "../components/viz";
import { useMotionPreference } from "../components/MotionProvider";
import { motionAllowed } from "../lib/motion";
import {
  attributeRanges,
  attributionClass,
  attributionLabel,
  fanOutExceedsTripwire,
  forwardsEmptyCopy,
  mapAddressSpace,
  sortForwards,
  type RangeRow,
  type SubnetFetch,
} from "../lib/routedrangesview";

// ── S14.7 — ROUTED RANGES ───────────────────────────────────────────────────────────────────────────────
//
// The answer to "what LAN traffic goes down the tunnel", read from the SAME endpoint a device reads. That is
// the screen's whole value: not a reconstruction of what should be pushed, but the served projection itself.
//
// ⛔ TWO PANELS FROM THE HANDOFF ARE CUT, both recorded in docs/CUT-REGISTER.md before this file existed.
//
//   ADDRESS-SPACE HEATMAP — two independent reasons. A /24 lights a whole /16 cell, so the picture is
//   coarser than every range it draws; and the grid's domain is 10.0.0.0/8 while real customers use
//   172.16/12 and 192.168/16, which get NO CELLS AT ALL. A map that cannot draw two of the three private
//   blocks is not a map.
//
//   PENDING QUEUE — lives on Sites, because that is where the MUTATION ENDPOINT is: /site-subnets/pending
//   and /approve are `site:manage`, while /routed-ranges is `org:view` with no approve verb. (Its first
//   recorded reason was "the grid was cut, so this is read-only", which was DEPENDENT on the other cut and
//   would evaporate if the grid ever came back. THE GRID HAS SINCE COME BACK, and the reason held — which is
//   the whole argument for recording an INDEPENDENT reason at the time of the cut.)
//
// ⛔ THE HEATMAP WAS CUT AND IS NOW FOUNDER-RULED BACK IN — built with BOTH cut reasons closed rather than
// reproduced. See `mapAddressSpace`. The pending CELLS are legitimate here even though the pending QUEUE is
// not: the per-site fan-out this screen already issues for attribution returns pending subnets too, so they
// are real served data, and showing a withheld range as a dashed cell is the opposite of offering an
// approve control we have no permission for.
//
// SCALE: one row per range, constant height. N=many here is a /8 fully allocated — 256 /16s, or thousands of
// /24s — so there is nothing per-row that grows, and the teaching text renders once.

// ⛔ THE `STATUS` COLUMN IS CUT AS A CONSTANT COLUMN, and that is a statement about the endpoint.
// `/routed-ranges` is APPROVED-ONLY. A status column here would have exactly one value in every row of every
// org forever, which is not information — it is a column that teaches the reader the wrong thing, namely
// that some other value is possible on this screen.

export default function RoutedRangesPage() {
  const [org, setOrg] = useState<Org | null>(null);
  const [ranges, setRanges] = useState<string[] | null>(null);
  const [forwards, setForwards] = useState<DNSForward[] | null>(null);
  const [sites, setSites] = useState<Site[]>([]);
  // `null` = the fan-out has not resolved. NOT `[]` — see the four-armed union in the view model. This is the
  // state that keeps an in-flight SITE cell from reading as "no site owns this".
  const [fanOut, setFanOut] = useState<SubnetFetch[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    setLoadError(null);
    setFanOut(null);

    const oRes = await loadOne(() => api.GET("/api/v1/organizations"));
    if (!oRes.ok) return setLoadError(oRes.error);
    const first = (oRes.data as Org[])[0];
    if (!first)
      return setLoadError("You are not a member of any organization yet.");
    setOrg(first);

    // ⛔ A FAILED READ IS NOT AN EMPTY ROUTING TABLE. On the screen that answers "does my LAN traffic go down
    // the tunnel", `[]` rendered for a fetch failure says NO with total confidence. Blank the page to a retry.
    const rRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/routed-ranges", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<{ ranges: string[]; forwards: DNSForward[] }>;
    if (!rRes.ok) return setLoadError(rRes.error);
    setRanges(rRes.data.ranges);
    setForwards(rRes.data.forwards);

    // Attribution is a SECOND-CLASS read: it enriches rows that are already correct and already rendered. A
    // failure here degrades the SITE column to "Could not load" and never blanks the table.
    const sRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/sites", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Site[]>;
    if (!sRes.ok) {
      // We do not know the site list, so we cannot even enumerate who to ask. One synthetic failure marks the
      // census incomplete, which is exactly what it is — every unmatched row becomes `unknown`, not "no site".
      setSites([]);
      setFanOut([{ ok: false, siteId: "" }]);
      return;
    }
    setSites(sRes.data);

    // ⛔ THE FAN-OUT. N sites = N+1 requests, in PARALLEL, ONCE per visit. `/routed-ranges` does not serve
    // `site_id` (it is a device-facing projection), so attribution has to be joined here.
    //
    // `Promise.all` over `loadOne` results, never over raw promises: one 500 must not reject the batch and
    // lose the sites that DID answer. Partial knowledge is representable; a thrown batch is not.
    const results = await Promise.all(
      sRes.data.map(async (s): Promise<SubnetFetch> => {
        const res = (await loadOne(() =>
          api.GET("/api/v1/organizations/{orgId}/sites/{siteId}/subnets", {
            params: { path: { orgId: first.id, siteId: s.id } },
          }),
        )) as Loaded<SiteSubnet[]>;
        return res.ok
          ? { ok: true, siteId: s.id, subnets: res.data }
          : { ok: false, siteId: s.id };
      }),
    );
    setFanOut(results);
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const rows = useMemo(
    () => attributeRanges(ranges ?? [], sites, fanOut),
    [ranges, sites, fanOut],
  );
  // Pending subnets come from the SAME fan-out attribution already needs — no extra request. They are drawn
  // as withheld cells, never as approved ones.
  const pendingCidrs = useMemo(
    () =>
      (fanOut ?? [])
        .flatMap((f) => (f.ok ? f.subnets : []))
        .filter((s) => s.status === "pending")
        .map((s) => s.cidr),
    [fanOut],
  );
  const spaceMap = useMemo(
    () => mapAddressSpace(ranges ?? [], pendingCidrs),
    [ranges, pendingCidrs],
  );
  const failedSites = useMemo(
    () => (fanOut ?? []).filter((f) => !f.ok).length,
    [fanOut],
  );

  const columns = [
    {
      key: "range",
      header: "Range",
      cell: (r: RangeRow) => (
        // Monospace and unabbreviated. This is the string a device actually receives in its AllowedIPs; an
        // admin comparing it against a router config needs it character-for-character.
        <span className="font-mono text-ink-primary">{r.range}</span>
      ),
    },
    {
      key: "site",
      header: "Site",
      cell: (r: RangeRow) => (
        <span className={`text-cell ${attributionClass(r.attribution)}`}>
          {attributionLabel(r.attribution)}
        </span>
      ),
    },
    {
      key: "pushed",
      header: "Pushed to",
      cell: () => (
        // ⛔ CONSTANT BY CONSTRUCTION, AND SAID ONCE PER ROW ANYWAY, because it is the answer to the question
        // that brings people here. The handoff shows "split-tunnel AllowedIPs · 126 devices"; THE DEVICE
        // COUNT IS NOT SERVED — same class as the gateway peer count — and is absent with its reason below
        // rather than invented from the device list, which would count devices that never fetched this.
        <span className="text-micro text-ink-tertiary">
          split-tunnel AllowedIPs
        </span>
      ),
    },
  ];

  const reduced = useMotionPreference();
  const animate = motionAllowed(reduced);
  const loading = !loadError && (org === null || ranges === null);

  return (
    <div className="flex flex-col gap-3.5">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="text-[22px] font-semibold text-ink-heading">
            Routed ranges
          </h1>
          <p className="text-cell text-ink-tertiary">
            {org ? org.name : "…"}
          </p>
        </div>
      </div>

      {loadError && <LoadRetry error={loadError} onRetry={reload} />}
      {loading && <p className="text-cell text-ink-faint">Loading…</p>}

      {!loadError && org && ranges && forwards && (
        <div className="grid grid-cols-1 items-start gap-3 lg:grid-cols-[8fr_4fr]">
          <div className="flex min-w-0 flex-col gap-3">
            {spaceMap.blocks.length > 0 && (
              <Panel title="Address space map">
                <p className="text-micro text-ink-faint">
                  Approved CIDRs pushed into split-tunnel AllowedIPs. One grid
                  per private block that has ranges in it.
                </p>
                {spaceMap.blocks.map((m) => (
                  <AddressSpaceMap
                    key={m.block.key}
                    map={m}
                    animate={animate}
                  />
                ))}
                {/* Legend, in text. The grid encodes three states by fill and inset; none of that reaches a
                    screen reader, and colour alone would fail the same reader twice. */}
                <ul className="flex flex-wrap items-center gap-4 text-micro text-ink-tertiary">
                  <li className="flex items-center gap-1.5">
                    <span
                      aria-hidden
                      className="h-[9px] w-[9px] rounded-[2px]"
                      style={{ background: "var(--tnx-neutral)" }}
                    />
                    fills its cell, approved and pushed
                  </li>
                  <li className="flex items-center gap-1.5">
                    <span
                      aria-hidden
                      className="h-[5px] w-[5px] rounded-[1px]"
                      style={{ background: "var(--tnx-neutral)" }}
                    />
                    part of a cell (a range finer than the grid)
                  </li>
                  <li className="flex items-center gap-1.5">
                    <span
                      aria-hidden
                      className="h-[9px] w-[9px] rounded-[2px] border border-dashed"
                      style={{ borderColor: "var(--tnx-warn)" }}
                    />
                    pending, withheld until approved on Sites
                  </li>
                </ul>
                {spaceMap.blocks.some((m) => m.lit.length > MAP_LIST_MAX) && (
                  // A SILENT CAP IS A LIE ABOUT COVERAGE. Past six lit cells the connector fan is unreadable,
                  // so the labelled list is dropped — and said so, with the complete list named.
                  <p className="text-micro text-ink-faint">
                    Some blocks have more than {MAP_LIST_MAX} lit cells, so
                    their labelled call-outs are omitted as unreadable. Every
                    range is in the table below.
                  </p>
                )}
                {spaceMap.offMap.length > 0 && (
                  // ⛔ THE DEFECT THE HANDOFF'S FIXED 10/8 GRID WOULD HAVE HAD: a range outside every drawn
                  // block would simply not appear. Listed, never dropped.
                  <p className="text-micro text-warn">
                    Outside the private blocks and therefore not drawn:{" "}
                    <span className="font-mono">
                      {spaceMap.offMap.join(", ")}
                    </span>
                    . They are routed exactly the same; only the map cannot
                    place them.
                  </p>
                )}
                {spaceMap.unparseable.length > 0 && (
                  <p className="text-micro text-danger">
                    Not a parseable IPv4 CIDR, so not drawn:{" "}
                    <span className="font-mono">
                      {spaceMap.unparseable.join(", ")}
                    </span>
                    .
                  </p>
                )}
              </Panel>
            )}

            <Panel title={`Approved ranges (${ranges.length})`}>
              <DataTable
                caption="Approved routed ranges"
                columns={columns}
                rows={rows}
                rowKey={(r: RangeRow) => r.range}
                // ⛔ THE EMPTY STATE NAMES THE PRECONDITION, because on THIS screen empty is the common case
                // for a fresh org and is a first-class answer from the endpoint. "No ranges" alone reads as a
                // failure to a reader who does not know a range has to be approved first.
                empty="No LAN ranges are routed. A site advertises a subnet, an admin approves it on Sites, and it appears here."
                failed={false}
              />
              <p className="text-micro text-ink-tertiary">
                This is the list a split-tunnel device reads, in the order it
                reads it. Traffic to these ranges goes down the tunnel;
                everything else does not.
              </p>
            </Panel>

            {failedSites > 0 && (
              // ⛔ THE DEGRADED READ IS STATED, NOT INFERRED FROM ITALICS. Without this line a reader sees
              // some rows attributed and some not and concludes the unattributed ones have no site, which is
              // the opposite of what happened.
              <p className="text-micro text-warn">
                {failedSites} site{failedSites === 1 ? "" : "s"} could not be
                read, so any range without a site here may still belong to one.
                Retry to complete the picture.
              </p>
            )}

            {fanOutExceedsTripwire(sites.length) && (
              // The tripwire from the commit-one, surfaced where it can actually be observed. A bound that
              // only exists in a document is discovered at a customer.
              <p className="text-micro text-ink-faint">
                This organization has {sites.length} sites, so attribution costs{" "}
                {sites.length + 1} requests per visit and will feel slow. The
                fix is serving the site on the range itself.
              </p>
            )}

            <p className="text-micro text-ink-faint">
              Not shown, and why: the <strong>device count</strong> per range is
              not a field we serve, and counting devices locally would count
              ones that never fetched this list. <strong>Status</strong> is
              omitted because this endpoint is approved-only, so the column
              would carry one value forever; pending subnets are approved on{" "}
              <strong>Sites</strong>, which is where the approve permission
              lives.
            </p>
          </div>

          <div className="flex min-w-0 flex-col gap-3">
            <Panel title={`Reachable DNS forwards (${forwards.length})`}>
              {forwards.length === 0 ? (
                <EmptyState>{forwardsEmptyCopy(ranges.length)}</EmptyState>
              ) : (
                <ul className="flex flex-col gap-1.5">
                  {sortForwards(forwards).map((f) => (
                    <li
                      key={`${f.domain}@${f.resolver_ip}`}
                      className="flex items-center justify-between gap-2 rounded-lg border border-line bg-ink-800 px-2.5 py-2"
                    >
                      <span className="font-mono text-cell text-ink-body">
                        {f.domain}
                      </span>
                      <span className="font-mono text-micro text-ink-tertiary">
                        {f.resolver_ip}
                      </span>
                    </li>
                  ))}
                </ul>
              )}
              {/* ⛔ THE GATE, STATED WHETHER OR NOT THE LIST IS EMPTY. This panel's emptiness means something
                  NARROWER than it looks, and the honest reading has to be available to someone who sees a
                  NON-empty list too: a zone they configured and cannot find here was withheld, not lost. */}
              <p className="text-micro text-ink-faint">
                A forward is handed to devices only when its resolver falls
                inside a routed range. A resolver outside every range is
                withheld rather than handed over as a lookup that can only fail.
              </p>
            </Panel>

            <Panel title="Where these come from">
              <p className="text-cell text-ink-body">
                A gateway advertises its LAN, an admin approves it, and the
                control plane pushes the approved set to every split-tunnel
                device.
              </p>
              <p className="text-micro text-ink-faint">
                Ranges are validated disjoint from each other and from the
                device pool, so two sites cannot advertise the same LAN.
              </p>
            </Panel>
          </div>
        </div>
      )}
    </div>
  );
}
