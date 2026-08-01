// DESIGN TOKENS — the ONE authored form (S14.1, EPIC 14).
//
// Shared because Item A ruling A3 says the desktop client gets its OWN COMPONENTS but the SAME TOKENS: two
// component sets that look like one product because they read the same values. This file is the source of
// truth for both; nothing else may hold a colour.
//
// TWO EMITTERS, ONE SOURCE. `tailwindColors()` gives Tailwind a palette of `var(--tnx-*)` references so every
// existing utility class keeps working unchanged; `themeCss()` emits the variables themselves, per theme.
// Neither mechanism works alone: a Tailwind config bakes hex at BUILD time (so a theme swap would need a
// rebuild or a `dark:`-variant on every element — the div-soup coupling this epic exists to remove), and CSS
// variables alone would abandon every class in the app. Together, a theme swap is one attribute on <html>.
//
// N-THEME BY CONSTRUCTION, TWO SHIPPED. The founder ruled two for this slice: a third palette is churn inside
// an infrastructure slice and belongs where it can be judged against rendered output.

export type Tone = "ok" | "warn" | "danger";

/**
 * THE SEMANTIC RESERVATION, AS DATA — not as a comment.
 *
 * S4.4 decision f reserved `ok` for LIVENESS ONLY ("alive right now": an online peer, a healthy check) and
 * explicitly NOT for success feedback — "sent / saved / role changed" use the accent, so that green keeps
 * meaning LIVE. That is a shipped decision about what a colour MEANS.
 *
 * It is carried here as data because a token migration is exactly how such a rule dies: `ok` drifts into a
 * generic "success" colour, every screen still renders, nothing looks wrong, and the one place that recorded
 * the rule was a comment in a config file that got rewritten. `tokens.test.ts` asserts it.
 */
export const RESERVATIONS: Record<Tone, { meaning: string; forbiddenUses: string[] }> = {
  ok: {
    meaning: "LIVENESS ONLY — alive right now (online peer, healthy check).",
    // If any of these appear as a use-site for `ok`, the reservation has been broken.
    forbiddenUses: ["success", "saved", "sent", "created", "confirmed", "role changed"],
  },
  warn: { meaning: "caution / one-time secret.", forbiddenUses: [] },
  danger: { meaning: "revoked / error.", forbiddenUses: [] },
};

/** Every token name the system defines. A theme MUST supply all of them — see `assertThemeComplete`. */
export const TOKEN_NAMES = [
  "ink-950",
  "ink-900",
  "ink-800",
  "ink-700",
  "ink-600",
  "accent-400",
  "accent-500",
  "accent-600",
  "ok",
  "warn",
  "danger",
  "text-primary",
  "text-muted",
] as const;
export type TokenName = (typeof TOKEN_NAMES)[number];

export type Theme = Record<TokenName, string>;

/**
 * `dark` is the CURRENT brand kit, value-for-value. S14.1 must not alter rendering, so these are copied from
 * the pre-existing tailwind config rather than re-picked — a re-pick would make the slice a visual change
 * wearing an infrastructure slice's name.
 */
const dark: Theme = {
  "ink-950": "#08080d",
  "ink-900": "#0b0b12",
  "ink-800": "#12121c",
  "ink-700": "#1a1a28",
  "ink-600": "#232335",
  "accent-400": "#9b84ff",
  "accent-500": "#7c5cff",
  "accent-600": "#6344e6",
  ok: "#2ecc8f",
  warn: "#fbbf24",
  danger: "#fb7185",
  "text-primary": "#ffffff",
  "text-muted": "#94a3b8",
};

/** `mono` — the second theme, present to PROVE the mechanism is n-theme. Hue stripped, contrast preserved. */
const mono: Theme = {
  "ink-950": "#0a0a0a",
  "ink-900": "#101010",
  "ink-800": "#181818",
  "ink-700": "#222222",
  "ink-600": "#2e2e2e",
  "accent-400": "#d4d4d4",
  "accent-500": "#a3a3a3",
  "accent-600": "#7a7a7a",
  ok: "#2ecc8f",
  warn: "#fbbf24",
  danger: "#fb7185",
  "text-primary": "#ffffff",
  "text-muted": "#a1a1aa",
};

export const THEMES: Record<string, Theme> = { dark, mono };
export const DEFAULT_THEME = "dark";

/** Typography + spacing travel as tokens now; ADOPTION is S14.2, so this slice cannot alter rendering. */
export const TYPOGRAPHY = {
  sans: ['"Inter Variable"', "ui-sans-serif", "system-ui", "Segoe UI", "Roboto", "sans-serif"],
  mono: ['"JetBrains Mono Variable"', "ui-monospace", "SFMono-Regular", "Menlo", "monospace"],
} as const;

// ── S14.3 SLICE 0 — THE SCALES S14.1'S PAPER CLAIMED AND ITS ARTIFACT DID NOT CARRY ──────────────────────────
//
// ⚠ THIS IS A DEFECT BEING CORRECTED, NOT A GAP BEING FILLED (founder-ruled).
//
// S14.1's commit-one listed FIVE covered groups — colour, typography, spacing, radius/border/elevation, motion.
// The emitted set was THIRTEEN NAMES AND EVERY ONE WAS A COLOUR. Font FAMILIES shipped (TYPOGRAPHY above, into
// the palette JSON); a size scale, a spacing scale, radius, elevation and motion did not.
//
// A PAPER VOUCHING FOR A PROPERTY THE ARTIFACT LACKS IS THE SAME CLASS AS A COMMENT VOUCHING FOR ABSENT CODE —
// and this repo has just paid for that class once, in S14.2's mutation 1.
//
// WHY S14.1'S OWN GATES MISSED IT, which is the part worth fixing: `tokens.test.ts` asserted theme
// COMPLETENESS (every theme supplies every token NAME) and contrast and the `ok` reservation. NOTHING ASSERTED
// THE EMITTED SET AGAINST THE CLAIMED COVERAGE. Every gate passed because every gate was aimed at the names
// that existed, never at the ones the paper promised. See CLAIMED_COVERAGE below — that assertion now exists.

/** Spacing scale. One scale, so a gap is a decision rather than a number someone typed. */
export const SPACING = {
  0: "0",
  1: "0.25rem",
  2: "0.5rem",
  3: "0.75rem",
  4: "1rem",
  6: "1.5rem",
  8: "2rem",
  12: "3rem",
  16: "4rem",
} as const;

/** Type scale. Sizes only — families are TYPOGRAPHY, and the two are separate so a theme may re-scale without re-picking a face. */
export const TYPE_SCALE = {
  xs: "0.75rem",
  sm: "0.875rem",
  base: "1rem",
  lg: "1.125rem",
  xl: "1.375rem",
  "2xl": "1.75rem",
} as const;

export const RADIUS = { none: "0", sm: "0.25rem", md: "0.5rem", lg: "0.75rem", full: "9999px" } as const;

/**
 * Elevation. The wireframe's glassmorphism layer model lives HERE rather than as inline `backdrop-filter`
 * declarations across the app — the artifact carries 242 of those, which is 242 places for one of them to drift.
 */
export const ELEVATION = {
  0: "none",
  1: "0 1px 2px rgb(0 0 0 / 0.4)",
  2: "0 4px 12px rgb(0 0 0 / 0.45)",
  3: "0 12px 32px rgb(0 0 0 / 0.5)",
} as const;

/**
 * Motion. Durations and easings, so an animation is a token rather than a number chosen per component.
 *
 * ⛔ `prefers-reduced-motion` IS A GATE, NOT A COURTESY (founder-ruled), and the CSS-first half of that gate is
 * emitted alongside these values: a media block that re-points every duration to `0ms`. That means the
 * REDUCTION IS UNCONDITIONAL AND NEEDS NO JAVASCRIPT — a component that forgets to check the preference still
 * animates for zero milliseconds. The JS half (the pure `motionAllowed` decision) is slice B, and it gates the
 * animations CSS cannot reach.
 */
export const MOTION = {
  duration: { instant: "0ms", fast: "120ms", normal: "200ms", slow: "320ms" },
  easing: {
    standard: "cubic-bezier(0.2, 0, 0, 1)",
    decelerate: "cubic-bezier(0, 0, 0, 1)",
    accelerate: "cubic-bezier(0.3, 0, 1, 1)",
  },
} as const;

/**
 * THE CLAIM, AS DATA — hand-authored to mirror what the PAPER promises, and deliberately NOT derived from the
 * scales above.
 *
 * A derived list would compare the token set to itself and pass by construction: the fixture-restates-production
 * shape, which is exactly how S14.1's coverage claim survived unchallenged. This list is the CLAIM; the emitted
 * CSS is the ARTIFACT; the census in `tokens.test.ts` compares one to the other.
 *
 * ⛔ ADDING A CATEGORY HERE WITHOUT EMITTING IT GOES RED. That is the whole point — the failure mode being
 * guarded is a paper (or this list) growing a promise the artifact never grew.
 */
export const CLAIMED_COVERAGE: Array<{ category: string; claim: string; prefix: string; minCount: number }> = [
  { category: "colour", claim: "ink surfaces, accent, semantic ok/warn/danger, text", prefix: "", minCount: 13 },
  { category: "typography", claim: "a size scale (families ship in the palette JSON)", prefix: "text-", minCount: 6 },
  { category: "spacing", claim: "one spacing scale", prefix: "space-", minCount: 9 },
  { category: "radius", claim: "border radius scale", prefix: "radius-", minCount: 5 },
  { category: "elevation", claim: "the layer/glassmorphism model, as tokens not 242 inline declarations", prefix: "elevation-", minCount: 4 },
  { category: "motion", claim: "duration + easing, with prefers-reduced-motion honoured", prefix: "duration-", minCount: 4 },
  { category: "motion", claim: "easing curves", prefix: "ease-", minCount: 3 },
];

// ── emitters ────────────────────────────────────────────────────────────────────────────────────────────────

const cssVar = (n: TokenName) => `--tnx-${n}`;

/** The Tailwind palette: every colour is a `var()` reference, so a theme swap needs no rebuild. */
export function tailwindColors() {
  const ref = (n: TokenName) => `var(${cssVar(n)})`;
  return {
    ink: { 950: ref("ink-950"), 900: ref("ink-900"), 800: ref("ink-800"), 700: ref("ink-700"), 600: ref("ink-600") },
    accent: { 400: ref("accent-400"), 500: ref("accent-500"), 600: ref("accent-600") },
    ok: ref("ok"),
    warn: ref("warn"),
    danger: ref("danger"),
  };
}

/**
 * The non-colour scales, emitted as `:root` variables — plus the reduced-motion media block.
 *
 * Colour is per-theme; these are NOT. A theme changes what the product looks like, never how far apart things
 * sit or how long a transition runs; making spacing themeable would let a theme change layout, which is a
 * different decision wearing a palette's name.
 */
export function scaleCss(): string {
  const decls = [
    ...Object.entries(TYPE_SCALE).map(([k, v]) => `--tnx-text-${k}:${v}`),
    ...Object.entries(SPACING).map(([k, v]) => `--tnx-space-${k}:${v}`),
    ...Object.entries(RADIUS).map(([k, v]) => `--tnx-radius-${k}:${v}`),
    ...Object.entries(ELEVATION).map(([k, v]) => `--tnx-elevation-${k}:${v}`),
    ...Object.entries(MOTION.duration).map(([k, v]) => `--tnx-duration-${k}:${v}`),
    ...Object.entries(MOTION.easing).map(([k, v]) => `--tnx-ease-${k}:${v}`),
  ];
  // The CSS half of the motion gate. Unconditional: nothing has to remember to check.
  const reduced = Object.keys(MOTION.duration)
    .map((k) => `--tnx-duration-${k}:0ms`)
    .join(";");
  return [
    `:root{${decls.join(";")}}`,
    `@media (prefers-reduced-motion: reduce){:root{${reduced}}}`,
  ].join("\n");
}

/** `:root` carries the default theme; `[data-theme="x"]` re-points the same names. One attribute, no rebuild. */
export function themeCss(): string {
  const block = (sel: string, t: Theme) =>
    `${sel}{${TOKEN_NAMES.map((n) => `${cssVar(n)}:${t[n]}`).join(";")}}`;
  return [
    block(":root", THEMES[DEFAULT_THEME]),
    ...Object.entries(THEMES).map(([name, t]) => block(`[data-theme="${name}"]`, t)),
  ].join("\n");
}

// ── contrast, computed — so the accessibility floor is a TEST, not a review ──────────────────────────────────

function srgbToLinear(c: number): number {
  const s = c / 255;
  return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
}

/** WCAG 2.1 relative luminance. */
export function luminance(hex: string): number {
  const h = hex.replace("#", "");
  const [r, g, b] = [0, 2, 4].map((i) => parseInt(h.slice(i, i + 2), 16));
  return 0.2126 * srgbToLinear(r) + 0.7152 * srgbToLinear(g) + 0.0722 * srgbToLinear(b);
}

/** WCAG 2.1 contrast ratio, 1..21. */
export function contrastRatio(a: string, b: string): number {
  const [l1, l2] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (l1 + 0.05) / (l2 + 0.05);
}

/**
 * THE PAIRS THE SYSTEM PERMITS, with the floor each must meet. Enumerated deliberately rather than derived:
 * a derived list would compare the token set to itself and pass by construction, which is the
 * fixture-restates-production shape applied to a design system.
 *
 * AA floors: 4.5:1 for body text, 3:1 for large text and for UI/graphical boundaries.
 */
export const CONTRAST_PAIRS: Array<{ fg: TokenName; bg: TokenName; floor: number; why: string }> = [
  { fg: "text-primary", bg: "ink-900", floor: 4.5, why: "body text on the app background" },
  { fg: "text-primary", bg: "ink-800", floor: 4.5, why: "body text on a card" },
  { fg: "text-primary", bg: "ink-700", floor: 4.5, why: "body text on a raised control" },
  { fg: "text-muted", bg: "ink-900", floor: 4.5, why: "secondary text on the app background" },
  { fg: "text-muted", bg: "ink-800", floor: 4.5, why: "secondary text on a card" },
  { fg: "ok", bg: "ink-900", floor: 3, why: "liveness badge — a UI boundary, not body text" },
  { fg: "warn", bg: "ink-900", floor: 3, why: "caution badge" },
  { fg: "danger", bg: "ink-900", floor: 3, why: "revoked/error badge" },
  { fg: "accent-400", bg: "ink-900", floor: 3, why: "link/hover affordance" },
];
