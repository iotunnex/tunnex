import { describe, expect, it } from "vitest";
import {
  attributeRanges,
  attributionClass,
  attributionLabel,
  canonicalCidr,
  FANOUT_TRIPWIRE,
  fanOutExceedsTripwire,
  forwardsEmptyCopy,
  sortForwards,
  type SubnetFetch,
} from "../src/lib/routedrangesview";
import type { Site, SiteSubnet } from "../src/lib/api";

// ⛔ MECHANISM ⑨ — ONE-SIDED OBSERVATION — IS THE THING THIS FILE IS WRITTEN AGAINST.
//
// The S14.6 `aria-pressed` mutation survived because the test only ever observed the UNSELECTED value: a
// test that sees one value of a two-valued thing cannot tell the variable from the constant, and mutation
// testing inherits that blind spot rather than catching it.
//
// Every two-valued thing below is therefore asserted at BOTH values IN THE SAME TEST, so the assertion
// depends on the difference and not on one arm's literal.

const site = (id: string, name: string): Site =>
  ({ id, name }) as unknown as Site;

const subnet = (
  siteId: string,
  cidr: string,
  status: "approved" | "pending" = "approved",
): SiteSubnet =>
  ({ id: `${siteId}-${cidr}`, site_id: siteId, cidr, status }) as SiteSubnet;

const SITES = [site("s1", "Sydney"), site("s2", "Frankfurt")];

describe("canonicalCidr", () => {
  it("masks host bits off, and is the identity on an already-canonical range", () => {
    // BOTH SIDES IN ONE ASSERTION. A function that simply returned its input unchanged would pass a
    // canonical-only test; a function that always masked to /0 would pass a non-canonical-only test.
    expect(canonicalCidr("10.20.0.1/24")).toBe("10.20.0.0/24");
    expect(canonicalCidr("10.20.0.0/24")).toBe("10.20.0.0/24");
  });

  it("handles the bit-shift edges where a signed int32 would go negative", () => {
    // `0xffffffff << 0` is fine, but `<< 32` is a no-op and `<< 31` yields a NEGATIVE int32 in JS. Without
    // the `>>> 0` in the implementation these come back as garbage octets, not as a wrong-but-plausible CIDR.
    expect(canonicalCidr("10.20.30.40/0")).toBe("0.0.0.0/0");
    expect(canonicalCidr("10.20.30.40/1")).toBe("0.0.0.0/1");
    expect(canonicalCidr("172.16.5.9/32")).toBe("172.16.5.9/32");
    expect(canonicalCidr("192.168.1.130/25")).toBe("192.168.1.128/25");
  });

  it("returns null rather than a plausible-looking string for anything that is not an IPv4 CIDR", () => {
    // Null matters: the caller falls back to the RAW string as the join key. A lenient parse that guessed
    // would make two different inputs collide on one key and attribute a range to the wrong site.
    for (const bad of [
      "10.20.0.0", // no prefix
      "10.20.0.0/33", // prefix out of range
      "10.20.0.0/", // empty prefix
      "10.20.0/24", // three octets
      "10.20.0.0.0/24", // five octets
      "10.20.0.256/24", // octet out of range
      "fd00::/8", // IPv6 — not silently accepted
      "", // empty
    ])
      expect(canonicalCidr(bad), bad).toBeNull();
  });
});

describe("attributeRanges", () => {
  it("is `loading` before the fan-out resolves, and NOT loading after — null is not the same as empty", () => {
    const ranges = ["10.20.0.0/24"];

    // THE DISTINCTION THE UNION EXISTS FOR, ASSERTED AS A DIFFERENCE.
    const inFlight = attributeRanges(ranges, SITES, null);
    const resolvedEmpty = attributeRanges(ranges, SITES, []);

    expect(inFlight[0].attribution.kind).toBe("loading");
    expect(resolvedEmpty[0].attribution.kind).toBe("unmatched");
    // If the implementation collapsed null and [] the two would be equal — so assert they are not.
    expect(inFlight[0].attribution.kind).not.toBe(
      resolvedEmpty[0].attribution.kind,
    );
  });

  it("attributes a range to the site that advertises it, by canonical form on BOTH sides", () => {
    // The stored subnet here is NON-CANONICAL, which the live `cidr` column would in fact reject today. It is
    // used deliberately: this test's job is to prove the join does not DEPEND on that column type, because a
    // migration to `text` would otherwise break attribution silently.
    const fanOut: SubnetFetch[] = [
      { ok: true, siteId: "s1", subnets: [subnet("s1", "10.20.0.1/24")] },
      { ok: true, siteId: "s2", subnets: [] },
    ];
    const [row] = attributeRanges(["10.20.0.0/24"], SITES, fanOut);
    expect(row.attribution).toEqual({
      kind: "site",
      siteId: "s1",
      siteName: "Sydney",
    });
  });

  it("does NOT attribute a range to a PENDING subnet, while the same site's APPROVED subnet does attribute", () => {
    // Both arms, one test. Dropping the status filter makes the first expectation fail; hard-coding a skip
    // makes the second fail.
    const fanOut: SubnetFetch[] = [
      {
        ok: true,
        siteId: "s1",
        subnets: [
          subnet("s1", "10.30.0.0/24", "pending"),
          subnet("s1", "10.20.0.0/24", "approved"),
        ],
      },
    ];
    const rows = attributeRanges(
      ["10.20.0.0/24", "10.30.0.0/24"],
      SITES,
      fanOut,
    );
    expect(rows[0].attribution.kind).toBe("site");
    expect(rows[1].attribution.kind).toBe("unmatched");
  });

  it("degrades EVERY unmatched row to `unknown` when ANY site's fetch failed — a negative needs a complete census", () => {
    const ranges = ["10.20.0.0/24", "10.99.0.0/24"];
    const complete: SubnetFetch[] = [
      { ok: true, siteId: "s1", subnets: [subnet("s1", "10.20.0.0/24")] },
      { ok: true, siteId: "s2", subnets: [] },
    ];
    const partial: SubnetFetch[] = [
      { ok: true, siteId: "s1", subnets: [subnet("s1", "10.20.0.0/24")] },
      { ok: false, siteId: "s2" }, // s2 might own 10.99.0.0/24 — we cannot say it does not.
    ];

    // The MATCHED row is unaffected: a positive answer stays knowable, because finding it required only the
    // site that answered.
    expect(attributeRanges(ranges, SITES, complete)[0].attribution.kind).toBe(
      "site",
    );
    expect(attributeRanges(ranges, SITES, partial)[0].attribution.kind).toBe(
      "site",
    );

    // The UNMATCHED row flips, and this is the whole point: "no site advertises this" is a claim about
    // having asked every site.
    expect(attributeRanges(ranges, SITES, complete)[1].attribution.kind).toBe(
      "unmatched",
    );
    expect(attributeRanges(ranges, SITES, partial)[1].attribution.kind).toBe(
      "unknown",
    );
  });

  it("renders the site id when the subnet names a site `listSites` did not return", () => {
    // Honest over reassuring: falling back to "Unknown site" would be indistinguishable from the `unknown`
    // arm, which means something completely different (we could not ask).
    const fanOut: SubnetFetch[] = [
      { ok: true, siteId: "s9", subnets: [subnet("s9", "10.20.0.0/24")] },
    ];
    const [row] = attributeRanges(["10.20.0.0/24"], SITES, fanOut);
    expect(row.attribution).toEqual({
      kind: "site",
      siteId: "s9",
      siteName: "s9",
    });
  });

  it("preserves the API's order and the exact served string, which is what a device receives", () => {
    const ranges = ["10.20.0.0/24", "10.10.0.0/16", "192.168.4.0/22"];
    const rows = attributeRanges(ranges, SITES, []);
    expect(rows.map((r) => r.range)).toEqual(ranges);
  });

  it("N=0 ranges yields no rows in every fan-out state", () => {
    expect(attributeRanges([], SITES, null)).toEqual([]);
    expect(attributeRanges([], [], [])).toEqual([]);
  });
});

describe("attributionLabel / attributionClass", () => {
  it("gives all four arms a distinct label — no two states read the same", () => {
    const labels = [
      attributionLabel({ kind: "site", siteId: "s1", siteName: "Sydney" }),
      attributionLabel({ kind: "loading" }),
      attributionLabel({ kind: "unknown" }),
      attributionLabel({ kind: "unmatched" }),
    ];
    expect(new Set(labels).size).toBe(4);
    // ⛔ AND NONE OF THEM IS BLANK. A blank cell is the reassuring-empty shape this union exists to prevent;
    // an arm that returned "" would still be four distinct values if the others differ.
    for (const l of labels) expect(l.trim()).not.toBe("");
  });

  it("recedes every non-answer and only every non-answer", () => {
    const answered = attributionClass({
      kind: "site",
      siteId: "s1",
      siteName: "Sydney",
    });
    for (const a of [
      { kind: "loading" } as const,
      { kind: "unknown" } as const,
      { kind: "unmatched" } as const,
    ])
      expect(attributionClass(a)).not.toBe(answered);
  });
});

describe("fanOutExceedsTripwire", () => {
  it("is false AT the threshold and true one past it", () => {
    // Both sides of the boundary. `>=` instead of `>` fails the first; a constant fails one or the other.
    expect(fanOutExceedsTripwire(FANOUT_TRIPWIRE)).toBe(false);
    expect(fanOutExceedsTripwire(FANOUT_TRIPWIRE + 1)).toBe(true);
    expect(fanOutExceedsTripwire(0)).toBe(false);
  });
});

describe("forwardsEmptyCopy", () => {
  it("never says forwards are unconfigured, in either branch: empty means UNREACHABLE", () => {
    // Proof the guard can fire, rather than a regex nobody has watched reject: the exact sentence it forbids.
    expect("No forwarded zones are configured.").toMatch(
      /\bno\b[^.]*\b(forwards?|zones?)\b[^.]*\bconfigured\b/i,
    );

    // The gate is the whole subtlety: a forward is withheld when its resolver is outside every routed range.
    // Copy claiming "none configured" sends an admin to configure one that already exists.
    for (const copy of [forwardsEmptyCopy(0), forwardsEmptyCopy(3)]) {
      // `[^.]*` deliberately: the SECOND sentence of the non-empty branch says zones may well BE configured,
      // which is the honest clarification. The banned claim is "no zones are configured" WITHIN one sentence.
      expect(copy).not.toMatch(/\bno\b[^.]*\b(forwards?|zones?)\b[^.]*\bconfigured\b/i);
      expect(copy.toLowerCase()).toContain("reachable");
    }
  });

  it("distinguishes 'nothing is routed' from 'nothing reachable' — the two have different fixes", () => {
    expect(forwardsEmptyCopy(0)).not.toBe(forwardsEmptyCopy(1));
    expect(forwardsEmptyCopy(0)).toMatch(/no ranges are routed/i);
  });
});

describe("sortForwards", () => {
  it("sorts by domain then resolver, and does not mutate its input", () => {
    const input = [
      { domain: "corp.local", resolver_ip: "10.20.0.53" },
      { domain: "aws.internal", resolver_ip: "10.10.0.2" },
      { domain: "corp.local", resolver_ip: "10.20.0.10" },
    ];
    const snapshot = JSON.stringify(input);
    expect(sortForwards(input).map((f) => `${f.domain}@${f.resolver_ip}`)).toEqual([
      "aws.internal@10.10.0.2",
      "corp.local@10.20.0.10",
      "corp.local@10.20.0.53",
    ]);
    expect(JSON.stringify(input)).toBe(snapshot);
  });
});
