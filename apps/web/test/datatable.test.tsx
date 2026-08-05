import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen, fireEvent, within } from "@testing-library/react";
import { DataTable } from "../src/components/ui";

afterEach(cleanup);

type Row = { id: string; name: string; owner: string; state: string };

const ROWS: Row[] = [
  { id: "1", name: "zebra", owner: "ana@ex.com", state: "revoked" },
  { id: "2", name: "alpha", owner: "bo@ex.com", state: "active" },
  { id: "3", name: "mango", owner: "ana@ex.com", state: "active" },
];

function table(rows: Row[] = ROWS, failed = false) {
  return render(
    <DataTable<Row>
      caption="Widgets"
      rows={rows}
      failed={failed}
      rowKey={(r) => r.id}
      empty="No widgets exist."
      columns={[
        {
          key: "name",
          header: "Name",
          // The owner is searchable although this column never displays it.
          sortValue: (r) => `${r.name} ${r.owner}`,
          cell: (r) => <span>{r.name}</span>,
        },
        // ⛔ The state's TEXT lives in sortValue because the cell renders it as a styled element.
        { key: "state", header: "State", sortValue: (r) => r.state, cell: (r) => <em>{r.state}</em> },
        { key: "plain", header: "Plain", cell: () => <span>x</span> },
      ]}
    />,
  );
}

const bodyNames = () =>
  screen.getAllByRole("row").slice(1).map((r) => within(r).getAllByRole("cell")[0].textContent);

describe("DataTable — scannability", () => {
  it("⛔ A SORTABLE HEADER'S NAME IS STILL ITS HEADER", () => {
    // The sort indicator is an SVG precisely so it contributes no text. A character glyph would make this
    // column's name "Name↕" and silently break every query and test that names a column — which is how it
    // was caught: three existing suites went red at once.
    table();
    expect(screen.getByRole("columnheader", { name: "Name" })).toBeTruthy();
    const headers = screen.getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).toEqual(["Name", "State", "Plain"]);
  });

  it("sorts ascending, then descending, and announces which via aria-sort", () => {
    table();
    const btn = within(screen.getByRole("columnheader", { name: "Name" })).getByRole("button");
    fireEvent.click(btn);
    expect(bodyNames()).toEqual(["alpha", "mango", "zebra"]);
    expect(screen.getByRole("columnheader", { name: "Name" }).getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(btn);
    expect(bodyNames()).toEqual(["zebra", "mango", "alpha"]);
    expect(screen.getByRole("columnheader", { name: "Name" }).getAttribute("aria-sort")).toBe("descending");
  });

  it("⚠ A COLUMN WITHOUT sortValue IS NOT SORTABLE — and is still a real header", () => {
    // Without this, "every header is a button" would pass the test above while making an unsortable column
    // click to nothing, which is a control that lies about what it does.
    table();
    const plain = screen.getByRole("columnheader", { name: "Plain" });
    expect(within(plain).queryByRole("button")).toBeNull();
  });

  it("filters on sortValue, INCLUDING text the cell never shows", () => {
    table();
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter Widgets" }), {
      target: { value: "ana@ex.com" },
    });
    // Two rows share that owner, and neither cell renders it.
    expect(bodyNames()).toEqual(["zebra", "mango"]);
    expect(screen.getByText("2 of 3")).toBeTruthy();
  });

  it("⭐ finds a row by a state its cell renders as a styled element, not as plain text", () => {
    table();
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter Widgets" }), {
      target: { value: "revoked" },
    });
    expect(bodyNames()).toEqual(["zebra"]);
  });

  it("⛔ A FILTER THAT MATCHES NOTHING IS NOT THE SAME CLAIM AS HAVING NOTHING", () => {
    // THE WHOLE POINT. `empty` says none exist; a filter miss says none match. Rendering the second as the
    // first tells an operator a resource does not exist when it is one keystroke away — a new way to
    // manufacture the reassuring empty on a component whose `failed` prop exists because of that class.
    table();
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter Widgets" }), {
      target: { value: "nothing-matches-this" },
    });
    expect(screen.queryByText("No widgets exist.")).toBeNull();
    expect(screen.getByText(/No widgets match/)).toBeTruthy();
    // ⚠ And the way back is offered, with the true total — a dead end would leave the operator believing it.
    expect(screen.getByText(/see all 3/)).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Clear filter" }));
    expect(bodyNames()).toHaveLength(3);
  });

  it("genuinely zero rows renders the empty copy, and a FAILED load renders nothing at all", () => {
    // The three states, asserted apart. A failed load must not borrow either emptiness — the page owns retry.
    const { unmount } = table([]);
    expect(screen.getByText("No widgets exist.")).toBeTruthy();
    unmount();
    table(ROWS, true);
    expect(screen.queryByRole("table")).toBeNull();
    expect(screen.queryByText("No widgets exist.")).toBeNull();
  });

  it("⚠ SORTING DOES NOT MUTATE THE CALLER'S ARRAY", () => {
    // `rows` is the page's state. Sorting in place would reorder it under React and make the next render
    // disagree with the data the page thinks it holds.
    const rows = [...ROWS];
    render(
      <DataTable<Row>
        caption="Copy check"
        rows={rows}
        failed={false}
        rowKey={(r) => r.id}
        empty="none"
        defaultSortKey="name"
        columns={[{ key: "name", header: "Name", sortValue: (r) => r.name, cell: (r) => <span>{r.name}</span> }]}
      />,
    );
    expect(rows.map((r) => r.name)).toEqual(["zebra", "alpha", "mango"]);
  });
});
