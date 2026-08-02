import { useCallback, useEffect, useState } from "react";
import {
  api,
  loadOne,
  type Loaded,
  type Node,
  type Org,
} from "../lib/api";
import { Gateways as GatewayFleet } from "../components/Gateways";
import { LoadRetry } from "../components/LoadRetry";

// ── S14.6 SLICE 1 — THE PROMOTION, AND NOTHING ELSE ─────────────────────────────────────────────────────
//
// ⛔ THE FINDING THIS SLICE EXISTS FOR: Gateways was never a screen.
//
// `components/Gateways.tsx` is 458 lines of working fleet management — the table, the enrollment ceremony
// with its one-time join token, the revoke confirmation — and it was mounted in exactly ONE place: partway
// down `pages/Devices.tsx`. There was no `/gateways` route and no nav entry. **An operator reached the
// gateway fleet by opening Devices and scrolling.**
//
// That is the same class as EPIC 11's walk finding `backupctl` and `preflight` written, tested, named in two
// runbooks, and absent from the image: THE THING WORKS AND CANNOT BE FOUND.
//
// ⛔ AND THIS SLICE DELIBERATELY DOES NOT REDESIGN IT.
//
// The promotion and the section pass are different changes with different risks, and bundling them would put
// a 458-line move and a visual redesign in one diff where neither can be reviewed. Slice 1 moves the mount
// point and changes nothing a user sees except that the screen now EXISTS. Slice 2 does the handoff's
// layout — filter chips, the health-grouped list, the OpenVPN panel — against the columns we actually serve.
//
// A MOVE AND A REWRITE IN ONE COMMIT IS A DIFF NOBODY CAN READ.

export default function GatewaysPage() {
  const [org, setOrg] = useState<Org | null>(null);
  const [nodes, setNodes] = useState<Node[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);

  const reload = useCallback(async () => {
    setLoadError(null);
    const oRes = await loadOne(() => api.GET("/api/v1/organizations"));
    if (!oRes.ok) return setLoadError(oRes.error);
    const first = (oRes.data as Org[])[0];
    if (!first)
      return setLoadError("You are not a member of any organization yet.");
    setOrg(first);
    const nRes = (await loadOne(() =>
      api.GET("/api/v1/organizations/{orgId}/nodes", {
        params: { path: { orgId: first.id } },
      }),
    )) as Loaded<Node[]>;
    // ⛔ A FAILED LOAD IS NOT AN EMPTY FLEET. `[].length === 0` is how "we could not read the gateways"
    // becomes a confident "you have none" — on the screen whose whole job is telling you what is running.
    if (!nRes.ok) return setLoadError(nRes.error);
    setNodes(nRes.data);
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  return (
    <div className="flex flex-col gap-3.5">
      <div>
        <h1 className="text-[22px] font-semibold text-ink-heading">Gateways</h1>
        <p className="text-cell text-ink-tertiary">{org ? org.name : "…"}</p>
      </div>

      {loadError && <LoadRetry error={loadError} onRetry={reload} />}
      {!loadError && (org === null || nodes === null) && (
        <p className="text-cell text-ink-faint">Loading…</p>
      )}
      {!loadError && org && nodes && (
        <GatewayFleet org={org} nodes={nodes} onNodesChanged={reload} />
      )}
    </div>
  );
}
