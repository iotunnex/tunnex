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
        {label} isn&rsquo;t available yet — {source.why}
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
  neutral: "var(--tnx-ink-600)",
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
}: {
  label: string;
  source: VizSource;
  failed: boolean;
  slices: Slice[];
  empty?: ReactNode;
}) {
  const total = slices.reduce((t, s) => t + s.value, 0);
  const titleId = useId();
  let offset = 0;
  return (
    <VizFrame
      label={label}
      source={source}
      failed={failed}
      isEmpty={total === 0}
      empty={empty}
    >
      <div className="flex items-center gap-4">
        <svg
          viewBox="0 0 42 42"
          className="h-24 w-24 shrink-0"
          role="presentation"
          aria-labelledby={titleId}
        >
          <title id={titleId}>{label}</title>
          {slices.map((s) => {
            const pct = (s.value / total) * 100;
            const dash = `${pct} ${100 - pct}`;
            const el = (
              <circle
                key={s.label}
                cx="21"
                cy="21"
                r="15.9"
                fill="transparent"
                stroke={TONE_VAR[s.tone]}
                strokeWidth="4"
                strokeDasharray={dash}
                strokeDashoffset={String(25 - offset)}
              />
            );
            offset += pct;
            return el;
          })}
        </svg>
        {/* THE READABLE HALF. This is what the tier queries and what a screen reader announces. */}
        <ul className="space-y-1 text-xs">
          {slices.map((s) => (
            <li key={s.label} className="text-slate-400">
              <span className="text-slate-200">{s.value}</span> {s.label}
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
}
export interface Link {
  from: string;
  to: string;
  healthy: boolean;
}

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
}: {
  label: string;
  source: VizSource;
  failed: boolean;
  nodes: Node[];
  links: Link[];
  empty?: ReactNode;
}) {
  const hub = nodes.find((n) => n.kind === "hub");
  const spokes = nodes.filter((n) => n.kind !== "hub");
  const pos = new Map<string, { x: number; y: number }>();
  if (hub) pos.set(hub.id, { x: 100, y: 60 });
  spokes.forEach((s, i) => {
    const a = (i / Math.max(1, spokes.length)) * Math.PI * 2;
    pos.set(s.id, { x: 100 + Math.cos(a) * 70, y: 60 + Math.sin(a) * 45 });
  });

  return (
    <VizFrame
      label={label}
      source={source}
      failed={failed}
      isEmpty={nodes.length === 0}
      empty={empty}
    >
      <svg viewBox="0 0 200 120" className="w-full" role="presentation">
        {links.map((l) => {
          const a = pos.get(l.from);
          const b = pos.get(l.to);
          if (!a || !b) return null;
          return (
            <line
              key={`${l.from}-${l.to}`}
              x1={a.x}
              y1={a.y}
              x2={b.x}
              y2={b.y}
              stroke={l.healthy ? "var(--tnx-ok)" : "var(--tnx-danger)"}
              strokeWidth="1"
            />
          );
        })}
        {nodes.map((n) => {
          const p = pos.get(n.id)!;
          return (
            <circle
              key={n.id}
              cx={p.x}
              cy={p.y}
              r={n.kind === "hub" ? 6 : 4}
              fill="var(--tnx-accent-500)"
            />
          );
        })}
      </svg>
      {/* Same rule as the donut: the SVG is decoration; this list is the content. Link health is stated in
          words, so "down" is never carried by a red line alone. */}
      <ul className="mt-2 space-y-1 text-xs">
        {nodes.map((n) => (
          <li key={n.id} className="text-slate-400">
            {n.label}
            {n.kind === "hub" && (
              <span className="ml-1 text-slate-600">(hub)</span>
            )}
          </li>
        ))}
        {links
          .filter((l) => !l.healthy)
          .map((l) => (
            <li key={`${l.from}-${l.to}-d`} className="text-danger">
              {l.from} ↔ {l.to} down
            </li>
          ))}
      </ul>
    </VizFrame>
  );
}
