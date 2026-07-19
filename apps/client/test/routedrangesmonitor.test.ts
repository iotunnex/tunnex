import { test } from "node:test";
import assert from "node:assert/strict";

import { RoutedRangesMonitor, canonRanges } from "../src/main/routedrangesmonitor";

// mk builds a monitor whose poll returns rangesSeq[i] (an Error throws) and records every applied set.
function mk(base: string[], rangesSeq: Array<string[] | Error>) {
  const applied: string[][] = [];
  let i = 0;
  const api = {
    routedRanges: async () => {
      const v = rangesSeq[Math.min(i++, rangesSeq.length - 1)];
      if (v instanceof Error) throw v;
      return v;
    },
  };
  const m = new RoutedRangesMonitor("org", base, api, async (s) => {
    applied.push(s);
  });
  return { m, applied };
}

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
  const api = { routedRanges: async () => ["192.168.5.0/24"] };
  const m = new RoutedRangesMonitor("org", base, api, async (s) => {
    if (fail) {
      fail = false;
      throw new Error("helper refused");
    }
    applied.push(s);
  });
  assert.equal(await m.checkOnce(), "inconclusive"); // apply threw → not advanced
  assert.equal(applied.length, 0);
  assert.equal(await m.checkOnce(), "applied"); // retry succeeds
  assert.deepEqual(applied[0], ["10.99.0.0/24", "192.168.5.0/24"]);
});

test("immediate first poll: start() applies within the first tick, then schedules the 30s cadence (ruling A)", async () => {
  const applied: string[][] = [];
  const delays: number[] = [];
  const api = { routedRanges: async () => ["192.168.5.0/24"] };
  const m = new RoutedRangesMonitor(
    "org",
    ["10.99.0.0/24"],
    api,
    async (s) => {
      applied.push(s);
    },
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

test("stop abandons an in-flight poll's result (no apply after disconnect)", async () => {
  const { m, applied } = mk(["10.99.0.0/24"], [["192.168.5.0/24"]]);
  const p = m.checkOnce();
  m.stop();
  assert.equal(await p, "skipped");
  assert.equal(applied.length, 0);
});
