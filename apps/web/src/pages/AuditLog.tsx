import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, apiErrorMessage, type AuditLogEntry, type Member, type Org } from "../lib/api";
import { relativeAge } from "../lib/format";
import { Button, Card, ErrorText, Field, Input } from "../components/ui";

const PAGE = 50;

const selectCls =
  "rounded-md border border-white/10 bg-ink-900 px-2 py-1 text-sm text-white focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent-400";

// Filters applied to the feed. Empty string = unset.
type Filters = { actor: string; action: string; from: string; to: string };
const NO_FILTERS: Filters = { actor: "", action: "", from: "", to: "" };

// A type=date value is a calendar day ("YYYY-MM-DD"); parse it in the user's LOCAL
// zone (no trailing Z) and cover the whole day so `created_at <= to` is inclusive.
const dayStart = (d: string) => new Date(`${d}T00:00:00`).toISOString();
const dayEnd = (d: string) => new Date(`${d}T23:59:59.999`).toISOString();

export default function AuditLog() {
  const [org, setOrg] = useState<Org | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [entries, setEntries] = useState<AuditLogEntry[]>([]);
  // `filters` is the editing state; `applied` is the set that produced the current
  // list — "Load more" must page with `applied`, never mid-edit `filters`, or the
  // keyset cursor (from the applied list) mixes with a different filter set.
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const [applied, setApplied] = useState<Filters>(NO_FILTERS);
  const [more, setMore] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Generation token: each fetch bumps it; a response whose token is stale (a
  // newer fetch started, or the component unmounted) is discarded — so out-of-
  // order responses can't leave a stale page as the final list.
  const reqSeq = useRef(0);

  // fetchPage loads from the top (cursor omitted) or appends after `cursor` (the
  // last entry's created_at + id — keyset, not offset). It fetches PAGE+1 and
  // shows PAGE: the extra row is how we know there's a next page without a count
  // (page.length === PAGE would dead-click at exact multiples).
  async function fetchPage(orgId: string, f: Filters, cursor?: AuditLogEntry) {
    const seq = ++reqSeq.current;
    setBusy(true);
    const { data, error } = await api.GET("/api/v1/organizations/{orgId}/audit-logs", {
      params: {
        path: { orgId },
        query: {
          actor: f.actor || undefined,
          action: f.action || undefined,
          from: f.from ? dayStart(f.from) : undefined,
          to: f.to ? dayEnd(f.to) : undefined,
          cursor_ts: cursor?.created_at,
          cursor_id: cursor?.id,
          limit: PAGE + 1,
        },
      },
    });
    if (seq !== reqSeq.current) return; // superseded by a newer fetch / unmounted
    setBusy(false);
    if (error) return setError(apiErrorMessage(error, "Could not load the audit log."));
    const fetched = data ?? [];
    const page = fetched.slice(0, PAGE); // drop the has-more probe row
    setEntries((prev) => (cursor ? [...prev, ...page] : page));
    setMore(fetched.length > PAGE);
    setApplied(f); // this filter set now owns the displayed list + its cursor
  }

  useEffect(() => {
    reqSeq.current++; // invalidate any in-flight fetch on unmount
    let cancelled = false;
    (async () => {
      const { data: orgs, error: orgErr } = await api.GET("/api/v1/organizations");
      if (cancelled) return;
      if (orgErr) return setError(apiErrorMessage(orgErr, "Could not load your organizations."));
      const first = orgs?.[0];
      if (!first) return setError("You are not a member of any organization yet.");
      setOrg(first);
      // Actor filter is org-scoped BY CONSTRUCTION: the dropdown offers only this
      // org's members (the server enforces org-scoping too).
      const { data: ms } = await api.GET("/api/v1/organizations/{orgId}/members", { params: { path: { orgId: first.id } } });
      if (!cancelled) setMembers(ms ?? []);
      if (!cancelled) await fetchPage(first.id, NO_FILTERS);
    })();
    return () => {
      cancelled = true;
      reqSeq.current++; // discard a fetchPage response that resolves post-unmount
    };
  }, []);

  function applyFilters(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (org) void fetchPage(org.id, filters); // from the top with the new filters
  }

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Audit log</h1>
      <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>
      <ErrorText>{error}</ErrorText>

      <form onSubmit={applyFilters} className="mt-6">
        <Card>
          <div className="flex flex-wrap items-end gap-3">
            <label className="text-sm text-slate-300">
              <span className="block text-xs text-slate-500">Actor</span>
              <select className={`mt-1 ${selectCls}`} value={filters.actor} onChange={(e) => setFilters((f) => ({ ...f, actor: e.target.value }))}>
                <option value="">Anyone</option>
                {members.map((m) => (
                  <option key={m.user_id} value={m.user_id}>
                    {m.name || m.email}
                  </option>
                ))}
              </select>
            </label>
            <div className="w-40">
              <Field label="Action">
                <Input value={filters.action} onChange={(e) => setFilters((f) => ({ ...f, action: e.target.value }))} placeholder="e.g. device.created" />
              </Field>
            </div>
            <label className="text-sm text-slate-300">
              <span className="block text-xs text-slate-500">From</span>
              <input type="date" className={`mt-1 ${selectCls}`} value={filters.from} onChange={(e) => setFilters((f) => ({ ...f, from: e.target.value }))} />
            </label>
            <label className="text-sm text-slate-300">
              <span className="block text-xs text-slate-500">To</span>
              <input type="date" className={`mt-1 ${selectCls}`} value={filters.to} onChange={(e) => setFilters((f) => ({ ...f, to: e.target.value }))} />
            </label>
            <Button type="submit" disabled={busy}>
              Apply
            </Button>
          </div>
        </Card>
      </form>

      <ul className="mt-4 space-y-2">
        {entries.map((a) => (
          <li key={a.id} className="rounded-lg border border-white/5 bg-ink-800 px-4 py-2.5">
            <div className="flex items-center justify-between">
              <span className="font-mono text-xs text-slate-300">{a.action}</span>
              <span className="text-xs text-slate-500">{relativeAge(a.created_at)}</span>
            </div>
            <div className="mt-1 text-xs text-slate-500">
              {a.actor_id ? `actor ${actorName(members, a.actor_id)}` : "system"}
              {a.target_type && <span className="ml-2">· {a.target_type}</span>}
              {a.details && Object.keys(a.details).length > 0 && (
                <span className="ml-2 font-mono text-slate-600">{JSON.stringify(a.details)}</span>
              )}
            </div>
          </li>
        ))}
        {entries.length === 0 && !error && <li className="text-sm text-slate-500">No audit events yet.</li>}
      </ul>

      {more && (
        <div className="mt-4">
          <Button variant="ghost" disabled={busy} onClick={() => org && fetchPage(org.id, applied, entries[entries.length - 1])}>
            {busy ? "Loading…" : "Load more"}
          </Button>
        </div>
      )}
    </div>
  );
}

// actorName resolves an actor id to a display name from the roster (falls back to
// the short id for actors no longer on the roster, e.g. removed members).
function actorName(members: Member[], id: string): string {
  const m = members.find((mm) => mm.user_id === id);
  return m ? m.name || m.email : id.slice(0, 8);
}
