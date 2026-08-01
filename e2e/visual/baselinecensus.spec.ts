import { test, expect } from "@playwright/test";
import { readdirSync, existsSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

// `__dirname` is undefined here: the e2e package is ESM, so the module directory comes from import.meta.
const HERE = dirname(fileURLToPath(import.meta.url));

// ⛔ THE BASELINE CENSUS — THE LOAD-BEARING DEFENCE, and the reason this suite can be trusted.
//
// THE FAILURE MODE IT CLOSES: a red visual suite is easiest to silence by DELETING A SNAPSHOT. That leaves
// NO DIFF TO REVIEW — the check goes green and its subject is simply gone. Mechanism ⑦ in image form: a claim
// with nothing behind it, indistinguishable from a claim that is kept.
//
// ⛔ AN EXACT COUNT, NEVER A MINIMUM. A floor (`>= 1`) is satisfied by deleting all but one, which is exactly
// the move it is meant to prevent. The number moves DELIBERATELY, in a reviewable edit to this file, or the
// suite goes red BY NAME.
//
// Same form as the screen census (`toBe`, not `toBeGreaterThan`) and for the same reason.

const EXPECTED_SNAPSHOTS = [
  "gallery-1440.png",
  "gallery-390.png",
  "overview-1440.png",
  "overview-390.png",
] as const;

test("the baseline set is EXACTLY what is expected — no additions, no silent deletions", () => {
  const dir = join(HERE, "visual.spec.ts-snapshots");
  const dir2 = join(HERE, "overview.spec.ts-snapshots");
  const found = [
    ...(existsSync(dir) ? readdirSync(dir) : []),
    ...(existsSync(dir2) ? readdirSync(dir2) : []),
  ]
    .filter((f) => f.endsWith(".png"))
    // Playwright suffixes baselines with the platform (…-linux.png). The census is about WHICH images exist,
    // not which platform produced them.
    // Playwright suffixes with PROJECT and PLATFORM (…-chromium-linux.png).
    .map((f) =>
      f.replace(
        /-(chromium|firefox|webkit)-(linux|darwin|win32)\.png$/,
        ".png",
      ),
    )
    .sort();

  expect(
    found,
    `baseline set drifted.\n  expected: ${[...EXPECTED_SNAPSHOTS].sort().join(", ")}\n  found:    ${found.join(", ")}`,
  ).toEqual([...EXPECTED_SNAPSHOTS].sort());
});

test("the expectation list is not empty — a census over zero baselines cannot fail", () => {
  expect(EXPECTED_SNAPSHOTS.length).toBeGreaterThanOrEqual(4);
});
