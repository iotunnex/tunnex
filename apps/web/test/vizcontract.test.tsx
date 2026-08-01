import { describe, expect, it, afterEach } from "vitest";
import { render, screen, cleanup, within } from "@testing-library/react";
import { Donut, Histogram, NodeLink } from "../src/components/viz";

// S14.3 SLICE C — THE VISUALIZATION CONTRACT.
//
// A chart is the easiest place in a UI to assert a fact nobody measured: a zero baseline LOOKS like data, and
// "no denials" and "we could not read the denials" draw identically. Both known render-floor violations in
// this repo are charts — that is the pattern, not a coincidence — so the guards live on the PRIMITIVE.

afterEach(cleanup);

const SLICES = [
  { label: "seen in last 3 min", value: 3, tone: "ok" as const },
  { label: "not seen recently", value: 1, tone: "neutral" as const },
];
const SRC = { endpoint: "/api/v1/organizations/{orgId}/overview" };

describe("⛔ A FAILED LOAD DRAWS NOTHING — never an empty axis, never a flat line at zero", () => {
  it("[Donut] failed renders no figure and no empty message", () => {
    render(
      <Donut
        label="Gateway liveness"
        source={SRC}
        failed={true}
        slices={SLICES}
        empty="none"
      />,
    );
    expect(
      screen.queryByRole("figure", { name: "Gateway liveness" }),
    ).toBeNull();
    expect(screen.queryByText("none")).toBeNull();
  });

  it("[Histogram] failed renders nothing, even with bins in hand", () => {
    // Stale bins under a failed refresh present old data as current — the same lie one step quieter.
    render(
      <Histogram
        label="Verdicts"
        source={SRC}
        failed={true}
        bins={[{ label: "09", value: 4 }]}
        empty="none"
      />,
    );
    expect(screen.queryByRole("figure", { name: "Verdicts" })).toBeNull();
  });

  it("[NodeLink] failed renders nothing", () => {
    render(
      <NodeLink
        label="Topology"
        source={SRC}
        failed={true}
        nodes={[]}
        links={[]}
        empty="none"
      />,
    );
    expect(screen.queryByRole("figure", { name: "Topology" })).toBeNull();
  });
});

describe("ZERO DATA SAYS SO — it does not draw an empty chart", () => {
  it("[Donut] a zero total renders the empty message, not a 0%% ring", () => {
    render(
      <Donut
        label="Gateway liveness"
        source={SRC}
        failed={false}
        slices={[{ label: "online", value: 0, tone: "ok" }]}
        empty="No gateways enrolled yet."
      />,
    );
    expect(screen.getByText("No gateways enrolled yet.")).toBeTruthy();
    expect(
      screen.queryByRole("figure", { name: "Gateway liveness" }),
    ).toBeNull();
  });
});

describe("⛔ A ROADMAP CHART RENDERS ITS HONEST STATE — never a plausible drawing", () => {
  it("says it is not available and why, and draws no figure", () => {
    // A greyed-out sample is still a picture. "Fleet risk" and "Site-Link Throughput" are the two known
    // violations, and both would have shipped as pictures with nothing behind them.
    render(
      <Histogram
        label="Site-link throughput"
        source={{
          roadmap: true,
          why: "no time-series endpoint exists, and the spec forbids summing the byte gauges",
        }}
        failed={false}
        bins={[]}
        empty="none"
      />,
    );
    const note = screen.getByRole("note");
    expect(note.textContent).toMatch(/isn.t available yet/);
    expect(note.textContent).toMatch(/spec forbids/);
    expect(
      screen.queryByRole("figure", { name: "Site-link throughput" }),
    ).toBeNull();
  });
});

describe("THE NUMBERS ARE TEXT, NOT ONLY GEOMETRY", () => {
  it("[Donut] every slice's value and label is readable", () => {
    // An SVG arc is unreadable to a screen reader, unqueryable by the tier, and ambiguous to anyone who
    // cannot distinguish the colours — three failures with one cause.
    render(
      <Donut
        label="Gateway liveness"
        source={SRC}
        failed={false}
        slices={SLICES}
        empty="none"
      />,
    );
    const fig = screen.getByRole("figure", { name: "Gateway liveness" });
    expect(within(fig).getByText("3")).toBeTruthy();
    expect(within(fig).getByText(/seen in last 3 min/)).toBeTruthy();
  });

  it("[NodeLink] a DOWN link is stated in words, not carried by a red line alone", () => {
    render(
      <NodeLink
        label="Topology"
        source={SRC}
        failed={false}
        nodes={[
          { id: "a", label: "hub-syd", kind: "hub" },
          { id: "b", label: "spoke-wus", kind: "spoke" },
        ]}
        links={[{ from: "a", to: "b", healthy: false }]}
        empty="none"
      />,
    );
    expect(screen.getByText(/a ↔ b down/)).toBeTruthy();
  });
});

describe("⛔ A GAP IS DRAWN AS A GAP — 'no data' must never render as zero", () => {
  it("a gap bin is labelled 'no data', distinctly from a zero-count bin", () => {
    // AccessEvent.decision carries `gap` as a first-class enum value precisely because the agent can know it
    // did not observe a window. Drawing that as a zero-height bar would make "no denials" and "we did not
    // see" identical — the reassuring-empty defect with an axis on it.
    render(
      <Histogram
        label="Verdicts"
        source={SRC}
        failed={false}
        bins={[
          { label: "09", value: 5 },
          { label: "10", value: 0 },
          { label: "11", gap: true, value: 0 },
        ]}
        empty="none"
      />,
    );
    expect(screen.getByLabelText("11: no data")).toBeTruthy();
    expect(screen.getByLabelText("10: 0")).toBeTruthy();
    // The two must not collapse into the same rendering.
    expect(screen.queryByRole("figure", { name: "11: 0" })).toBeNull();
  });
});
