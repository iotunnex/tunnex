import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, apiErrorMessage, type OrgOverview } from "../lib/api";
import { relativeAge } from "../lib/format";
import { Card, ErrorText } from "../components/ui";

export default function Dashboard() {
  const [orgName, setOrgName] = useState("");
  const [data, setData] = useState<OrgOverview | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const { data: orgs, error: orgErr } = await api.GET("/api/v1/organizations");
        if (cancelled) return;
        if (orgErr) return setError(apiErrorMessage(orgErr, "Could not load your organizations."));
        const org = orgs?.[0];
        if (!org) return setError("You are not a member of any organization yet.");
        setOrgName(org.name);
        const { data: ov, error: ovErr } = await api.GET("/api/v1/organizations/{orgId}/overview", {
          params: { path: { orgId: org.id } },
        });
        if (cancelled) return;
        if (ovErr || !ov) return setError(apiErrorMessage(ovErr, "Could not load the overview."));
        setData(ov);
      } catch {
        if (!cancelled) setError("Could not reach the API.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Overview</h1>
      <p className="text-sm text-slate-400">{orgName || "…"}</p>
      <ErrorText>{error}</ErrorText>

      {data && (
        <>
          <div className="mt-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
            <Stat label="Members" value={data.members} />
            <Stat label="Devices" value={data.devices} />
            <Stat label="Gateways" value={data.nodes} />
            {/* Honest label: online is derived from last-handshake recency (S3.6).
                This liveness tile is the ONE place the reserved status-green
                (ok token) appears on the dashboard — and only when >0. */}
            <Stat label="Seen in last 3 min" value={data.online} tone={data.online > 0 ? "ok" : undefined} />
          </div>

          {/* Empty states double as the onboarding funnel for a fresh org. */}
          {data.nodes === 0 && (
            <Card className="mt-4">
              <p className="text-sm text-slate-300">No gateway enrolled yet.</p>
              <p className="mt-1 text-xs text-slate-500">Enroll a tunnex-node agent to start serving WireGuard peers.</p>
            </Card>
          )}
          {data.devices === 0 && data.nodes > 0 && (
            <Card className="mt-4">
              <p className="text-sm text-slate-300">No devices yet.</p>
              <Link to="/devices" className="mt-1 inline-block text-xs text-accent-400 hover:text-accent-500">
                Add your first device →
              </Link>
            </Card>
          )}

          <h2 className="mt-8 text-sm font-semibold text-slate-300">Recent activity</h2>
          <ul className="mt-3 space-y-2">
            {data.recent_activity.map((a, i) => (
              <li key={i} className="flex items-center justify-between rounded-lg border border-white/5 bg-ink-800 px-4 py-2.5">
                <span className="text-sm text-slate-300">
                  <span className="font-mono text-xs text-slate-400">{a.action}</span>
                  {a.target_type && <span className="ml-2 text-xs text-slate-500">{a.target_type}</span>}
                </span>
                <span className="text-xs text-slate-500">{relativeAge(a.created_at)}</span>
              </li>
            ))}
            {data.recent_activity.length === 0 && <li className="text-sm text-slate-500">No activity yet.</li>}
          </ul>
        </>
      )}
    </div>
  );
}

function Stat({ label, value, tone }: { label: string; value: number; tone?: "ok" }) {
  return (
    <div className="rounded-xl border border-white/5 bg-ink-800 p-4">
      <div className={`font-mono text-2xl font-semibold ${tone === "ok" ? "text-ok" : "text-white"}`}>{value}</div>
      <div className="mt-1 text-xs text-slate-500">{label}</div>
    </div>
  );
}
