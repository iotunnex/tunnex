import { cloneElement, isValidElement, useId } from "react";
import { createPortal } from "react-dom";
import type {
  ButtonHTMLAttributes,
  InputHTMLAttributes,
  ReactElement,
  ReactNode,
  SelectHTMLAttributes,
} from "react";

// A small, deliberate set of primitives — enough to compose the app's pages
// consistently without a heavyweight component library. Colors come only from the
// theme tokens (accent/ink/slate), so a palette swap restyles everything.

/**
 * ⛔ THE GLASS RECIPE, IN ONE PLACE. Every surface in the product composes from this constant.
 *
 * It was previously spelled out on `Stat` and NOT on `Panel`, so the stat row rendered as glass and every
 * panel below it rendered as flat plastic — in the same screenshot. A material defined per-component is a
 * material that WILL be half-applied, and the half that is missing reads as a rendering bug rather than a
 * missing class.
 *
 * `bg-surface` is TRANSLUCENT (`rgba(31,31,31,.72)`), and the blur needs the page's radial field behind it to
 * refract (index.css). Opaque fill or flat backdrop and the effect disappears entirely.
 *
 * NO INSET WHITE HIGHLIGHT LINE — the designer removed it explicitly. Do not reintroduce
 * `inset 0 1px 0 rgba(255,255,255,…)`.
 */
export const GLASS =
  "rounded-card border border-white/[.14] bg-surface shadow-card backdrop-blur-[24px] backdrop-saturate-[1.4]";

export function Button({
  variant = "primary",
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "primary" | "ghost" | "danger";
}) {
  const base =
    "inline-flex items-center justify-center rounded-md px-4 py-2 text-sm font-medium transition-colors disabled:opacity-50 disabled:pointer-events-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent-400";
  const variants = {
    primary: "bg-accent-500 text-white hover:bg-accent-600",
    ghost: "border border-white/10 text-slate-200 hover:bg-white/5",
    danger: "text-slate-400 hover:text-danger",
  } as const;
  return (
    <button
      className={`${base} ${variants[variant]} ${className}`}
      {...props}
    />
  );
}

export function Card({
  className = "",
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return <div className={`${GLASS} p-4 ${className}`}>{children}</div>;
}

export function Field({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  // Explicit id/htmlFor association (not just implicit wrapping) so the label
  // stays linked to the control even once helper/error text is added, and the
  // accessible name is exactly the label — not the concatenated subtree text.
  const id = useId();
  const control = isValidElement(children)
    ? cloneElement(children as ReactElement<{ id?: string }>, { id })
    : children;
  return (
    <div className="block">
      <label htmlFor={id} className="block text-sm text-slate-300">
        {label}
      </label>
      <span className="mt-1 block">{control}</span>
    </div>
  );
}

export function Input({
  className = "",
  ...props
}: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={`w-full rounded-md border border-white/10 bg-ink-900 px-3 py-2 text-sm text-white placeholder:text-slate-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent-400 ${className}`}
      {...props}
    />
  );
}

/** StatusDot: a small colored dot for online/offline/neutral state (semantic
 * tokens, deliberately not the brand accent). */
export function StatusDot({ tone }: { tone: "on" | "off" | "warn" }) {
  const cls = { on: "bg-ok", off: "bg-slate-600", warn: "bg-warn" }[tone];
  return <span className={`inline-block h-1.5 w-1.5 rounded-full ${cls}`} />;
}

export function ErrorText({ children }: { children: ReactNode }) {
  return children ? <p className="text-xs text-danger">{children}</p> : null;
}

// Select: themed <select>, promoted from the raw <select>+selectCls that pages rolled
// inline (S7.4a). Same border/bg/focus tokens as Input so the two read as one family.
export function Select({
  className = "",
  children,
  ...props
}: SelectHTMLAttributes<HTMLSelectElement> & { children: ReactNode }) {
  return (
    <select
      className={`w-full rounded-md border border-white/10 bg-ink-900 px-3 py-2 text-sm text-white focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent-400 ${className}`}
      {...props}
    >
      {children}
    </select>
  );
}

// Modal: the one generic overlay+dismiss shell (S7.4a), extracted from the
// OneTimeSecretModal structure but content-agnostic — reused for every create/edit
// form and the two confirm dialogs. Deliberately NOT a live "switch": consequential,
// confirm-gated actions must not wear switch clothing. Esc + backdrop-click dismiss;
// `danger` tints the title for the strong (zero-rules lockout) gate.
export function Modal({
  title,
  danger = false,
  onDismiss,
  children,
  actions,
}: {
  title: string;
  danger?: boolean;
  onDismiss: () => void;
  children: ReactNode;
  actions: ReactNode;
}) {
  // Dismiss on backdrop-click or the Cancel action only. Esc-to-dismiss was DROPPED after a
  // 3-finding churn (broken → too-global → focus-steal) on a nice-to-have that's also a
  // data-loss footgun on a form modal. If a11y later needs Esc, it returns as the full
  // designed dialog pattern (focus trap + first-field focus + panel listener), not a patch.
  //
  // ⛔ PORTALLED TO <body>, AND THIS IS NOT COSMETIC.
  //
  // `position: fixed` is relative to the VIEWPORT — unless an ancestor has `filter`, `transform`,
  // `perspective`, `will-change` or `backdrop-filter`, any of which makes that ancestor the containing block.
  // S14.4 gave `Card` the glass recipe, which includes `backdrop-filter` — and FIVE modals across FOUR screens
  // render inside a Card. Every one of them silently stopped being viewport-positioned: the overlay was
  // clipped to the card, and the card's own body sat on top of the modal's buttons, so clicks never landed.
  //
  // It surfaced as ONE Playwright click timing out with a Card listed as the intercepting element. It did not
  // surface in the component tier at all — jsdom has no layout engine, so a containing-block change is
  // invisible there, and a click-through of all twelve screens reported "nothing broken" because nothing
  // crashed and no content was lost.
  //
  // A portal is the correct fix independent of the cause: an overlay's position must never depend on WHERE IN
  // THE TREE it happens to be rendered.
  return createPortal(
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      role="dialog"
      aria-modal="true"
      aria-label={title}
      onClick={onDismiss}
    >
      <div
        className="w-full max-w-md rounded-card border border-white/10 bg-surface p-4 shadow-modal backdrop-blur-[24px] backdrop-saturate-[1.4]"
        onClick={(e) => e.stopPropagation()}
      >
        <h2
          className={`text-title font-semibold ${danger ? "text-danger" : "text-ink-heading"}`}
        >
          {title}
        </h2>
        <div className="mt-3 text-cell text-ink-body">{children}</div>
        <div className="mt-5 flex justify-end gap-2">{actions}</div>
      </div>
    </div>,
    document.body,
  );
}

// ── S14.3 SLICE A — STRUCTURAL PRIMITIVES, SEMANTIC BY CONSTRUCTION ─────────────────────────────────────────
//
// ⚠ THE MEASUREMENT THAT MADE THIS A DEFECT RATHER THAN A POLISH ITEM: this app contained ZERO `<table>`
// elements. Thirty-seven `.map()` calls rendered `<div>` rows.
//
// The cost was not cosmetic and it was not confined to the UI. Query rule 1 says query by ROLE — and
// `role="table"` / `row` / `cell` DID NOT EXIST ANYWHERE TO QUERY, so every wiring test in the component tier
// worked around the gap by MATCHING TEXT. Text matching is the most brittle query there is and the first thing
// a redesign breaks. A missing semantic primitive degrades the UI once and the TESTS OF THAT UI a second time,
// and the second cost is invisible from either side: the tests look like they work, the components look like
// they render (docs/laws.md).
//
// So these primitives ship WITH their consumers converted and the tier's assertions re-pointed at roles. A
// primitive that ships while its consumers keep the workaround has only half landed.

/** A named region. `aria-labelledby` is the point: an unnamed region cannot be found by role + name. */
export function Panel({
  title,
  actions,
  className = "",
  children,
}: {
  title: string;
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  const id = useId();
  return (
    // README: card padding 16, internal gap 10, title 600 13.5px. `flex-col` with the body in a `flex-1`
    // wrapper keeps every panel in a row the same height WITHOUT centring its content — the row stretches,
    // the content stays top-aligned. Centred content in a stretched panel is what makes a bento look
    // "overlapped": each panel floats its text at a different vertical position.
    <section
      aria-labelledby={id}
      className={`${GLASS} flex flex-col gap-2.5 p-4 ${className}`}
    >
      <div className="flex items-center justify-between gap-2">
        <h2 id={id} className="text-title font-semibold text-ink-heading">
          {title}
        </h2>
        {actions}
      </div>
      <div className="flex-1">{children}</div>
    </section>
  );
}

/**
 * A status badge.
 *
 * ⛔ THE TEXT IS THE STATUS; THE COLOUR IS AN ACCELERANT. A badge that says its state only in colour is
 * unreadable to a colour-blind user, invisible to a screen reader, and unqueryable by the tier — three
 * failures with one cause. `tone` may never be the only carrier of meaning, which is why `children` is
 * required rather than optional.
 *
 * `ok` REMAINS LIVENESS-ONLY (S4.4 decision f). The reservation scan in tokens.test.ts reads these use-sites.
 */
export function Badge({
  tone = "neutral",
  children,
}: {
  tone?: "ok" | "warn" | "danger" | "neutral";
  children: ReactNode;
}) {
  const cls = {
    ok: "border-ok/40 text-ok",
    warn: "border-warn/40 text-warn",
    danger: "border-danger/40 text-danger",
    neutral: "border-white/10 text-slate-400",
  }[tone];
  return (
    <span
      className={`inline-flex items-center rounded-full border px-2 py-0.5 text-xs ${cls}`}
    >
      {children}
    </span>
  );
}

/**
 * The empty state.
 *
 * ⚠ EMPTY IS NOT THE SAME AS FAILED, and this component may only ever express the first. Twelve hand-written
 * "No X yet." strings existed before it; the risk in unifying them is that a FAILED load starts borrowing the
 * empty wording, which is the reassuring-empty defect the `loadOne` law exists to prevent. A failed load
 * renders `LoadRetry`, never this.
 */
export function EmptyState({
  children,
  action,
}: {
  children: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="py-10">
      <p className="text-cell text-ink-tertiary">{children}</p>
      {action && <div className="mt-3">{action}</div>}
    </div>
  );
}

/** In-flight state. Announced, not merely drawn: a spinner nothing announces is invisible to a screen reader. */
export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <p role="status" className="py-10 text-cell text-ink-tertiary">
      {label}
    </p>
  );
}

/** A non-tabular collection. `<ul>/<li>`, so it is a list to the accessibility tree — never a table pretending. */
export function List({
  label,
  children,
}: {
  label: string;
  children: ReactNode;
}) {
  return (
    <ul aria-label={label} className="divide-y divide-white/5">
      {children}
    </ul>
  );
}

export function ListItem({ children }: { children: ReactNode }) {
  return <li className="py-3">{children}</li>;
}

export interface Column<T> {
  /** Stable key — React's key and the mutation-visible identity of the column. */
  key: string;
  /** The column's HEADER TEXT. Required: a `<th>` with no text is a cell the tier cannot name. */
  header: string;
  cell: (row: T) => ReactNode;
  /** Numeric/right-aligned columns. Presentation only — never a reason to drop the header. */
  numeric?: boolean;
}

/**
 * A real table.
 *
 * `<table>` + `<caption>` + `<thead>` + `<th scope="col">` + `<tbody>`, so the tier can ask for
 * `getByRole("table", { name })`, `getAllByRole("row")`, `getByRole("columnheader", { name })` — the queries
 * that were impossible in this app until now.
 *
 * THE CAPTION IS THE TABLE'S ACCESSIBLE NAME and it is REQUIRED. Two unnamed tables on one screen are two
 * `role="table"` matches with no way to tell them apart, which pushes the tier straight back to text matching.
 *
 * `caption` is visually hidden by default (`sr-only`) because the surrounding Panel usually shows the same
 * heading — hidden from sight, PRESENT in the accessibility tree. That is the correct direction of the
 * invisible-is-not-absent rule: absent to the eye, present to the machine, never the reverse.
 */
export function DataTable<T>({
  caption,
  columns,
  rows,
  rowKey,
  empty,
  failed,
}: {
  caption: string;
  columns: Array<Column<T>>;
  rows: T[];
  rowKey: (row: T) => string;
  /** What to render when there are GENUINELY zero rows. */
  empty: ReactNode;
  /**
   * ⛔ REQUIRED, AND REQUIRED ON PURPOSE — did the load that produced `rows` FAIL?
   *
   * An empty array means two different things and the difference is the whole point: "there are none" and
   * "we never found out". Rendering the second as the first is the REASSURING-EMPTY defect the `loadOne` law
   * exists to prevent, and on a roster or a rule list it is not a neutral emptiness — it is a claim about
   * who has access, made by a screen that never successfully read anything.
   *
   * THIS PROP IS NOT OPTIONAL BECAUSE A DEFAULT WOULD PICK THE DANGEROUS ANSWER SILENTLY. Every call site
   * must state which case it is in, and forgetting is a COMPILE ERROR rather than a review note — a guard
   * enforced by types beats one enforced by discipline (docs/laws.md).
   *
   * FOUND THE HARD WAY, IN THIS SLICE: converting Users to a table dropped the page's `&& !error` guard and
   * reintroduced exactly this defect — inside the slice whose own EmptyState comment warns against it. The
   * component tier caught it. That is the third time in this epic that a near miss landed inside the guard
   * written to prevent it, which is why this is a type and not a comment.
   *
   * When `failed` is true the table renders NOTHING: the page owns the retry affordance (LoadRetry), because
   * only the page knows what to retry.
   */
  failed: boolean;
}) {
  if (failed) return null;
  if (rows.length === 0) return <EmptyState>{empty}</EmptyState>;
  return (
    <div className="overflow-x-auto">
      <table className="w-full text-left text-sm">
        <caption className="sr-only">{caption}</caption>
        <thead>
          <tr className="text-xs uppercase tracking-wide text-slate-500">
            {columns.map((c) => (
              <th
                key={c.key}
                scope="col"
                className={`py-2 pr-4 font-medium ${c.numeric ? "text-right" : ""}`}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-white/5">
          {rows.map((r) => (
            <tr key={rowKey(r)}>
              {columns.map((c) => (
                <td
                  key={c.key}
                  className={`py-3 pr-4 align-top ${c.numeric ? "text-right" : ""}`}
                >
                  {c.cell(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
