import { useEffect, useState } from "react";
import { api } from "../lib/api";
import { Button, Card, ErrorText, Input } from "./ui";

type Status = {
  state: "unlicensed" | "valid" | "expired" | "lapsed";
  tier: string;
  gateway_ceiling?: number | null;
  org_ceiling?: number | null;
  features: string[];
  expires_at?: string | null;
  grace_ends_at?: string | null;
  clock_went_backwards?: boolean;
};

/**
 * ⭐ AN OPERATOR WHO PASTES A KEY AND SEES NOTHING CANNOT TELL SUCCESS FROM SILENCE.
 *
 * So this always renders the CURRENT entitlement — tier, gateway ceiling, expiry — and re-renders it from
 * the install response. The change is visible in the same place the action happened.
 *
 * ⛔ AND "unlicensed" IS NOT AN ERROR STATE. A deployment with no key is a complete, supported Community
 * deployment. Rendering it as a problem would be a false claim about a working product.
 */
export function LicenceCard({
  orgId,
  canManage,
}: {
  orgId: string;
  canManage: boolean;
}) {
  const [status, setStatus] = useState<Status | null>(null);
  const [key, setKey] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void (async () => {
      const { data } = await api.GET("/api/v1/organizations/{orgId}/license", {
        params: { path: { orgId } },
      });
      if (data) setStatus(data as Status);
    })();
  }, [orgId]);

  async function install(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const { data, error: err } = await api.POST(
      "/api/v1/organizations/{orgId}/license",
      {
        params: { path: { orgId } },
        body: { key: key.trim() },
      },
    );
    setBusy(false);
    if (err) {
      // ⚠ The server's message names WHICH half was wrong and what to do — a truncated key and a key for
      // another deployment need opposite actions. Surfacing it verbatim rather than "invalid key".
      setError(
        (err as { error?: { message?: string } }).error?.message ??
          "That key was not accepted.",
      );
      return;
    }
    if (data) {
      setStatus(data as Status);
      setKey("");
    }
  }

  const ceiling = (n: number | null | undefined) =>
    n === null || n === undefined ? "unlimited" : String(n);

  return (
    <Card>
      <h2 className="text-title font-semibold text-ink-heading">Licence</h2>

      {status && (
        <dl className="mt-3 grid grid-cols-2 gap-x-6 gap-y-1.5 text-cell">
          <dt className="text-ink-tertiary">Tier</dt>
          <dd className="text-ink-body">{status.tier}</dd>
          <dt className="text-ink-tertiary">Gateways</dt>
          <dd className="text-ink-body">{ceiling(status.gateway_ceiling)}</dd>
          <dt className="text-ink-tertiary">Organizations</dt>
          <dd className="text-ink-body">{ceiling(status.org_ceiling)}</dd>
          {status.expires_at && (
            <>
              <dt className="text-ink-tertiary">Expires</dt>
              <dd className="text-ink-body">
                {status.expires_at.slice(0, 10)}
              </dd>
            </>
          )}
        </dl>
      )}

      {/* ⛔ EXPIRED IS NOT LAPSED, AND THE COPY MUST NOT CONFLATE THEM. Nothing stops at expiry. */}
      {status?.state === "expired" && (
        <p className="mt-3 text-explainer text-warn">
          This licence expired
          {status.grace_ends_at
            ? ` and its grace period ends ${status.grace_ends_at.slice(0, 10)}`
            : ""}
          . Nothing has stopped — everything keeps working until then.
        </p>
      )}
      {status?.state === "lapsed" && (
        <p className="mt-3 text-explainer text-warn">
          The grace period has ended, so this deployment is back to Community
          limits. Gateways and organizations already running are unaffected —
          only enrolling new ones is.
        </p>
      )}
      {status?.state === "unlicensed" && (
        <p className="mt-3 text-explainer text-ink-tertiary">
          No licence installed. This is the complete product on one gateway and
          one organization.
        </p>
      )}
      {status?.clock_went_backwards && (
        <p className="mt-2 text-explainer text-warn">
          This server's clock moved backwards. Licence dates may read
          incorrectly until it is corrected — nothing has been refused because
          of it.
        </p>
      )}

      {canManage && (
        <form onSubmit={install} className="mt-4 flex flex-col gap-2">
          <Input
            aria-label="Licence key"
            placeholder="tnxl_…"
            value={key}
            onChange={(e) => setKey(e.target.value)}
          />
          {error && <ErrorText>{error}</ErrorText>}
          {/* ⚠ Says the thing an operator most needs to know before pasting into a live system. */}
          <p className="text-explainer text-ink-tertiary">
            Takes effect immediately. No restart.
          </p>
          <Button type="submit" disabled={busy || !key.trim()}>
            {busy ? "Installing…" : "Install licence"}
          </Button>
        </form>
      )}
    </Card>
  );
}
