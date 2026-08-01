import { describe, expect, it } from "vitest";
import {
  CONTRAST_PAIRS,
  DEFAULT_THEME,
  RESERVATIONS,
  THEMES,
  TOKEN_NAMES,
  contrastRatio,
  tailwindColors,
  themeCss,
} from "../../../packages/shared/src/tokens";
// Imported by RELATIVE path, matching vite.config.ts and tailwind.config.ts. One specifier for the one
// authored form: the package-name route resolves through @tunnex/shared's raw .ts entry, which Node cannot
// load at config time and which a cached workspace link can serve staleley. Same file, no ambiguity.

// S14.1 — THE DESIGN SYSTEM'S GATES. Both FAIL the build; neither warns.
//
// A linter that emits warnings is a convention. A failing test is a mechanism — and this repo has already
// ruled that a standard recorded only in prose is the convention-not-mechanism failure.

describe("accessibility floor — WCAG 2.1 AA, COMPUTED not reviewed", () => {
  // Contrast is computable from the token values, so the floor is a unit test rather than a design review.
  // Every (foreground, background) pair the system PERMITS is enumerated deliberately in CONTRAST_PAIRS — a
  // derived list would compare the token set to itself and pass by construction.
  for (const themeName of Object.keys(THEMES)) {
    for (const pair of CONTRAST_PAIRS) {
      it(`[${themeName}] ${pair.fg} on ${pair.bg} meets ${pair.floor}:1 — ${pair.why}`, () => {
        const theme = THEMES[themeName]!;
        const ratio = contrastRatio(theme[pair.fg], theme[pair.bg]);
        expect(
          ratio,
          `${pair.fg} (${theme[pair.fg]}) on ${pair.bg} (${theme[pair.bg]}) = ${ratio.toFixed(2)}:1, floor ${pair.floor}:1`,
        ).toBeGreaterThanOrEqual(pair.floor);
      });
    }
  }

  it("the pair list is not empty — a floor over zero pairs cannot fail", () => {
    // The gate's own vacuity guard. An empty CONTRAST_PAIRS would make every assertion above vanish and the
    // suite would go green having checked nothing.
    expect(CONTRAST_PAIRS.length).toBeGreaterThanOrEqual(8);
  });
});

describe("the `ok` reservation — S4.4 decision f, asserted rather than commented", () => {
  // WHY THIS EXISTS. `ok` is reserved for LIVENESS ONLY — an online peer, a healthy check — and explicitly NOT
  // for success feedback, so that green keeps meaning LIVE. That is a decision about what a colour MEANS, and
  // a token migration is exactly how it dies: `ok` drifts into a generic "success" colour, every screen still
  // renders, nothing looks wrong, and the only record of the rule was a comment in a config that got rewritten.
  it("carries its meaning and its forbidden uses as DATA", () => {
    expect(RESERVATIONS.ok.meaning).toMatch(/liveness only/i);
    expect(RESERVATIONS.ok.forbiddenUses).toContain("success");
  });

  it("NO forbidden use appears as an `ok` use-site anywhere in the app", () => {
    // The real assertion. `text-ok` / `bg-ok` next to success wording is the drift this reservation forbids,
    // and it is the form the violation actually takes — nobody renames the token, they reuse it.
    const files = import.meta.glob("../src/**/*.tsx", { query: "?raw", import: "default", eager: true }) as Record<string, string>;
    const offenders: string[] = [];

    for (const [path, src] of Object.entries(files)) {
      for (const line of src.split("\n")) {
        if (!/\b(?:text|bg|border|ring)-ok\b/.test(line)) continue;
        const hit = RESERVATIONS.ok.forbiddenUses.find((w: string) => new RegExp(`\\b${w}\\b`, "i").test(line));
        if (hit) offenders.push(`${path}: "${hit}" beside an \`ok\` colour — ${RESERVATIONS.ok.meaning}`);
      }
    }

    expect(offenders, offenders.join("\n")).toEqual([]);
  });

  it("scans a non-trivial number of files — the scan must not pass by finding nothing", () => {
    // Same vacuity guard as above: a glob that silently matched zero files would make the check green forever.
    const files = import.meta.glob("../src/**/*.tsx", { query: "?raw", import: "default", eager: true });
    expect(Object.keys(files).length).toBeGreaterThanOrEqual(20);
  });
});

describe("theme completeness — a theme that omits a token renders a broken var()", () => {
  for (const [name, theme] of Object.entries(THEMES) as Array<[string, Record<string, string>]>) {
    it(`[${name}] supplies every token name`, () => {
      const missing = TOKEN_NAMES.filter((n) => !theme[n]);
      expect(missing, `missing tokens in "${name}": ${missing.join(", ")}`).toEqual([]);
    });
  }

  it("the emitted CSS carries :root plus one selector per theme", () => {
    const css = themeCss();
    expect(css).toContain(":root{");
    for (const name of Object.keys(THEMES)) expect(css).toContain(`[data-theme="${name}"]`);
    expect(Object.keys(THEMES)).toContain(DEFAULT_THEME);
  });

  it("the Tailwind palette references variables ONLY — a literal hex here defeats theming", () => {
    // If any colour resolved to a hex, that class would stop responding to a theme swap — silently, since it
    // would still render a colour.
    const flat = JSON.stringify(tailwindColors());
    expect(flat).not.toMatch(/#[0-9a-f]{3,8}/i);
    expect(flat).toContain("var(--tnx-");
  });
});
