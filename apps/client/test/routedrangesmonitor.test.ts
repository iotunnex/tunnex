import { test } from "node:test";
import assert from "node:assert/strict";

import { RoutedRangesMonitor, canonRanges, canonForwards } from "../src/main/routedrangesmonitor";
import type { ResolverForward } from "../src/main/helperclient";

// mk builds a monitor whose poll returns rangesSeq[i] (an Error throws) with EMPTY forwards, and records
// every applied ranges set. Forwards-tier tests use mkF.
function mk(base: string[], rangesSeq: Array<string[] | Error>) {
  const applied: string[][] = [];
  let i = 0;
  const api = {
    routedConfig: async () => {
      const v = rangesSeq[Math.min(i++, rangesSeq.length - 1)];
      if (v instanceof Error) throw v;
      return { ranges: v, forwards: [] };
    },
  };
  const m = new RoutedRangesMonitor(
    "org",
    base,
    api,
    async (s) => {
      applied.push(s);
    },
    async () => {},
  );
  return { m, applied };
}

// mkF builds a monitor whose poll returns a FIXED range set with fwdSeq[i] forwards, recording every
// applied forward set. applyForwards throws when failForwards is true. Ranges are constant (base only) so
// the ranges tier stays quiet and the forwards tier is exercised in isolation.
function mkF(base: string[], fwdSeq: Array<ResolverForward[] | Error>, failForwards = false) {
  const fwdApplied: ResolverForward[][] = [];
  let i = 0;
  let fail = failForwards;
  const api = {
    routedConfig: async () => {
      const v = fwdSeq[Math.min(i++, fwdSeq.length - 1)];
      if (v instanceof Error) throw v;
      return { ranges: [] as string[], forwards: v };
    },
  };
  const m = new RoutedRangesMonitor(
    "org",
    base,
    api,
    async () => {},
    async (f) => {
      if (fail) {
        fail = false;
        throw new Error("resolver refused");
      }
      fwdApplied.push(f);
    },
  );
  return { m, fwdApplied };
}

const FWD = (domain: string, ip: string): ResolverForward => ({ domain, resolver_ip: ip });

test("canonRanges dedups + sorts (order-free compare — the peersEqual lesson)", () => {
  assert.deepEqual(canonRanges(["10.2.0.0/16", "10.1.0.0/16", "10.2.0.0/16", ""]), ["10.1.0.0/16", "10.2.0.0/16"]);
});

test("empty ranges = unchanged, ZERO helper calls (D5 empty-channel client half)", async () => {
  const { m, applied } = mk(["10.99.0.0/24"], [[]]);
  assert.equal(await m.checkOnce(), "unchanged");
  assert.equal(applied.length, 0, "an empty-ranges org must not emit a pointless helper call");
});

test("no-churn: N identical polls = exactly ONE apply", async () => {
  const { m, applied } = mk(["10.99.0.0/24"], [["192.168.5.0/24"], ["192.168.5.0/24"], ["192.168.5.0/24"]]);
  assert.equal(await m.checkOnce(), "applied"); // first change
  assert.equal(await m.checkOnce(), "unchanged"); // identical → no call
  assert.equal(await m.checkOnce(), "unchanged");
  assert.equal(applied.length, 1);
});

test("full-sweep: declare appears, remove disappears, stable core in EVERY applied set", async () => {
  const { m, applied } = mk(["10.99.0.0/24"], [["192.168.5.0/24"], []]);
  await m.checkOnce(); // declare
  assert.deepEqual(applied[0], ["10.99.0.0/24", "192.168.5.0/24"], "applied = base ∪ ranges");
  await m.checkOnce(); // remove → back to base only (full-sweep, not accumulated)
  assert.deepEqual(applied[1], ["10.99.0.0/24"]);
  for (const s of applied) assert.ok(s.includes("10.99.0.0/24"), "the pool (stable core) is never dropped");
});

test("fail-static: a poll THROW keeps the last-applied set (no strip-to-baked)", async () => {
  const { m, applied } = mk(["10.99.0.0/24"], [["192.168.5.0/24"], new Error("CP blip"), ["192.168.5.0/24"]]);
  assert.equal(await m.checkOnce(), "applied");
  assert.equal(await m.checkOnce(), "inconclusive"); // blip → keep, no apply
  assert.equal(applied.length, 1, "a CP blip must not un-route the office LAN");
  assert.equal(await m.checkOnce(), "unchanged"); // recovered, same ranges → still no re-apply
  assert.equal(applied.length, 1);
});

test("apply failure keeps lastApplied → retries on the next poll", async () => {
  const base = ["10.99.0.0/24"];
  let fail = true;
  const applied: string[][] = [];
  const api = { routedConfig: async () => ({ ranges: ["192.168.5.0/24"], forwards: [] }) };
  const m = new RoutedRangesMonitor(
    "org",
    base,
    api,
    async (s) => {
      if (fail) {
        fail = false;
        throw new Error("helper refused");
      }
      applied.push(s);
    },
    async () => {},
  );
  assert.equal(await m.checkOnce(), "inconclusive"); // apply threw → not advanced
  assert.equal(applied.length, 0);
  assert.equal(await m.checkOnce(), "applied"); // retry succeeds
  assert.deepEqual(applied[0], ["10.99.0.0/24", "192.168.5.0/24"]);
});

test("immediate first poll: start() applies within the first tick, then schedules the 30s cadence (ruling A)", async () => {
  const applied: string[][] = [];
  const delays: number[] = [];
  const api = { routedConfig: async () => ({ ranges: ["192.168.5.0/24"], forwards: [] }) };
  const m = new RoutedRangesMonitor(
    "org",
    ["10.99.0.0/24"],
    api,
    async (s) => {
      applied.push(s);
    },
    async () => {},
    30_000,
    300_000,
    (_cb, ms) => {
      delays.push(ms);
      return 0 as unknown as ReturnType<typeof setTimeout>; // capture the delay; never actually fire
    },
    () => {},
  );
  m.start();
  await new Promise((r) => setImmediate(r)); // drain the immediate loop's checkOnce
  assert.equal(applied.length, 1, "the first poll must fire IMMEDIATELY, not after a full interval");
  assert.equal(delays[0], 30_000, "the NEXT poll is scheduled at the steady cadence");
  m.stop();
});

test("canonForwards folds order/case-invariant (DNS tier no-churn compare)", () => {
  assert.equal(
    canonForwards([FWD("Corp.Local", "10.20.0.53"), FWD("app.internal", "10.20.0.9")]),
    canonForwards([FWD("app.internal", "10.20.0.9"), FWD("corp.local", "10.20.0.53")]),
  );
});

test("forwards tier: applies on change, no-churn on repeat (Slice 3 DNS handoff)", async () => {
  const { m, fwdApplied } = mkF(["10.99.0.0/24"], [[FWD("corp.local", "10.20.0.53")], [FWD("corp.local", "10.20.0.53")]]);
  assert.equal(await m.checkOnce(), "applied");
  assert.deepEqual(fwdApplied[0], [FWD("corp.local", "10.20.0.53")], "the gated forward is handed to set_resolvers");
  assert.equal(await m.checkOnce(), "unchanged"); // identical → no second resolver call
  assert.equal(fwdApplied.length, 1);
});

test("forwards tier: empty forwards = unchanged, ZERO resolver calls (lastForwards seeded empty)", async () => {
  const { m, fwdApplied } = mkF(["10.99.0.0/24"], [[]]);
  assert.equal(await m.checkOnce(), "unchanged");
  assert.equal(fwdApplied.length, 0, "an org with no reachable forwards must not emit a set_resolvers call");
});

test("forwards tier fail-static is INDEPENDENT: a resolver-write throw keeps last, retries — routes untouched", async () => {
  // First apply throws (failForwards) → inconclusive, lastForwards NOT advanced; next poll (same set) retries.
  const { m, fwdApplied } = mkF(["10.99.0.0/24"], [[FWD("corp.local", "10.20.0.53")], [FWD("corp.local", "10.20.0.53")]], true);
  assert.equal(await m.checkOnce(), "inconclusive"); // resolver write threw → keep, no advance
  assert.equal(fwdApplied.length, 0, "a resolver-write failure must not drop DNS to a wrong/empty answer");
  assert.equal(await m.checkOnce(), "applied"); // retry succeeds
  assert.deepEqual(fwdApplied[0], [FWD("corp.local", "10.20.0.53")]);
});

test("stop abandons an in-flight poll's result (no apply after disconnect)", async () => {
  const { m, applied } = mk(["10.99.0.0/24"], [["192.168.5.0/24"]]);
  const p = m.checkOnce();
  m.stop();
  assert.equal(await p, "skipped");
  assert.equal(applied.length, 0);
});
