import type { ReactNode } from "react";
import { Logo, PRODUCT_TAGLINE } from "../brand";
import { Card } from "./ui";
import { HealthStatus } from "./HealthStatus";
import {
  HERO_HEADLINE,
  HERO_SUBHEAD,
  MESH_NODES,
  TRUST_BADGES,
} from "../lib/authhero";

/**
 * ⛔ THE MESH, DRAWN AT A FIXED viewBox AND NEVER `w-full`.
 *
 * S14.7's flow graph shipped at 4x because a viewBox was scaled by a full-width class; the geometry
 * was quoted correctly from the design and then applied wrong. Fixed width/height here for the same
 * reason — the labels are positioned in user units and only stay on their nodes at one scale.
 *
 * Decorative: aria-hidden, so a screen reader hears the headline and the form, not six place names
 * with no relationship to each other.
 */
function MeshIllustration() {
  // Six nodes on a circle around the mark. Angles are explicit rather than computed in the render
  // so the layout cannot drift with a refactor.
  const R = 104;
  const cx = 150;
  const cy = 132;
  const pts = MESH_NODES.map((label, i) => {
    const a = (Math.PI * 2 * i) / MESH_NODES.length - Math.PI / 2;
    return { label, x: cx + R * Math.cos(a), y: cy + R * Math.sin(a) };
  });
  return (
    <svg
      width="300"
      height="264"
      viewBox="0 0 300 264"
      aria-hidden="true"
      focusable="false"
      className="tnx-mesh"
    >
      {/* Every node joined to every other — the claim the headline makes, drawn. */}
      {pts.map((p, i) =>
        pts.slice(i + 1).map((q, j) => (
          <line
            key={`${i}-${j}`}
            x1={p.x}
            y1={p.y}
            x2={q.x}
            y2={q.y}
            stroke="currentColor"
            strokeWidth="0.5"
            className="text-white/10"
          />
        )),
      )}
      {pts.map((p) => (
        <g key={p.label}>
          <line
            x1={cx}
            y1={cy}
            x2={p.x}
            y2={p.y}
            stroke="currentColor"
            strokeWidth="1"
            className="text-accent-400/30"
          />
          <circle cx={p.x} cy={p.y} r="4" className="fill-accent-400/70" />
          <text
            x={p.x}
            y={p.y - 10}
            textAnchor="middle"
            className="fill-slate-400 font-mono text-[9px]"
          >
            {p.label}
          </text>
        </g>
      ))}
      <circle cx={cx} cy={cy} r="17" className="fill-accent-500/20" />
      <circle cx={cx} cy={cy} r="8" className="fill-accent-400" />
    </svg>
  );
}

/** AuthLayout is the shared frame for the pre-auth screens (login, signup, reset,
 * verify): the hero panel beside the form card, health/tagline footer. */
export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-full flex-col">
      <main className="grid flex-1 place-items-center px-6 py-10">
        <div className="grid w-full max-w-4xl items-center gap-10 lg:grid-cols-2">
          {/* Hero: hidden below lg so the form is never pushed off a small screen. */}
          <div className="hidden flex-col items-center lg:flex">
            <MeshIllustration />
            <h2 className="mt-4 text-center text-xl font-semibold text-white">
              {HERO_HEADLINE}
            </h2>
            <p className="mt-2 max-w-xs text-center text-sm text-slate-400">
              {HERO_SUBHEAD}
            </p>
            {/* ⛔ EVERY BADGE IS A CLAIM THE PRODUCT CAN SUPPORT. The design's "SOC 2 Type II
                certified" and "SSO + SCIM" were CUT — see lib/authhero.ts for the measurement. */}
            <ul className="mt-5 space-y-1.5">
              {TRUST_BADGES.map((b) => (
                <li
                  key={b.text}
                  className="flex items-center gap-2 text-xs text-slate-500"
                >
                  <span className="h-1 w-1 rounded-full bg-accent-400" />
                  {b.text}
                </li>
              ))}
            </ul>
          </div>
          <div className="mx-auto w-full max-w-sm">
            <div className="mb-6 flex justify-center lg:justify-start">
              <Logo />
            </div>
            <Card>{children}</Card>
          </div>
        </div>
      </main>
      <footer className="flex items-center justify-between px-6 py-4 text-xs text-slate-600">
        <HealthStatus />
        <span>{PRODUCT_TAGLINE}</span>
      </footer>
    </div>
  );
}
