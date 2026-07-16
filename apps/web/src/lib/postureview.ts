import type { Device, HealthCheck } from "./api";
import { badgeClass, type BadgeTone } from "./healthview";

// postureview — PURE projections for the S7.5.3 device-posture surfaces
// (config section + devices-list badge). No fetches, no JSX: unit-tested like
// healthview/policyview.

// ── the verbatim honesty line (docs + config UI, D6 — locked) ────────────────────────
// This exact copy ships wherever an admin configures posture checks. Client-reported
// posture is NOT attestation; selling it as attestation is the dishonest failure mode
// the paper forbids.
export const POSTURE_HONESTY_LINE =
  "Posture checks deter honest non-compliance and give you an audit trail. They are " +
  "client-reported, not hardware-attested — a compromised device can misreport. " +
  "Treat posture as defense-in-depth, not a guarantee.";

// The client platforms that actually REPORT posture (the desktop apps). The coverage
// indicator enumerates exactly these — a min set for only one of them leaves the other
// unconstrained, and that gap must be VISIBLE (fail-open by design is fine; fail-open
// invisibly is the reassuring-green class).
export const REPORTING_PLATFORMS = ["macos", "windows"] as const;
export type ReportingPlatform = (typeof REPORTING_PLATFORMS)[number];

export const PLATFORM_LABELS: Record<ReportingPlatform, string> = {
  macos: "macOS",
  windows: "Windows",
};

// ── device badge (devices list + approval queue) ─────────────────────────────────────
// The three-way surface, sharpened per the slice-3 rider:
//   blocked      → danger  (excluded from every gateway right now)
//   noncompliant → warn    (warn-mode failure: surfaced, access continues)
//   unknown      → unknown ("not reported" — an admin must NOT read this as a pass)
//   compliant    → subtle ok
// Returns null when the API sent no health fields (org has no checks configured /
// open edition) — no posture noise on a feature the org doesn't use.
export interface PostureBadge {
  label: string;
  tone: BadgeTone | "ok";
}

export function postureBadge(
  d: Pick<Device, "health_state" | "health_blocked" | "health_reported_at">,
): PostureBadge | null {
  if (d.health_state === undefined) return null; // surface inactive — no badge, no noise
  if (d.health_blocked) {
    // The enforcement fact wins the label even when the report has gone stale
    // (state unknown): the device IS excluded until the sweep clears it.
    return { label: "posture blocked", tone: "danger" };
  }
  switch (d.health_state) {
    case "noncompliant":
      return { label: "posture warning", tone: "warn" };
    case "compliant":
      return { label: "posture ok", tone: "ok" };
    default:
      // unknown: never reported, stale, or the fact was absent. Distinct from
      // compliant BY DESIGN — absence is not compliance.
      return { label: d.health_reported_at ? "posture stale" : "posture not reported", tone: "unknown" };
  }
}

// postureBadgeClass extends healthview's tone vocabulary with the compliant-ok tone
// (subtle, not celebratory — compliance is the expected steady state). warn/danger/
// unknown DELEGATE to healthview.badgeClass so a palette restyle can't drift the
// posture and gateway-health badges out of sync; only "ok" is new here.
export function postureBadgeClass(tone: PostureBadge["tone"]): string {
  return tone === "ok" ? "text-emerald-400" : badgeClass(tone);
}

// ── per-fact tri-state rendering (admin detail) ──────────────────────────────────────
// A fact the client reported ABSENT renders "not reported" — never a dash that reads
// as n/a-fine, never a guessed value.
export function diskFactLabel(diskEncrypted: boolean | undefined): string {
  if (diskEncrypted === undefined) return "not reported";
  return diskEncrypted ? "encrypted" : "not encrypted";
}

// ── config rows + the coverage indicator ─────────────────────────────────────────────
export interface OsVersionMins {
  macos: string;
  windows: string;
}

// osVersionMins extracts the per-platform min map from a HealthCheck's param
// ("" = not set = that platform is NOT constrained).
export function osVersionMins(check: Pick<HealthCheck, "param"> | undefined): OsVersionMins {
  const min = (check?.param as { min?: Record<string, string> } | undefined | null)?.min ?? {};
  return { macos: min.macos ?? "", windows: min.windows ?? "" };
}

// osVersionCoverage is THE coverage indicator (the ratified rider): one line per
// reporting platform, constrained platforms with their floor, unconstrained ones
// NAMED as unconstrained — never silently omitted.
export interface CoverageLine {
  platform: ReportingPlatform;
  label: string; // e.g. "macOS: 14.0 or newer required" / "Windows: not constrained by this check"
  covered: boolean;
}

export function osVersionCoverage(mins: OsVersionMins): CoverageLine[] {
  return REPORTING_PLATFORMS.map((p) => {
    const min = mins[p].trim();
    if (!min) {
      return { platform: p, label: `${PLATFORM_LABELS[p]}: not constrained by this check`, covered: false };
    }
    return { platform: p, label: `${PLATFORM_LABELS[p]}: ${min} or newer required`, covered: true };
  });
}

// buildOsVersionParam builds the PUT param from the two inputs; empty inputs are
// OMITTED (platform-absent = not enforced). Returns null when NO platform is set —
// the caller must refuse the save (an os_version check constraining nothing is a
// config lie, and the server rejects an empty min anyway).
export function buildOsVersionParam(mins: OsVersionMins): { min: Record<string, string> } | null {
  const min: Record<string, string> = {};
  if (mins.macos.trim()) min.macos = mins.macos.trim();
  if (mins.windows.trim()) min.windows = mins.windows.trim();
  return Object.keys(min).length > 0 ? { min } : null;
}

// ── save-result copy (the would_fail blast radius) ───────────────────────────────────
// After a PUT, the server returns the best-effort count of devices whose LAST report
// would fail the check. require-mode failing devices get BLOCKED at their next report
// (~one report cycle); warn-mode ones surface a warning. The config write itself
// blocks nothing (D4 grandfather) — the copy says when the effect lands, honestly.
export function wouldFailCopy(mode: "warn" | "require", wouldFail: number | undefined): string | null {
  if (wouldFail === undefined || wouldFail === 0) return null;
  const n = `${wouldFail} device${wouldFail === 1 ? "" : "s"}`;
  if (mode === "require") {
    return `${n} last reported non-compliant for this check — they will be BLOCKED at their next report (within ~10 minutes). Devices that never report stay unaffected (unknown, not blocked).`;
  }
  return `${n} last reported non-compliant for this check — they will show a warning; access continues.`;
}

// ── section state helpers ────────────────────────────────────────────────────────────
export type CheckMode = "off" | "warn" | "require";

// checkModeOf maps the config list (no row = off) to the 3-state control.
export function checkModeOf(checks: HealthCheck[] | null, kind: HealthCheck["kind"]): CheckMode {
  const row = checks?.find((c) => c.kind === kind);
  return row ? row.mode : "off";
}
