import { useCallback, useEffect, useState } from "react";
import { useLocation } from "react-router-dom";
import {
  api,
  loadOne,
  type Loaded,
  type Node,
  type Org,
  type Site,
  type Device,
} from "./api";
import {
  FAILED,
  INITIAL_NAV_COUNTS,
  NAV_COUNT_REFRESH_MS,
  countFrom,
  type NavCounts,
} from "./navcounts";

// S14.4 — the shell's ONE data dependency, owned in ONE place.
//
// AppShell owned no data before this. Giving it some is a real cost, so it is confined: one hook, one set of
// counts, four sources, and every source resolves INDEPENDENTLY. A failure in one never blanks another —
// which is the same reasoning that kept the six stat cards off an aggregated endpoint.
//
// Each source goes through `loadOne`, so a failed fetch produces FAILED rather than an empty array whose
// length is zero. `[].length === 0` is exactly how a failure becomes a confident `0` in permanent chrome.

const isOnline = (n: Node) => {
  if (!n.last_seen_at) return false;
  // Same recency window the Overview's `online` count uses (S3.6). Derived from LAST HANDSHAKE, not a session
  // — which is why the label everywhere says "seen recently", never "online".
  return Date.now() - new Date(n.last_seen_at).getTime() < 3 * 60 * 1000;
};

export function useNavCounts(): NavCounts {
  const [counts, setCounts] = useState<NavCounts>(INITIAL_NAV_COUNTS);
  const location = useLocation();

  const refresh = useCallback(async () => {
    const orgRes = (await loadOne(() =>
      api.GET("/api/v1/organizations"),
    )) as Loaded<Org[]>;
    if (!orgRes.ok || !orgRes.data[0]) {
      // No org, or the org list failed: every count is UNKNOWN. Not zero — we did not learn that there are
      // none, we failed to learn anything.
      setCounts({
        gatewaysOnline: FAILED,
        gatewaysTotal: FAILED,
        sites: FAILED,
        devices: FAILED,
      });
      return;
    }
    const orgId = orgRes.data[0].id;

    const [nodes, sites, devices] = await Promise.all([
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/nodes", {
          params: { path: { orgId } },
        }),
      ) as Promise<Loaded<Node[]>>,
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/sites", {
          params: { path: { orgId } },
        }),
      ) as Promise<Loaded<Site[]>>,
      loadOne(() =>
        api.GET("/api/v1/organizations/{orgId}/devices", {
          params: { path: { orgId } },
        }),
      ) as Promise<Loaded<Device[]>>,
    ]);

    setCounts({
      gatewaysTotal: countFrom(nodes, (n) => n.length),
      gatewaysOnline: countFrom(nodes, (n) => n.filter(isOnline).length),
      sites: countFrom(sites, (r) => r.length),
      devices: countFrom(devices, (r) => r.length),
    });
  }, []);

  // Refresh on ROUTE CHANGE — the case that actually matters, because the user just did something and moved.
  useEffect(() => {
    void refresh();
  }, [refresh, location.pathname]);

  // Plus a SLOW interval, so a count left on screen does not become a remembered number wearing a live badge.
  useEffect(() => {
    const t = setInterval(() => void refresh(), NAV_COUNT_REFRESH_MS);
    return () => clearInterval(t);
  }, [refresh]);

  return counts;
}
