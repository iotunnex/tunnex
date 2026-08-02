import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor, cleanup } from "@testing-library/react";

afterEach(cleanup);

// ── S14.7 — ROUTED RANGES, THE WIRING TIER ──────────────────────────────────────────────────────────────
//
// This screen makes ONE derivation and it is the only reason it is worth more than a fixture echo: the
// endpoint does NOT serve `site_id`, so the SITE column is joined client-side from an N-site fan-out.
//
// A join whose inputs can arrive LATE, arrive PARTIALLY, or not arrive at all has three ways to lie, and all
// three produce the SAME innocent-looking cell:
//
//   in flight       -> "we have not asked yet"
//   fan-out failed  -> "we could not ask"
//   asked, no match -> "nobody advertises this"
//
// Rendered as a blank cell those are indistinguishable, and the third is the only one that is a fact. So the
// tests below are written against WHICH OF THE THREE the screen claims, not against the table rendering.
//
// Mocking is at the NETWORK boundary, and queries are by role + accessible name, per the tier's rules.

type Handler = (path: string, opts?: unknown) => unknown;
let handler: Handler;

vi.mock("../src/lib/api", async () => {
  const actual =
    await vi.importActual<typeof import("../src/lib/api")>("../src/lib/api");
  return {
    ...actual,
    apiErrorMessage: (_e: unknown, fallback: string) => fallback,
    api: { GET: vi.fn(async (p: string, o?: unknown) => handler(p, o)) },
  };
});

const { default: RoutedRanges } = await import("../src/pages/RoutedRanges");

const ORG = [{ id: "o1", name: "Acme" }];
const SITES = [
  { id: "s1", name: "Sydney" },
  { id: "s2", name: "Frankfurt" },
];

/** Deferred promise, so a test can hold the fan-out open and observe the in-flight render. */
function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

type Opts = { params?: { path?: Record<string, string> } };

/**
 * The default backend. `subnets` maps a site id to its subnet list, or to the string "fail" to make that one
 * site's fetch error while the others succeed.
 */
function backend(cfg: {
  ranges?: string[];
  forwards?: Array<{ domain: string; resolver_ip: string }>;
  sites?: typeof SITES | "fail";
  subnets?: Record<string, Array<Record<string, string>> | "fail">;
  holdSubnets?: Promise<unknown>;
}): Handler {
  return async (path: string, opts?: unknown) => {
    const siteId = (opts as Opts)?.params?.path?.siteId;
    if (path === "/api/v1/organizations") return { data: ORG };
    if (path.endsWith("/routed-ranges"))
      return {
        data: { ranges: cfg.ranges ?? [], forwards: cfg.forwards ?? [] },
      };
    if (path.endsWith("/sites"))
      return cfg.sites === "fail"
        ? { error: { error: { message: "sites down" } } }
        : { data: cfg.sites ?? SITES };
    if (path.endsWith("/subnets")) {
      if (cfg.holdSubnets) await cfg.holdSubnets;
      const entry = cfg.subnets?.[siteId ?? ""];
      if (entry === "fail") return { error: { error: { message: "boom" } } };
      return { data: entry ?? [] };
    }
    return { data: undefined, error: undefined };
  };
}

const approved = (siteId: string, cidr: string) => ({
  id: `${siteId}-${cidr}`,
  site_id: siteId,
  cidr,
  status: "approved",
});

beforeEach(() => {
  handler = backend({});
});

describe("RoutedRanges — the attribution join", () => {
  it("attributes a range to its site once the fan-out lands", async () => {
    handler = backend({
      ranges: ["10.20.0.0/24"],
      subnets: { s1: [approved("s1", "10.20.0.0/24")] },
    });
    render(<RoutedRanges />);
    expect(await screen.findByText("Sydney")).toBeTruthy();
  });

  it("⛔ NEVER claims 'no site' while the fan-out is still in flight", async () => {
    // THE DEFECT THIS FILE EXISTS FOR. The table renders from ONE request and the SITE column fills in later.
    // If the in-flight cell rendered as blank — or worse, as the unmatched copy — the screen would state a
    // FACT ("nobody advertises this") during the window before it has asked anyone.
    const gate = deferred<void>();
    handler = backend({
      ranges: ["10.20.0.0/24"],
      subnets: { s1: [approved("s1", "10.20.0.0/24")] },
      holdSubnets: gate.promise,
    });
    render(<RoutedRanges />);

    // The RANGE is already on screen — proving we are past the first request and genuinely mid-fan-out,
    // rather than asserting against a page that has not rendered at all (which would pass vacuously).
    expect(await screen.findByText("10.20.0.0/24")).toBeTruthy();
    expect(screen.queryByText(/no site advertises this/i)).toBeNull();
    expect(screen.getByText(/loading/i)).toBeTruthy();

    // AND THE OTHER SIDE OF THE SAME TWO-VALUED THING (mechanism ⑨): release the gate and the very same cell
    // must resolve. A screen permanently stuck on "Loading…" would pass the assertions above.
    gate.resolve();
    expect(await screen.findByText("Sydney")).toBeTruthy();
    await waitFor(() => expect(screen.queryByText(/^loading…$/i)).toBeNull());
  });

  it("⛔ says 'could not load' — NOT 'no site' — when a site's subnet fetch fails", async () => {
    handler = backend({
      ranges: ["10.99.0.0/24"],
      subnets: { s1: [], s2: "fail" },
    });
    render(<RoutedRanges />);
    expect(await screen.findByText(/could not load/i)).toBeTruthy();
    // The unmatched claim requires a COMPLETE census. s2 might own this range; we cannot say it does not.
    expect(screen.queryByText(/no site advertises this/i)).toBeNull();
  });

  it("states the degraded read at panel level, so partial attribution is not read as partial coverage", async () => {
    handler = backend({
      ranges: ["10.99.0.0/24"],
      subnets: { s1: [], s2: "fail" },
    });
    render(<RoutedRanges />);
    // Without this line a reader sees some rows attributed and some not, and concludes the difference is in
    // the data rather than in the read.
    expect(
      await screen.findByText(/1 site could not be read/i),
    ).toBeTruthy();
  });

  it("DOES say 'no site advertises this' when every site answered and none matched", async () => {
    // The inverse of the two tests above, and the reason they are not just asserting a constant: with a
    // complete census the screen is ALLOWED to make the negative claim, and must.
    handler = backend({ ranges: ["10.99.0.0/24"], subnets: { s1: [], s2: [] } });
    render(<RoutedRanges />);
    expect(await screen.findByText(/no site advertises this/i)).toBeTruthy();
    expect(screen.queryByText(/could not load/i)).toBeNull();
    expect(screen.queryByText(/could not be read/i)).toBeNull();
  });

  it("does not attribute a range to a PENDING subnet", async () => {
    // `/routed-ranges` is approved-only, so a pending subnet can never own a served range. Attributing one
    // would claim traffic flows to a LAN nobody approved.
    handler = backend({
      ranges: ["10.20.0.0/24"],
      subnets: {
        s1: [{ ...approved("s1", "10.20.0.0/24"), status: "pending" }],
      },
    });
    render(<RoutedRanges />);
    expect(await screen.findByText(/no site advertises this/i)).toBeTruthy();
    expect(screen.queryByText("Sydney")).toBeNull();
  });
});

describe("RoutedRanges — failure and emptiness", () => {
  it("⛔ a failed ranges read renders a RETRY, never an empty routing table", async () => {
    // On the screen that answers "does my LAN traffic go down the tunnel", `[]` for a fetch failure says NO
    // with total confidence. This is the Loaded<T> class, on the surface where it is most expensive.
    handler = async (path: string) =>
      path === "/api/v1/organizations"
        ? { data: ORG }
        : { error: { error: { message: "ranges down" } } };
    render(<RoutedRanges />);
    expect(
      await screen.findByRole("button", { name: /retry/i }),
    ).toBeTruthy();
    expect(screen.queryByText(/no lan ranges are routed/i)).toBeNull();
  });

  it("an empty range list names the PRECONDITION, and is distinct from a failure", async () => {
    handler = backend({ ranges: [] });
    render(<RoutedRanges />);
    // Empty is a first-class answer from this endpoint and the common case for a fresh org, so the copy has
    // to teach what produces a range rather than reading as something being broken.
    const empty = await screen.findByText(/no lan ranges are routed/i);
    expect(empty.textContent).toMatch(/approve/i);
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
  });

  it("a failed SITES read degrades attribution without blanking the ranges", async () => {
    // Attribution is second-class: the rows are already correct and already rendered. Losing the enrichment
    // must not lose the subject.
    handler = backend({ ranges: ["10.20.0.0/24"], sites: "fail" });
    render(<RoutedRanges />);
    expect(await screen.findByText("10.20.0.0/24")).toBeTruthy();
    expect(screen.getByText(/could not load/i)).toBeTruthy();
    expect(screen.queryByRole("button", { name: /retry/i })).toBeNull();
  });
});

describe("RoutedRanges — the DNS forward gate", () => {
  it("⛔ an empty forwards list never says 'none configured' — it says none REACHABLE", async () => {
    // The gate is the subtlety: a forward is withheld when its resolver sits outside every routed range.
    // "None configured" sends an admin to create one that already exists.
    handler = backend({ ranges: ["10.20.0.0/24"], forwards: [] });
    render(<RoutedRanges />);
    const empty = await screen.findByText(/no forwarded zones are currently/i);
    expect(empty.textContent).toMatch(/reachable/i);
    expect(empty.textContent).not.toMatch(
      /\bno\b[^.]*\b(forwards?|zones?)\b[^.]*\bconfigured\b/i,
    );
  });

  it("states the gate even when the list is NON-empty — a withheld zone is invisible here", async () => {
    // ⛔ THE ONE-SIDED-OBSERVATION TRAP, AVOIDED DELIBERATELY. It would be easy to explain the gate only in
    // the empty state. But the reader who most needs it is the one looking at a POPULATED list and wondering
    // why their zone is missing from it.
    handler = backend({
      ranges: ["10.20.0.0/24"],
      forwards: [{ domain: "corp.local", resolver_ip: "10.20.0.53" }],
    });
    render(<RoutedRanges />);
    expect(await screen.findByText("corp.local")).toBeTruthy();
    expect(
      screen.getByText(/only when its resolver falls inside a routed range/i),
    ).toBeTruthy();
  });
});
