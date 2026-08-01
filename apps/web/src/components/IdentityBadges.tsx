import { useEffect, useState } from "react";
import {
  api,
  loadOne,
  type Loaded,
  type Member,
  type Meta,
  type Org,
} from "../lib/api";
import { roleFromMembers } from "../lib/policyview";
import { useAuth } from "../lib/auth";
import { Badge } from "./ui";

// S14.4 — EDITION AND ROLE AS READ-ONLY BADGES.
//
// ⛔ THE WIREFRAME DREW THESE AS TOGGLES (FREE/ENTERPRISE and ADMIN/USER). They are DEMO CONTROLS, and the
// founder ruled them out: A USER CANNOT SWITCH THEIR OWN EDITION OR ROLE. Rendering a toggle would offer a
// privilege the product does not grant — and a control that looks interactive and is not is a worse lie than
// a missing control, because the user forms a plan around it.
//
// So these are <span>s carrying TEXT. Not buttons, not selects, not anything focusable.
//
// EDITION READS THROUGH THE ONE GATING SEAM — `/meta`'s `edition`, the same value that decides whether
// enterprise surfaces exist. Never a second source, so S12.1 rewrites one thing rather than hunting copies.
// ROLE reads the resolved role from the roster, which is where Users.tsx already gets it.

export function IdentityBadges() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  const [edition, setEdition] = useState<string | null>(null);
  const [role, setRole] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const m = (await loadOne(() => api.GET("/api/v1/meta"))) as Loaded<Meta>;
      if (!cancelled && m.ok) setEdition(m.data.edition);
      const o = (await loadOne(() =>
        api.GET("/api/v1/organizations"),
      )) as Loaded<Org[]>;
      if (!o.ok || !o.data[0] || cancelled) return;
      const mem = (await loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/members", {
          params: { path: { orgId: o.data[0]!.id } },
        }),
      )) as Loaded<Member[]>;
      const resolved = roleFromMembers(mem, myId);
      if (!cancelled && !resolved.failed && resolved.role)
        setRole(resolved.role);
    })();
    return () => {
      cancelled = true;
    };
  }, [myId]);

  // ABSENT UNTIL KNOWN, same rule as the nav counts. A badge reading "free" because /meta failed would
  // misstate what the org has paid for — and unlike a count, nobody would think to doubt it.
  return (
    <span className="flex items-center gap-2">
      {edition && <Badge tone="neutral">{edition}</Badge>}
      {role && <Badge tone="neutral">{role}</Badge>}
    </span>
  );
}
