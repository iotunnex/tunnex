import { describe, expect, it } from "vitest";
import {
  POSTURE_HONESTY_LINE,
  buildOsVersionParam,
  checkModeOf,
  diskFactLabel,
  osVersionCoverage,
  osVersionMins,
  postureBadge,
  postureBadgeClass,
  wouldFailCopy,
} from "../src/lib/postureview";
import type { HealthCheck } from "../src/lib/api";

describe("postureBadge — the three-way legibility rider", () => {
  it("renders NOTHING when the surface is inactive (no health fields = org has no checks)", () => {
    expect(postureBadge({})).toBeNull();
  });

  it("unknown is FIRST-CLASS and distinct — never a pass", () => {
    const never = postureBadge({ health_state: "unknown", health_blocked: false });
    expect(never).toEqual({ label: "posture not reported", tone: "unknown" });
    const stale = postureBadge({ health_state: "unknown", health_blocked: false, health_reported_at: "2026-07-16T00:00:00Z" });
    expect(stale).toEqual({ label: "posture stale", tone: "unknown" });
    // The load-bearing distinction: unknown must never share compliant's tone/label.
    const ok = postureBadge({ health_state: "compliant", health_blocked: false });
    expect(never!.tone).not.toBe(ok!.tone);
    expect(stale!.tone).not.toBe(ok!.tone);
  });

  it("blocked wins the label even when the report went stale (the device IS still excluded)", () => {
    expect(postureBadge({ health_state: "unknown", health_blocked: true, health_reported_at: "2026-07-16T00:00:00Z" })).toEqual({
      label: "posture blocked",
      tone: "danger",
    });
  });

  it("warn-mode noncompliance is a warning, not a block", () => {
    expect(postureBadge({ health_state: "noncompliant", health_blocked: false })).toEqual({
      label: "posture warning",
      tone: "warn",
    });
  });

  it("tones map to distinct classes (incl. the ok tone)", () => {
    const tones = ["ok", "warn", "danger", "unknown"] as const;
    const classes = tones.map(postureBadgeClass);
    expect(new Set(classes).size).toBe(tones.length);
  });
});

describe("diskFactLabel — per-fact tri-state", () => {
  it("an absent fact reads 'not reported', never a guessed value", () => {
    expect(diskFactLabel(undefined)).toBe("not reported");
    expect(diskFactLabel(true)).toBe("encrypted");
    expect(diskFactLabel(false)).toBe("not encrypted");
  });
});

describe("osVersionCoverage — the ratified coverage indicator", () => {
  it("names an unconstrained platform explicitly — never a silent gap", () => {
    const lines = osVersionCoverage({ macos: "14.0", windows: "" });
    expect(lines).toHaveLength(2); // every reporting platform enumerated
    expect(lines.find((l) => l.platform === "macos")).toEqual({
      platform: "macos",
      label: "macOS: 14.0 or newer required",
      covered: true,
    });
    expect(lines.find((l) => l.platform === "windows")).toEqual({
      platform: "windows",
      label: "Windows: not constrained by this check",
      covered: false,
    });
  });

  it("both platforms empty → both named unconstrained", () => {
    expect(osVersionCoverage({ macos: "", windows: "" }).every((l) => !l.covered)).toBe(true);
  });
});

describe("buildOsVersionParam", () => {
  it("omits empty platforms (platform-absent = not enforced)", () => {
    expect(buildOsVersionParam({ macos: "14.0", windows: "" })).toEqual({ min: { macos: "14.0" } });
    expect(buildOsVersionParam({ macos: " 14.0 ", windows: "10.0" })).toEqual({ min: { macos: "14.0", windows: "10.0" } });
  });
  it("refuses an all-empty min (a check constraining nothing is a config lie)", () => {
    expect(buildOsVersionParam({ macos: "", windows: "  " })).toBeNull();
  });
});

describe("osVersionMins / checkModeOf — config round-trip", () => {
  const checks: HealthCheck[] = [
    { kind: "os_version", mode: "require", param: { min: { macos: "14.0" } } as never },
    { kind: "disk_encryption", mode: "warn" },
  ];
  it("extracts per-platform mins ('' = unset)", () => {
    expect(osVersionMins(checks[0])).toEqual({ macos: "14.0", windows: "" });
    expect(osVersionMins(undefined)).toEqual({ macos: "", windows: "" });
  });
  it("no row = off (opt-in by absence)", () => {
    expect(checkModeOf(checks, "os_version")).toBe("require");
    expect(checkModeOf(checks, "disk_encryption")).toBe("warn");
    expect(checkModeOf([], "disk_encryption")).toBe("off");
    expect(checkModeOf(null, "os_version")).toBe("off");
  });
});

describe("wouldFailCopy — honest blast-radius copy", () => {
  it("require says WHEN blocking lands and that non-reporters stay unaffected", () => {
    const c = wouldFailCopy("require", 3)!;
    expect(c).toContain("3 devices");
    expect(c).toContain("BLOCKED at their next report");
    expect(c).toContain("never report stay unaffected");
  });
  it("warn says access continues", () => {
    expect(wouldFailCopy("warn", 1)).toContain("access continues");
  });
  it("zero / unknown count → no banner", () => {
    expect(wouldFailCopy("require", 0)).toBeNull();
    expect(wouldFailCopy("require", undefined)).toBeNull();
  });
});

describe("the verbatim honesty line (D6 — locked copy)", () => {
  it("carries the three load-bearing claims verbatim", () => {
    expect(POSTURE_HONESTY_LINE).toContain("deter honest non-compliance");
    expect(POSTURE_HONESTY_LINE).toContain("client-reported, not hardware-attested");
    expect(POSTURE_HONESTY_LINE).toContain("defense-in-depth, not a guarantee");
  });
});
