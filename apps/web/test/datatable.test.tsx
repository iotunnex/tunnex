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

/**
 * ⛔ PAGINATION IS THREE MORE WAYS TO RENDER AN EMPTY TABLE OVER A FULL DATA SET, and every one of them
 * arrives by arithmetic rather than by a failed load — which is what makes them easy to ship. Narrowing
 * while deep in the list, resizing the page, and rows shrinking underneath all point the page index past
 * the end of the array.
 */
describe("DataTable — pagination", () => {
  const many = (n: number): Row[] =>
    Array.from({ length: n }, (_, i) => ({
      id: String(i),
      name: `row-${String(i).padStart(3, "0")}`,
      owner: i % 2 ? "ana@ex.com" : "bo@ex.com",
      state: "active",
    }));

  function paged(rows: Row[], pageSize?: number) {
    return render(
      <DataTable<Row>
        caption="Widgets"
        rows={rows}
        failed={false}
        rowKey={(r) => r.id}
        empty="No widgets exist."
        {...(pageSize === undefined ? {} : { pageSize })}
        columns={[
          { key: "name", header: "Name", sortValue: (r) => `${r.name} ${r.owner}`, cell: (r) => <span>{r.name}</span> },
        ]}
      />,
    );
  }

  it("shows one page, not everything, and says which page it is showing", () => {
    paged(many(60));
    expect(screen.getAllByRole("row")).toHaveLength(26); // 25 + the header
    expect(screen.getByText("Showing 1–25 of 60")).toBeTruthy();
    expect(screen.getByText("Page 1 of 3")).toBeTruthy();
  });

  it("pages forward and back, and the boundary buttons are disabled at the boundaries", () => {
    paged(many(60));
    expect(screen.getByRole("button", { name: "Previous" }).hasAttribute("disabled")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(bodyNames()[0]).toBe("row-025");
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 3 of 3")).toBeTruthy();
    // ⚠ The last page is SHORT, and that is not an empty page — 60 rows over 25 leaves 10.
    expect(screen.getAllByRole("row")).toHaveLength(11);
    expect(screen.getByRole("button", { name: "Next" }).hasAttribute("disabled")).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Previous" }));
    expect(screen.getByText("Page 2 of 3")).toBeTruthy();
  });

  it("⭐ FILTERING FROM A DEEP PAGE RETURNS TO PAGE ONE — the operator's own search must not read as empty", () => {
    // Without the reset: page 3 of a 60-row list, filter down to 30 matches, and slice(50, 75) is EMPTY.
    // A full result set renders as nothing, and the thing that produced it was the search itself.
    paged(many(60));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 3 of 3")).toBeTruthy();
    fireEvent.change(screen.getByRole("searchbox", { name: "Filter Widgets" }), {
      target: { value: "ana@ex.com" },
    });
    expect(bodyNames().length).toBeGreaterThan(0);
    expect(screen.getByText("Showing 1–25 of 30 (filtered from 60)")).toBeTruthy();
  });

  it("⭐ ROWS SHRINKING UNDER A DEEP PAGE CLAMPS INSTEAD OF RENDERING NOTHING", () => {
    // A revoke, a refetch, a sweep — the page index that was valid a moment ago now points past the end.
    // Clamped at RENDER, so there is no frame in which the stale index is used.
    const { rerender } = paged(many(60));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(screen.getByText("Page 3 of 3")).toBeTruthy();
    rerender(
      <DataTable<Row>
        caption="Widgets"
        rows={many(30)}
        failed={false}
        rowKey={(r) => r.id}
        empty="No widgets exist."
        columns={[
          { key: "name", header: "Name", sortValue: (r) => r.name, cell: (r) => <span>{r.name}</span> },
        ]}
      />,
    );
    expect(bodyNames().length).toBeGreaterThan(0);
    expect(screen.getByText("Page 2 of 2")).toBeTruthy();
  });

  it("⚠ NO PAGER WHEN EVERYTHING ALREADY FITS — a control that can only no-op implies there is more", () => {
    paged(many(5));
    expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
    expect(screen.queryByText(/^Page /)).toBeNull();
    expect(screen.getAllByRole("row")).toHaveLength(6);
  });

  it("⛔ pageSize={0} DISABLES PAGING ENTIRELY — for surfaces that already page server-side", () => {
    // AuditLog and AccessEvents fetch behind a keyset cursor. A second pager there would append rows the
    // operator cannot see and report a count describing neither the fetch nor the view.
    paged(many(60), 0);
    expect(screen.getAllByRole("row")).toHaveLength(61);
    expect(screen.queryByRole("button", { name: "Next" })).toBeNull();
  });

  it("changing rows-per-page returns to the first page", () => {
    paged(many(60));
    fireEvent.click(screen.getByRole("button", { name: "Next" }));
    expect(bodyNames()[0]).toBe("row-025");
    fireEvent.change(screen.getByRole("combobox", { name: "Rows per page" }), { target: { value: "50" } });
    expect(bodyNames()[0]).toBe("row-000");
    expect(screen.getByText("Showing 1–50 of 60")).toBeTruthy();
  });
});
