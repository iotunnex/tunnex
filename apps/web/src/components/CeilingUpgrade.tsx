import { Link } from "react-router-dom";

/**
 * ⛔ THE MOMENT A CUSTOMER WOULD HAVE PAID, AND THE PRODUCT USED TO SAY NOTHING.
 *
 * `gateway_limit_reached` and `org_limit_reached` are the only two refusals in this product that mean
 * "you have outgrown what you have". Until now both surfaced as a generic red error string: correct,
 * unactionable, and offering no route. The operator was told no at the exact instant they were deciding
 * to buy, and handed nowhere to go.
 *
 * > ## ⭐ **A LIMIT WITHOUT A ROUTE IS A DEAD END. A LIMIT WITH ONE IS A PRICE.**
 *
 * ⚠ IT NAMES THE BAND AND THE CEILING BECAUSE THE SERVER ALREADY DOES. The refusal message from the API is
 * rendered verbatim above these links — it says which band, which ceiling, how many exist, and that
 * nothing running is affected. This component adds only what the server cannot: navigation.
 *
 * ⛔ AND IT IS NOT AN UPSELL BANNER. It appears ONLY when a refusal has actually happened. A permanent
 * "upgrade now" on a screen the customer is using correctly teaches them to stop reading the screen.
 */
export function CeilingUpgrade({
  /** The server's own refusal text — already names band, ceiling, and what is unaffected. */
  message,
  /** Which limit was hit. Only changes the wording of the route, never whether one is offered. */
  kind,
}: {
  message: string;
  kind: "gateway" | "organization";
}) {
  const what = kind === "gateway" ? "more gateways" : "more organizations";
  return (
    <div className="mt-3 rounded-card border border-warn/30 bg-warn/5 p-3">
      <p className="text-cell text-ink-body">{message}</p>
      <div className="mt-2.5 flex flex-wrap items-center gap-x-4 gap-y-1.5">
        {/* ⭐ INSTALL COMES FIRST, DELIBERATELY. A customer who already HOLDS a key — bought minutes ago,
            or sitting in an inbox from a trial request — is one paste away from being unblocked, and
            sending them to a request form they do not need is the more annoying of the two wrong orders. */}
        <Link
          to="/settings#licence"
          className="text-cell font-medium text-accent hover:underline"
        >
          Install a licence key
        </Link>
        {/* ⚠ EXTERNAL, AND IT SAYS SO. The request flow lives on tunnex.io, not in the product — a
            deployment is air-gappable and must never depend on reaching us, so this is a link a human
            follows, never a call the product makes. */}
        <a
          href="https://tunnex.io/trial"
          target="_blank"
          rel="noreferrer"
          className="text-cell font-medium text-accent hover:underline"
        >
          Request a licence for {what} ↗
        </a>
      </div>
    </div>
  );
}

/**
 * ceilingKind maps an API error code to the limit it refers to, or null when the error is something else.
 *
 * ⛔ CODE, NEVER PROSE. Matching on the message would break the first time the wording improved — and the
 * wording is meant to improve, because it is the part the operator reads.
 */
export function ceilingKind(
  code: string | null | undefined,
): "gateway" | "organization" | null {
  if (code === "gateway_limit_reached") return "gateway";
  if (code === "org_limit_reached") return "organization";
  return null;
}
