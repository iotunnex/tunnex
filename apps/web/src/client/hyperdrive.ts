// The hyperdrive canvas — TRANSCRIBED from the handoff's own draw loop.
//
// ⛔ I MADE THE AUTH-HERO MISTAKE TWICE. There, I re-derived a picture from a screenshot instead of
// transcribing the SVG that shipped in the handoff. Here I read the block's TEXT extraction — which
// strips markup — saw four labels and a stats list, and built a card layout. **The block contains
// TWO CANVASES and a full draw loop**, and the window is literally called `tunnex · hyperdrive`
// after the first of them.
//
// > **A TEXT EXTRACTION OF A DESIGN IS A LIST OF ITS WORDS, NOT A DESCRIPTION OF IT.** Reading it
// > and calling that "reading the block" is how a canvas animation becomes a static card and
// > nobody notices until it is on screen.
//
// Everything below is the designer's arithmetic: 7 nodes, the ellipse radii, the stagger, the
// easing rates, the pulse periods, the graph's 64-sample window and its 63-step spacing.

export type HyperMode = "connected" | "connecting" | "idle";

type Node = {
  ang: number;
  rad: number;
  stagger: number;
  tw: number;
  sp: number;
  col: string;
};

export type HyperState = {
  nodes: Node[];
  /** Eased 0..1 link extension. Rises slowly while connecting, fast once connected. */
  link: number;
  /** Rolling throughput samples, newest last, capped at 64. */
  graph: number[];
  mode: HyperMode;
  connAt: number | null;
  _last: number;
};

const NP = 7;

/**
 * ⛔ SEEDED, NOT RANDOM — a deliberate departure, and the only one.
 *
 * The handoff seeds `tw` and `sp` with `Math.random()`. That is fine for a prototype and wrong for
 * us: it makes the animation unsnapshottable and every visual diff noisy forever. A fixed sequence
 * keeps the designer's VARIETY (the nodes still twinkle out of phase) while making the first frame
 * reproducible. Recorded here because it is the one line that is not theirs.
 */
export function createHyperState(seed = 1): HyperState {
  let s = seed;
  const rnd = () => {
    // xorshift — deterministic, and only used for phase offsets.
    s ^= s << 13;
    s ^= s >>> 17;
    s ^= s << 5;
    return Math.abs(s % 1000) / 1000;
  };
  const nodes: Node[] = [];
  for (let i = 0; i < NP; i++) {
    const ang = -Math.PI / 2 + (i / NP) * Math.PI * 2 + (i % 2 ? 0.18 : -0.12);
    const rad = 0.66 + (i % 3) * 0.11;
    nodes.push({
      ang,
      rad,
      stagger: (i / NP) * 0.6,
      tw: rnd() * Math.PI * 2,
      sp: 0.6 + rnd() * 0.8,
      col: "232,232,228",
    });
  }
  return { nodes, link: 0, graph: [], mode: "idle", connAt: null, _last: 0 };
}

/** Advance the eased link toward its target. Connecting crawls (0.018); connected snaps (0.06). */
export function stepLink(st: HyperState): void {
  const target = st.mode === "connected" || st.mode === "connecting" ? 1 : 0;
  st.link += (target - st.link) * (st.mode === "connecting" ? 0.018 : 0.06);
}

/**
 * Push one throughput sample. The handoff's own shape: while connected, a 14% chance of a burst
 * (0.55–1.0) and otherwise a low idle band; while down, decay by 0.09 a tick rather than snapping
 * to zero — so the graph drains instead of cutting.
 */
export function pushSample(st: HyperState, rand: () => number): void {
  const on = st.mode === "connected";
  const v = on
    ? rand() < 0.14
      ? 0.55 + rand() * 0.45
      : rand() * 0.2
    : Math.max(0, (st.graph[st.graph.length - 1] ?? 0) - 0.09);
  st.graph.push(v);
  while (st.graph.length > 64) st.graph.shift();
  if (!on) st.connAt = null;
  else if (!st.connAt) st.connAt = Date.now();
}

/** The hyperdrive draw, on a 2D context. Pure of React; the component owns the rAF loop. */
export function drawHyper(
  ctx: CanvasRenderingContext2D,
  w: number,
  h: number,
  st: HyperState,
  now: number,
): void {
  ctx.clearRect(0, 0, w, h);
  const cx = w / 2;
  const cy = h / 2;
  const rx = w * 0.4;
  const ry = h * 0.42;
  const L = st.link;

  const pts = st.nodes.map((nd) => ({
    x: cx + Math.cos(nd.ang) * rx * (0.92 + Math.sin(now / 900 + nd.tw) * 0.04),
    y: cy + Math.sin(nd.ang) * ry * (0.92 + Math.cos(now / 900 + nd.tw) * 0.04),
    nd,
  }));

  // Spokes, extending from the core with a per-node stagger.
  for (const p of pts) {
    const e = Math.max(0, Math.min(1, (L - p.nd.stagger) / 0.4));
    if (e <= 0) continue;
    const ex = cx + (p.x - cx) * e;
    const ey = cy + (p.y - cy) * e;
    ctx.strokeStyle = `rgba(${p.nd.col},${(0.12 + e * 0.28).toFixed(3)})`;
    ctx.lineWidth = 1.1;
    ctx.beginPath();
    ctx.moveTo(cx, cy);
    ctx.lineTo(ex, ey);
    ctx.stroke();
    // A packet only rides a spoke that is fully extended.
    if (e > 0.98) {
      const tp = (now / 1400 + p.nd.stagger) % 1;
      ctx.fillStyle = `rgba(${p.nd.col},${(0.95 * (1 - Math.abs(tp - 0.5) * 1.2)).toFixed(3)})`;
      ctx.beginPath();
      ctx.arc(cx + (p.x - cx) * tp, cy + (p.y - cy) * tp, 2.1, 0, Math.PI * 2);
      ctx.fill();
    }
  }

  // The ring between nodes appears late — only once the mesh is most of the way up.
  if (L > 0.6) {
    const ra = (L - 0.6) / 0.4;
    ctx.strokeStyle = `rgba(255,255,255,${(ra * 0.14).toFixed(3)})`;
    ctx.lineWidth = 0.8;
    for (let i = 0; i < pts.length; i++) {
      const a = pts[i];
      const b = pts[(i + 1) % pts.length];
      ctx.beginPath();
      ctx.moveTo(a.x, a.y);
      ctx.lineTo(b.x, b.y);
      ctx.stroke();
    }
  }

  // Nodes, breathing on their own phase.
  for (const p of pts) {
    const e = Math.max(0, Math.min(1, (L - p.nd.stagger) / 0.4));
    const rr = Math.max(
      1.2,
      2 + e * 2.3 + Math.sin(((now / 400) * p.nd.sp) + p.nd.tw) * 0.6 * e,
    );
    ctx.fillStyle = `rgba(${p.nd.col},${(0.25 + e * 0.7).toFixed(3)})`;
    ctx.beginPath();
    ctx.arc(p.x, p.y, rr, 0, Math.PI * 2);
    ctx.fill();
    if (e > 0.9) {
      ctx.strokeStyle = `rgba(${p.nd.col},0.4)`;
      ctx.lineWidth = 1;
      ctx.beginPath();
      ctx.arc(p.x, p.y, rr + 3, 0, Math.PI * 2);
      ctx.stroke();
    }
  }

  // The core: pulses while connecting, steady once up.
  const pulse = st.mode === "connecting" ? 2 + Math.sin(now / 140) * 2 : 0;
  const coreR = (st.mode === "connected" ? 9 : 6) + L * 3 + pulse;
  const g = ctx.createRadialGradient(cx, cy, 0, cx, cy, coreR * 3);
  g.addColorStop(0, "rgba(255,255,255,0.14)");
  g.addColorStop(0.4, "rgba(255,255,255,0.14)");
  g.addColorStop(1, "rgba(255,255,255,0)");
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.arc(cx, cy, coreR * 3, 0, Math.PI * 2);
  ctx.fill();
  ctx.fillStyle = "#D8D8D4";
  ctx.beginPath();
  ctx.arc(cx, cy, coreR * 0.55, 0, Math.PI * 2);
  ctx.fill();

  // An expanding ring, only while genuinely connected — the "it is alive" tell.
  if (st.mode === "connected") {
    const pt = (now % 2600) / 2600;
    ctx.strokeStyle = `rgba(232,232,228,${((1 - pt) * 0.4).toFixed(3)})`;
    ctx.lineWidth = 1.4;
    ctx.beginPath();
    ctx.arc(cx, cy, coreR + pt * 30, 0, Math.PI * 2);
    ctx.stroke();
  }
}

/** The throughput plot: a filled area under a 1.6px line, over a 64-sample window. */
export function drawGraph(
  ctx: CanvasRenderingContext2D,
  w: number,
  h: number,
  graph: number[],
): void {
  ctx.clearRect(0, 0, w, h);
  ctx.strokeStyle = "rgba(255,255,255,0.12)";
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(0, h * 0.5);
  ctx.lineTo(w, h * 0.5);
  ctx.stroke();

  const n = graph.length;
  if (n <= 1) return;
  // 63, not n-1: the window is fixed at 64 samples so the plot SCROLLS rather than rescaling.
  const step = w / 63;
  const base = h - 6;

  const grad = ctx.createLinearGradient(0, 0, 0, h);
  grad.addColorStop(0, "rgba(255,255,255,0.14)");
  grad.addColorStop(1, "rgba(255,255,255,0)");
  ctx.beginPath();
  ctx.moveTo(0, base);
  graph.forEach((v, i) => ctx.lineTo(i * step, base - v * (h - 12)));
  ctx.lineTo((n - 1) * step, base);
  ctx.closePath();
  ctx.fillStyle = grad;
  ctx.fill();

  ctx.beginPath();
  graph.forEach((v, i) => {
    const x = i * step;
    const y = base - v * (h - 12);
    if (i) ctx.lineTo(x, y);
    else ctx.moveTo(x, y);
  });
  ctx.strokeStyle = "#D6D6D2";
  ctx.lineWidth = 1.6;
  ctx.stroke();
}
