import { useId, type ReactNode } from "react";
import { EmptyState } from "./ui";

// S14.3 SLICE C — DATA VISUALIZATION. THREE PRIMITIVES, HAND-ROLLED SVG, NO CHARTING LIBRARY.
//
// WHY HAND-ROLLED, measured rather than preferred: of the ten visualization types named across the 17 screens,
// a general charting library covers at most three (donut, histogram, bar) and NONE of the force-directed
// network map, the bipartite access-flow, the address-space heatmap or the radial device fabric. A library
// would be added for three and the other seven hand-rolled anyway — which is exactly how "a design system
// acquires four charting libraries." Against a 352 kB bundle that is a large fraction for a minority of need.
//
// AND THE SURVIVORS REDUCE TO THREE SHAPES, not ten: a PROPORTION, a BINNED COUNT over discrete events, and a
// NODE-LINK graph. The heatmap is a proportion on a grid.

/**
 * ⛔ EVERY CHART NAMES ITS SOURCE, AND THE COMPILER ENFORCES IT.
 *
 * The render-floor rule used to be prose — *"every panel names its endpoint or is marked roadmap"* — and prose
 * is a convention. As a REQUIRED prop it is a mechanism: **a chart with no source does not typecheck.**
 *
 * ⚠ AND THE AUDIT READS THE SPEC'S SEMANTICS, NOT MERELY ENDPOINT EXISTENCE. That is the harder case and the
 * one that would otherwise pass. `openapi.yaml` describes `rx_bytes`/`tx_bytes` as:
 *
 *     "Raw gauge since the last handshake (display only, never summed as monotonic)."
 *
 * The endpoint EXISTS. The field EXISTS. The spec FORBIDS the use. An audit that only asks "does an endpoint
 * supply this?" answers YES and lets a throughput chart through — which is why both known render-floor
 * violations in this repo are charts. `endpoint` is therefore a claim about a PERMITTED reading, not about a
 * URL being reachable.
 */
export type VizSource =
  | { endpoint: string; roadmap?: never }
  | { roadmap: true; why: string; endpoint?: never };

/** Shared frame: accessible name, the source contract, and the failed/empty discipline in ONE place. */
interface VizFrameProps {
  /** The chart's accessible name. Required — an unnamed graphic is unqueryable and unannounced. */
  label: string;
  source: VizSource;
  /**
   * REQUIRED, same reasoning as `DataTable`: an empty dataset means either "there are none" or "we never found
   * out", and drawing the second as the first is the reassuring-empty defect with an axis on it. A default
   * would pick the dangerous answer silently.
   */
  failed: boolean;
  children: ReactNode;
  /** Rendered instead of the graphic when there is genuinely nothing to draw. */
  empty: ReactNode;
  isEmpty: boolean;
}

/**
 * The frame every visualization renders through.
 *
 * ⛔ A FAILED LOAD RENDERS NOTHING — never an empty axis, never a flat line at zero. A chart is the easiest
 * place in a UI to assert a fact nobody measured: a zero baseline LOOKS like data, and "no traffic" and "we
 * could not read the traffic" are opposite claims that draw identically.
 */
export function VizFrame({
  label,
  source,
  failed,
  isEmpty,
  empty,
  children,
}: VizFrameProps) {
  if (failed) return null;
  if (source.roadmap) {
    // NOT a plausible drawing. A roadmap chart renders its honest state — a picture with no data behind it is
    // the render-floor violation itself, and a greyed-out sample is still a picture.
    return (
      <p
        role="note"
        className="rounded-md border border-white/5 bg-ink-800 px-3 py-2 text-xs text-slate-400"
      >
        {label} isn&rsquo;t available yet. {source.why}
      </p>
    );
  }
  if (isEmpty) return <EmptyState>{empty}</EmptyState>;
  return <figure aria-label={label}>{children}</figure>;
}

// ── PRIMITIVE 1 — PROPORTION ────────────────────────────────────────────────────────────────────────────────

export interface Slice {
  label: string;
  value: number;
  tone: "ok" | "warn" | "danger" | "neutral";
}

const TONE_VAR: Record<Slice["tone"], string> = {
  ok: "var(--tnx-ok)",
  warn: "var(--tnx-warn)",
  danger: "var(--tnx-danger)",
  neutral: "var(--tnx-neutral)",
};

/**
 * A proportion of a CURRENT-STATE total — peers online, devices by posture, members by role.
 *
 * ⛔ THE NUMBERS ARE RENDERED AS TEXT BESIDE THE ARC, NOT ONLY AS GEOMETRY. An SVG arc is unreadable to a
 * screen reader, unqueryable by the tier, and ambiguous to anyone who cannot distinguish the colours — three
 * failures with one cause, the same one `Badge` avoids by carrying its status as text.
 */
export function Donut({
  label,
  source,
  failed,
  slices,
  empty = "Nothing to show yet.",
  centreLabel,
}: {
  label: string;
  source: VizSource;
  failed: boolean;
  slices: Slice[];
  empty?: ReactNode;
  /** The word under the centre total ("devices", "gateways"). Absent when the total needs no unit. */
  centreLabel?: string;
}) {
  const total = slices.reduce((t, s) => t + s.value, 0);
  const titleId = useId();
  let offset = 25; // 25% = 12 o'clock, so the first arc starts at the top rather than at 3 o'clock
  return (
    <VizFrame
      label={label}
      source={source}
      failed={failed}
      isEmpty={total === 0}
      empty={empty}
    >
      <div className="flex items-center gap-4">
        <div className="relative h-[120px] w-[120px] shrink-0">
          <svg
            viewBox="0 0 42 42"
            className="h-full w-full -rotate-90"
            role="presentation"
            aria-labelledby={titleId}
          >
            <title id={titleId}>{label}</title>
            {/* The track. Without it a partial ring reads as a broken ring rather than as a proportion. */}
            <circle
              cx="21"
              cy="21"
              r="15.9"
              fill="transparent"
              stroke="var(--tnx-badge-bg)"
              strokeWidth="4"
            />
            {slices.map((s) => {
              const pct = (s.value / total) * 100;
              const el = (
                <circle
                  key={s.label}
                  cx="21"
                  cy="21"
                  r="15.9"
                  fill="transparent"
                  stroke={TONE_VAR[s.tone]}
                  strokeWidth="4"
                  strokeDasharray={`${pct} ${100 - pct}`}
                  strokeDashoffset={String(offset)}
                />
              );
              offset -= pct;
              return el;
            })}
          </svg>
          {/* THE TOTAL IN THE CENTRE, as the design has it — and as TEXT, so it is readable, queryable and
              announced. The ring is the accelerant; the number is the content. */}
          <div className="pointer-events-none absolute inset-0 flex flex-col items-center justify-center">
            <span className="text-[26px] font-bold leading-none text-ink-heading">
              {total}
            </span>
            {centreLabel && (
              <span className="mt-1 text-[9px] text-ink-tertiary">
                {centreLabel}
              </span>
            )}
          </div>
        </div>
        {/* The legend. This is what the tier queries and what a screen reader announces. */}
        <ul className="min-w-0 flex-1 space-y-1.5 text-[11px]">
          {slices.map((s) => (
            <li key={s.label} className="flex items-center gap-2 text-ink-body">
              <span
                aria-hidden
                className="h-[7px] w-[7px] shrink-0 rounded-full"
                style={{ background: TONE_VAR[s.tone] }}
              />
              <span className="truncate">{s.label}</span>
              <span className="ml-auto shrink-0 text-ink-primary">
                {s.value}
                {total > 0 && (
                  <span className="ml-1 text-ink-tertiary">
                    ({Math.round((s.value / total) * 100)}%)
                  </span>
                )}
              </span>
            </li>
          ))}
        </ul>
      </div>
    </VizFrame>
  );
}

// ── PRIMITIVE 2 — BINNED COUNT OVER DISCRETE EVENTS ─────────────────────────────────────────────────────────

export interface Bin {
  /** Bucket label — an hour, a day, a provider. */
  label: string;
  value: number;
  /**
   * ⛔ NO DATA FOR THIS BUCKET — DISTINCT FROM ZERO, and the distinction is the reason this primitive is honest
   * enough to ship. `AccessEvent.decision` carries `gap` as a first-class enum value precisely because the
   * agent can know it did not observe a window. A chart that draws absent data as a zero-height bar is the
   * reassuring-empty defect with an axis on it: "no denials" and "we did not see" look identical.
   */
  gap?: boolean;
}

/**
 * A count of DISCRETE EVENTS per bucket — never a sampled rate.
 *
 * That distinction is what makes this chart permissible when a throughput series is not: binning events the
 * API actually returns (`/access-events`, with `occurred_at`) is honest; drawing a rate from a gauge the spec
 * calls "display only, never summed as monotonic" is not.
 */
export function Histogram({
  label,
  source,
  failed,
  bins,
  empty = "No events in this window.",
}: {
  label: string;
  source: VizSource;
  failed: boolean;
  bins: Bin[];
  empty?: ReactNode;
}) {
  const max = Math.max(1, ...bins.filter((b) => !b.gap).map((b) => b.value));
  return (
    <VizFrame
      label={label}
      source={source}
      failed={failed}
      isEmpty={bins.length === 0}
      empty={empty}
    >
      <ol className="flex h-24 items-end gap-1">
        {bins.map((b) => (
          <li key={b.label} className="flex h-full flex-1 flex-col justify-end">
            {b.gap ? (
              // A GAP IS DRAWN AS A GAP: a hatched placeholder with its own label, never a zero-height bar.
              <span
                aria-label={`${b.label}: no data`}
                title={`${b.label}: no data`}
                className="block w-full border-b-2 border-dashed border-slate-600"
                style={{ height: "100%" }}
              />
            ) : (
              <span
                aria-label={`${b.label}: ${b.value}`}
                title={`${b.label}: ${b.value}`}
                className="block w-full rounded-sm bg-accent-500"
                style={{ height: `${(b.value / max) * 100}%` }}
              />
            )}
          </li>
        ))}
      </ol>
    </VizFrame>
  );
}

// ── PRIMITIVE 3 — NODE-LINK ─────────────────────────────────────────────────────────────────────────────────

export interface Node {
  id: string;
  label: string;
  kind: "hub" | "spoke";
  /** One line of true facts under the label. The wireframe's `kind · ip · status`, minus the ip we do not serve. */
  sub?: string;
  /**
   * The number inside the ring.
   *
   * ⛔ The wireframe puts a SITE COUNT here because its nodes are regions. Ours are sites, so there is no
   * count of that kind — this carries whatever real number the caller has (Sites passes the site's bound
   * gateway count). Omitted renders an empty ring rather than a zero, because "no number to show" and
   * "the number is zero" are different and only one of them is a fact about the network.
   */
  value?: number | string;
  /**
   * The node's own link state, for the ring and the status dot.
   *
   * ⛔ ABSENT MEANS "NO LINK EXISTS", NOT "HEALTHY". A site with no gateway bound, or whose gateway IS the
   * hub, has no site link at all — so it has no link STATE either, and must not be tinted as if it did.
   * The neutral rendering is the honest one; `note` says why in words.
   */
  tone?: LinkTone;
  /** Why there is no link, or what is wrong with it. Rendered verbatim; never inferred from the tone. */
  note?: string;
}

/**
 * ⛔ THREE TONES, NOT A BOOLEAN — S14.5.
 *
 * This was `healthy: boolean` and the legend it feeds has THREE entries (linked · degraded · down). A
 * two-state type under a three-state legend forces every caller to collapse `degraded` into one of the
 * neighbours, and the safe-looking collapse (degraded → healthy) is the silent-blackhole direction.
 *
 * The control plane already distinguishes them: `site_link_down` / `site_hub_down` are their own health
 * kinds. The type now carries what the data carries.
 */
export type LinkTone = "linked" | "degraded" | "down";

export interface Link {
  from: string;
  to: string;
  tone: LinkTone;
  /** Why it is not `linked`. Rendered verbatim in the list; never inferred from the tone. */
  note?: string;
}

// ⛔ TONES TAKEN FROM THE WIREFRAME'S OWN `TONE` MAP, NOT INVENTED.
//
// I had these as ok/warn/danger — a green, an amber and a red. The design is NEAR-MONOCHROME: an `ok` edge
// is light grey, and degraded/down are progressively DARKER greys, distinguished by a DASH PATTERN. Only the
// status dot carries a hue, and only for `degraded`.
//
// That is the better call and it is worth stating why, because "add colour" is the reflex. A five-node mesh
// with three red edges reads as an emergency at a glance even when one spoke is merely unreachable. Recession
// is the honest encoding for a degraded link: it RETREATS rather than shouting, and the words in the list
// below carry the actual claim. Colour is spent where it is scarce and therefore meaningful.
export const LINK_STROKE: Record<LinkTone, string> = {
  linked: "#C9C9C4",
  degraded: "#3A3A3A",
  down: "#303030",
};
// `down` is dashed as well as red, so the state survives a monochrome print and a red-green viewer.
export const LINK_DASH: Record<LinkTone, string | undefined> = {
  linked: undefined,
  degraded: "6 7",
  down: "6 7",
};

// ring / fill / dot per tone — the wireframe's TONE map, verbatim.
const NODE_RING: Record<LinkTone, string> = {
  linked: "#C9C9C4",
  degraded: "#3A3A3A",
  down: "#303030",
};
const NODE_FILL: Record<LinkTone, string> = {
  linked: "#171717",
  degraded: "#161616",
  down: "#101010",
};
const NODE_DOT: Record<LinkTone, string> = {
  linked: "#D6D6D2",
  degraded: "#C39A4E", // the ONE hue in the diagram
  down: "#5E5E5B",
};

/**
 * The site topology.
 *
 * ⛔ DELIBERATELY NOT FORCE-DIRECTED, though the wireframe drew it that way. The model already computes a
 * deterministic hub-and-spoke (`siteLinkGraph`, S8.2), and a force simulation over a known structure produces a
 * DIFFERENT PICTURE ON EVERY RENDER of the same data — which makes it untestable, unmemorable, and impossible
 * to describe over a support call. Determinism is worth more here than organic-looking placement.
 */
export function NodeLink({
  label,
  source,
  failed,
  nodes,
  links,
  empty = "No sites yet.",
  selectedId,
  onSelect,
}: {
  label: string;
  source: VizSource;
  failed: boolean;
  nodes: Node[];
  links: Link[];
  empty?: ReactNode;
  /** Controlled selection. Undefined = the diagram is inert, exactly as it was before S14.5. */
  selectedId?: string | null;
  onSelect?: (id: string | null) => void;
}) {
  const hub = nodes.find((n) => n.kind === "hub");
  const spokes = nodes.filter((n) => n.kind !== "hub");
  const pos = new Map<string, { x: number; y: number }>();
  // The wireframe's frame: 600x320, hub dead centre at (300,162). Spokes ring it. Starting at -90° puts
  // the first spoke at twelve o'clock, which is where a reader looks first; a lone spoke then sits ABOVE the
  // hub rather than at an arbitrary angle.
  // ⛔ THE LAYOUT IS FITTED TO THE NODE COUNT, NOT INHERITED FROM THE POPULATED EXAMPLE.
  //
  // The wireframe places five spokes at fixed coordinates inside a 600x320 frame, and it looks right because
  // five spokes FILL that frame. I took the frame and the ring radius and left them fixed — so ONE site
  // rendered as a column of two circles with the left two-thirds of the panel empty. It read as a broken
  // diagram rather than a sparse one.
  //
  // A LAYOUT DERIVED FROM A POPULATED EXAMPLE MUST BE CHECKED AT N=1. A design shows every diagram at its
  // most interesting size, which is the size it will almost never have on a customer's first day.
  //
  // ⚠ AND THE FIRST FIX FOR THAT WAS ALSO WRONG, IN THE EXACT OPPOSITE DIRECTION. Fitting the viewBox to
  // the placed nodes removed the empty space by MAGNIFYING everything: a two-node bounding box stretched to
  // the panel width rendered 150px rings and oversized labels.
  //
  // THE SCALE IS A CONTRACT. The design's svg is `viewBox 0 0 600 320` at `height: 320px`, so ONE USER UNIT
  // IS ONE PIXEL and a hub ring is 68px ON PURPOSE. Fitting the box breaks that silently, because the shapes
  // stay in proportion to EACH OTHER while every one of them is the wrong size — nothing looks distorted, it
  // is just all wrong together, which is the hardest kind to notice.
  //
  // SO: the FRAME stays fixed, the PLACEMENT adapts to the count, and the content is TRANSLATED to centre.
  // Sparse then reads as sparse — airy and balanced — rather than as broken or as zoomed.
  const HUB = { x: 300, y: 162 };
  const k = spokes.length;
  if (hub) pos.set(hub.id, HUB);
  if (!hub && k === 1) {
    pos.set(spokes[0]!.id, HUB);
  } else {
    // One spoke needs distance, not an orbit. Two want opposite sides. Three or more want a ring.
    const rx = k <= 1 ? 155 : k === 2 ? 185 : 200;
    const ry = k <= 2 ? 0 : 105;
    spokes.forEach((sp, i) => {
      // Twelve o'clock first for a real ring; a LONE spoke goes RIGHT, because a relationship reads
      // left-to-right and straight-up reads as a stack.
      const a = k === 1 ? 0 : (i / k) * Math.PI * 2 - Math.PI / 2;
      pos.set(sp.id, {
        x: HUB.x + Math.cos(a) * rx,
        y: HUB.y + Math.sin(a) * ry,
      });
    });
  }

  // ⛔ FIT THE BOX *AND* THE PIXEL HEIGHT TO THE SAME NUMBER — that is what keeps the scale at 1:1.
  //
  // Fitting the viewBox ALONE magnified everything (attempt 2). Pinning a 600x320 box ALONE left a 320px-tall
  // panel with two small rings adrift in it (attempt 3). Both were half the answer.
  //
  // `preserveAspectRatio="xMidYMid meet"` scales to fit the TIGHTER of the two axes. So if the viewBox height
  // in USER UNITS equals the element height in PIXELS, the scale is exactly 1 — the design's contract, a 68px
  // hub ring — and the horizontal remainder is simply empty space, centred. The frame follows the content in
  // SIZE without ever changing its SCALE.
  const pts = [...pos.values()];
  const PAD_X = 34 + 42; // widest ring + room for the label, which is wider than its circle
  const PAD_TOP = 34 + 14;
  const PAD_BOTTOM = 34 + 34; // label at r+15 and sub-line at r+27 are drawn BELOW the ring
  const xs = pts.map((p) => p.x);
  const ys = pts.map((p) => p.y);
  const boxX = pts.length ? Math.min(...xs) - PAD_X : 0;
  const boxY = pts.length ? Math.min(...ys) - PAD_TOP : 0;
  const boxW = pts.length ? Math.max(...xs) - Math.min(...xs) + PAD_X * 2 : 600;
  const boxH = pts.length
    ? Math.max(...ys) - Math.min(...ys) + PAD_TOP + PAD_BOTTOM
    : 320;

  const interactive = onSelect != null;
  const selected = nodes.find((n) => n.id === selectedId) ?? null;

  return (
    <VizFrame
      label={label}
      source={source}
      failed={failed}
      isEmpty={nodes.length === 0}
      empty={empty}
    >
      {/* ⛔ GEOMETRY TAKEN FROM THE WIREFRAME, NOT INVENTED: viewBox 600x320, hub at (300,162).
          The earlier version was a 200x120 box of FILLED discs and it was wrong twice over — `w-full` with
          no height made it ~750px tall in an 8fr column, and the nodes were solid where the design has
          HOLLOW RINGS on a dark fill. The gallery could not catch either: it renders every specimen inside
          `w-80`, where the same element is a tidy 192px and a solid dot reads as a deliberate dot.
          A COMPONENT CONSTRAINED BY ITS HARNESS IS NOT A COMPONENT THAT HAS BEEN TESTED AT SIZE. */}
      <svg
        viewBox={`${boxX} ${boxY} ${boxW} ${boxH}`}
        preserveAspectRatio="xMidYMid meet"
        style={{ height: `${boxH}px` }}
        className="w-full"
        role="presentation"
      >
        {links.map((l) => {
          const a = pos.get(l.from);
          const b = pos.get(l.to);
          if (!a || !b) return null;
          const touches = selectedId === l.from || selectedId === l.to;
          return (
            <line
              key={`${l.from}-${l.to}`}
              x1={a.x}
              y1={a.y}
              x2={b.x}
              y2={b.y}
              stroke={LINK_STROKE[l.tone]}
              strokeDasharray={LINK_DASH[l.tone]}
              strokeWidth={touches ? 2.5 : 1.5}
              strokeLinecap="round"
              opacity={selectedId && !touches ? 0.18 : 1}
            />
          );
        })}
        {nodes.map((n) => {
          const p = pos.get(n.id)!;
          const isSel = n.id === selectedId;
          const isHub = n.kind === "hub";
          const r = isHub ? 34 : 26;
          const dim = selectedId && !isSel ? 0.3 : 1;
          return (
            <g key={n.id} opacity={dim}>
              {isSel && (
                <circle
                  cx={p.x}
                  cy={p.y}
                  r={r + 6}
                  fill="none"
                  stroke="var(--tnx-text-heading)"
                  strokeWidth="1.5"
                  opacity="0.55"
                />
              )}
              {/* The RING. Dark fill + light stroke, per the design — not a solid disc. */}
              <circle
                cx={p.x}
                cy={p.y}
                r={r}
                fill={isHub ? "#1F1F1F" : n.tone ? NODE_FILL[n.tone] : "#171717"}
                stroke={isHub ? "#C9C9C4" : n.tone ? NODE_RING[n.tone] : "#3A3A3A"}
                strokeWidth="1.6"
              />
              {/* The status dot at upper-right, coloured by the node's own worst link. Carried in the list
                  as words too — a dot alone states nothing to a screen reader. */}
              <circle
                cx={p.x + r * 0.66}
                cy={p.y - r * 0.66}
                r={4}
                fill={n.tone ? NODE_DOT[n.tone] : "#5E5E5B"}
                stroke="var(--tnx-bg)"
                strokeWidth="1.5"
              />
              <text
                x={p.x}
                y={p.y + (isHub ? 4 : 5)}
                textAnchor="middle"
                fill="var(--tnx-text-heading)"
                fontSize={isHub ? 11 : 15}
                fontWeight="700"
                fontFamily={isHub ? "JetBrains Mono, monospace" : "inherit"}
              >
                {isHub ? "HUB" : (n.value ?? "")}
              </text>
              <text
                x={p.x}
                y={p.y + r + 15}
                textAnchor="middle"
                fill="var(--tnx-text-primary)"
                fontSize="10.5"
                fontWeight="600"
              >
                {n.label}
              </text>
              {n.sub && (
                <text
                  x={p.x}
                  y={p.y + r + 27}
                  textAnchor="middle"
                  fill="var(--tnx-text-secondary)"
                  fontSize="8"
                  fontFamily="JetBrains Mono, monospace"
                >
                  {n.sub}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      {/* ⛔ THE SVG IS DECORATION. THIS LIST IS THE CONTENT — and it is what makes the diagram operable.
          Selection lives on real <button> elements here, NOT on the <circle>s: an SVG node is not focusable,
          not reachable by keyboard, and announces nothing. Clicking a circle is a shortcut for people who
          can see and aim; the list is the interface. Link state is stated in WORDS, so "down" is never
          carried by a red line alone. */}
      {/* ROW SPEC TAKEN FROM THE HANDOFF, not from a screenshot: inset surface, 1px hairline, radius 8,
          padding 9/11, MONO name, a pill for the state, the range in a muted grey, the note pushed right.
          It was bare text under the diagram, which read as debug output beside a designed panel. */}
      <ul className="mt-2 flex flex-col gap-1.5 text-cell">
        {nodes.map((n) => {
          const isSel = n.id === selectedId;
          const body = (
            <>
              <span className="font-mono text-ink-primary">{n.label}</span>
              {n.kind === "hub" ? (
                <span className="rounded-full border border-line px-1.5 py-px font-mono text-micro text-ink-body">
                  HUB
                </span>
              ) : n.tone ? (
                <span
                  className="rounded-full border px-1.5 py-px font-mono text-micro"
                  style={{ color: NODE_DOT[n.tone], borderColor: NODE_DOT[n.tone] }}
                >
                  {n.tone}
                </span>
              ) : (
                // NO PILL WHEN NO LINK EXISTS. A pill is a claim about a state, and there is no state here.
                <span className="font-mono text-micro text-ink-faint">
                  {n.note ?? "no link"}
                </span>
              )}
              {n.sub && (
                <span className="truncate text-micro text-ink-tertiary">
                  {n.sub}
                </span>
              )}
            </>
          );
          const rowClass =
            "flex w-full items-center gap-2.5 rounded-lg border border-line bg-ink-800 px-2.5 py-2 text-left";
          return (
            <li key={n.id}>
              {interactive ? (
                <button
                  type="button"
                  aria-pressed={isSel}
                  onClick={() => onSelect(isSel ? null : n.id)}
                  className={`${rowClass} ${isSel ? "ring-1 ring-white/25" : ""}`}
                >
                  {body}
                </button>
              ) : (
                <span className={rowClass}>{body}</span>
              )}
            </li>
          );
        })}
        {links
          .filter((l) => l.tone !== "linked")
          .map((l) => (
            <li
              key={`${l.from}-${l.to}-d`}
              className={l.tone === "down" ? "text-danger" : "text-warn"}
            >
              {l.from} to {l.to}: {l.note ?? l.tone}
            </li>
          ))}
      </ul>
      {interactive && (
        // ⛔ THE SELECTED-NODE READOUT, as its own bordered box rather than a loose paragraph.
        //
        // IT OCCUPIES THE SPACE WHETHER OR NOT ANYTHING IS SELECTED, on purpose: a readout that APPEARS on
        // selection shifts every element beneath it, and a diagram that reflows the page when you click it
        // feels broken even though nothing is wrong. The unselected state is a real state with real copy,
        // not a gap waiting to be filled.
        <div className="mt-2 flex items-center gap-2.5 self-start rounded-lg border border-line bg-ink-800 px-3 py-1.5">
          <span className="whitespace-nowrap text-cell font-semibold text-ink-emphasis">
            {selected ? selected.label : "No node selected"}
          </span>
          <span className="truncate font-mono text-micro text-ink-secondary">
            {selected
              ? (selected.sub ?? "no further detail served")
              : "Click a node to inspect"}
          </span>
        </div>
      )}
    </VizFrame>
  );
}
