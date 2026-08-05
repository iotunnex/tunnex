import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { stripJsComments } from "./support/source";

/**
 * ⛔ "LESS OVERWHELMING" IS UNFALSIFIABLE UNLESS IT IS COUNTED.
 *
 * The dashboard carried 7 stat cards + 11 panels = **18 bordered, filled, 24px-blurred containers**, every
 * one with identical treatment. `GLASS` applied to everything distinguished nothing: "Needs Attention" was
 * panel 10 of 11 and looked exactly like "Kubernetes", so the eye had no entry point.
 *
 * > **SCARCITY IS WHAT MAKES MATERIAL READ AS EMPHASIS.** One card is the budget; the number is the design.
 *
 * ⚠ AND THE SECOND HALF IS THE ONE THAT MATTERS MORE: **CHROME MAY GO, INFORMATION MAY NOT.** A dashboard
 * that got calmer by saying less has hidden the problem rather than solved it — the reassuring-empty defect
 * at page scale. Both halves are asserted here.
 */
const DASH = readFileSync(join(__dirname, "..", "src", "pages", "Dashboard.tsx"), "utf8");
const SRC = stripJsComments(DASH);

describe("dashboard chrome budget", () => {
  it("⛔ EXACTLY ONE CARD — everything else is a borderless Section", () => {
    // `Panel` is the glass container; `Section` is label + content + space.
    const panels = SRC.match(/<Panel\b/g) ?? [];
    const sections = SRC.match(/<Section\b/g) ?? [];
    expect(panels).toHaveLength(1);
    // ⚠ And the survivor is the one that asks for a DECISION, not one that merely reports.
    expect(SRC).toMatch(/<Panel title="Needs Attention"/);
    // The rest did not vanish — they became sections.
    expect(sections.length).toBeGreaterThanOrEqual(9);
  });

  it("⚠ THE STAT ROW CARRIES NO MATERIAL — seven boxes were seven equal claims on attention", () => {
    // GLASS is imported by pages that use it; the dashboard must no longer be one of them.
    expect(SRC).not.toMatch(/\bGLASS\b/);
  });

  it("⛔ AND EVERY STAT STILL NAMES ITSELF — chrome may go, information may not", () => {
    // Removing the card briefly removed the LABEL with it: the icon row held both, and cutting the row cut
    // the word. A column of unlabelled numbers is not a quieter dashboard, it is an unreadable one.
    for (const label of ["Members", "Devices", "Gateways", "Sites"]) {
      expect(SRC).toContain(`label="${label}"`);
    }
    expect(SRC).toMatch(/\{label\}/); // the label is rendered, not merely passed
  });

  it("⚠ AND EVERY SECTION IS STILL NAMED — a borderless region must remain addressable", () => {
    // `Section` names its region via aria-labelledby exactly as `Panel` did. Dropping the box must not drop
    // the name, or "quieter" would mean "unnavigable by anything that is not an eye".
    for (const title of ["Gateway Health", "Recent Activity", "Device Posture", "Kubernetes"]) {
      expect(SRC).toContain(`title="${title}"`);
    }
  });
});
