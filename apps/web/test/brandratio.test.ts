import { describe, expect, it } from "vitest";
import { readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import { stripJsComments, stripXmlComments } from "./support/source";

// ⛔ A SIZE EXPRESSED AS ONE DIMENSION IS A HOPE ABOUT THE OTHER.
//
// Two ratio bugs shipped on the login page before this guard existed:
//   · the mark is 577x551 — NOT square — and was rendered `h-7 w-7`, squashing it horizontally
//   · the wordmark is 792x120 (6.6:1) and was given a HEIGHT ONLY, so a flex row stretched it
//
// Both were invisible in code review and obvious on screen. The assets carry their intrinsic size
// in the file, so the ratio is a FACT that can be checked rather than a value to eyeball.
const ASSETS = join(__dirname, "..", "src", "assets");

function intrinsic(file: string): { w: number; h: number } {
  const svg = stripXmlComments(readFileSync(join(ASSETS, file), "utf8"));
  const w = Number(/width="(\d+)"/.exec(svg)?.[1]);
  const h = Number(/height="(\d+)"/.exec(svg)?.[1]);
  return { w, h };
}

describe("brand assets", () => {
  it("finds the assets (vacuity floor)", () => {
    const svgs = readdirSync(ASSETS).filter((f) => f.endsWith(".svg"));
    expect(svgs.length).toBeGreaterThanOrEqual(3);
  });

  it("⛔ the mark is NOT square — the assumption that broke it", () => {
    const { w, h } = intrinsic("tunnex-logo.svg");
    expect(w).toBeGreaterThan(0);
    expect(h).toBeGreaterThan(0);
    expect(w).not.toBe(h); // rendering it `h-N w-N` deforms it
  });

  it("⛔ Logo derives BOTH dimensions from the intrinsic ratios", () => {
    // Source-level, because the bug is the ABSENCE of a width — a rendering test would have to
    // guess a container to reproduce the stretch, and the real defect is that nothing pinned it.
    const src = stripJsComments(
      readFileSync(join(__dirname, "..", "src", "brand.tsx"), "utf8"),
    );
    const mark = intrinsic("tunnex-logo.svg");
    const word = intrinsic("tunnex-wordmark.svg");
    expect(src).toContain(`${mark.w} / ${mark.h}`);
    expect(src).toContain(`${word.w} / ${word.h}`);
    // Both <img> tags must carry width AND height, not one of the two.
    const imgs = [...src.matchAll(/<img[\s\S]*?\/>/g)].map((m) => m[0]);
    expect(imgs.length).toBeGreaterThanOrEqual(2);
    for (const img of imgs) {
      expect(img, "every brand img needs width=").toMatch(/width=\{/);
      expect(img, "every brand img needs height=").toMatch(/height=\{/);
    }
  });

  it("⛔ no brand img is sized with a square utility class", () => {
    // `h-7 w-7` on a 1.047 ratio is the exact shape of the original defect.
    const src = stripJsComments(
      readFileSync(join(__dirname, "..", "src", "brand.tsx"), "utf8"),
    );
    expect(src).not.toMatch(/className="[^"]*\bh-(\d+)\b[^"]*\bw-\1\b/);
  });
});
