import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, loadOne, type Loaded } from "../lib/api";
import {
  attributionNote, enrolmentKind, NO_AGENTS,
  UNDETERMINED_DETAIL, UNDETERMINED_LABEL, sortAgents, type AgentRow,
} from "../lib/agentview";
import { Badge, Card } from "../components/ui";

/**
 * AI agents — S15.3. A top-level destination in NETWORK, beside Kubernetes.
 *
 * ⛔ THE RENDER FLOOR GOVERNS EVERY STRING HERE, and it is stated in `lib/agentview.ts` with tests that
 * enforce it: no DETECTION language (the product does not inspect intent) and no PER-TOOL claim
 * (enforcement is five fields and a tool name is not among them). The honest verb is REACH.
 *
 * ⛔ ENTERPRISE. The open edition receives `403 edition_required`, which is a SUCCESSFUL REFUSAL — this
 * screen renders ABSENCE for it, never an error. Folding a correct refusal into the failed state would show
 * "could not load" for a server that answered correctly.
 */
export default function Agents() {
  const [orgId, setOrgId] = useState<string | null>(null);
  const [rows, setRows] = useState<Loaded<AgentRow[]> | null>(null);
  // ⚠ A SEPARATE FLAG, NOT AN ERROR. edition_required is a successful answer and must not reach the
  // failed state — see load() below.
  const [notEntitled, setNotEntitled] = useState(false);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const o = await loadOne<Array<{ id: string }>>(() => api.GET("/api/v1/organizations"));
      if (cancelled || !o.ok || o.data.length === 0) return;
      const id = o.data[0].id;
      setOrgId(id);
      const { data, error, response } = await api.GET(
        "/api/v1/organizations/{orgId}/agents",
        { params: { path: { orgId: id } } },
      );
      if (cancelled) return;
      // ⛔ 403 IS NOT A FAILURE. It is the server correctly stating an edition boundary; the screen shows
      // absence. Any other error is a real failure and must NOT render as "no agents" — a failed load
      // rendering as emptiness is a zero nobody measured.
      if (response?.status === 403) {
        setNotEntitled(true);
        setRows({ ok: true, data: [] });
        return;
      }
      if (error || !data) {
        setRows({ ok: false, error: "Could not load agents." });
        return;
      }
      setRows({ ok: true, data: data as AgentRow[] });
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // ⛔ ABSENCE, NOT A STYLED-AWAY CONTROL AND NOT AN UPSELL. The open edition simply does not have this
  // screen; inventing a boundary the client draws is the S14.5 defect.
  if (notEntitled) return null;

  return (
    <div className="flex flex-col gap-3.5">
      <div>
        <h1 className="text-[22px] font-semibold text-ink-heading">AI agents</h1>
        <p className="text-cell text-ink-tertiary">
          Agents authenticate as themselves and are bounded by the same policy as any device — they reach
          only what they are granted.
        </p>
      </div>
      <Card>
        <p className="text-xs text-slate-500">
          Each agent is shown with the person who authorised it into this organization. What an agent may
          reach is set by the grants on{" "}
          <Link to="/access" className="text-slate-300 underline">
            Access Policies
          </Link>
          . An agent with no grant reaches nothing.
        </p>
        {rows === null ? (
          <p className="mt-3 text-xs text-ink-secondary">Loading…</p>
        ) : !rows.ok ? (
          // ⛔ A FAILED LOAD IS NOT "NO AGENTS". Keeping them apart is the whole reason this is a Loaded<T>.
          <p data-state="load-failed" className="mt-3 rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger">
            {rows.error} <strong>This is not the same as having none.</strong>
          </p>
        ) : rows.data.length === 0 ? (
          <p data-state="no-agents" className="mt-3 text-xs text-ink-secondary">{NO_AGENTS}</p>
        ) : (
          <ul className="mt-3 space-y-1">
            {sortAgents(rows.data).map((a) => {
              const note = attributionNote(a);
              const kind = enrolmentKind({ enrolled_kind: a.enrolment_kind });
              return (
                <li
                  key={a.node_id}
                  data-kind={kind}
                  data-unattributable={a.unattributable ? "yes" : "no"}
                  className="flex items-center justify-between gap-3 rounded-md bg-white/5 px-3 py-2 text-sm"
                >
                  <span className="min-w-0 text-slate-200">
                    {a.name}
                    <span className="ml-2 font-mono text-xs text-slate-500">{a.address ?? "no address"}</span>
                    <span className="ml-2 text-xs text-ink-secondary">
                      {a.owner_email
                        ? <>· authorised by <span className="text-slate-300">{a.owner_email}</span></>
                        : "· no owner recorded"}
                    </span>
                  </span>
                  <span className="flex shrink-0 items-center gap-2">
                    {/* ⛔ THE THIRD STATE, IN ITS RULED WORDS. Not "not an agent" (a fact nobody has), not
                        "agent" (the defect the marker fixed), and never a fault — the node works; the gap
                        is in our record. */}
                    {kind === "undetermined" && (
                      <span title={UNDETERMINED_DETAIL}>
                        <Badge tone="neutral">{UNDETERMINED_LABEL}</Badge>
                      </span>
                    )}
                    {note && (
                      <span title={note.detail}>
                        <Badge tone="warn">{note.label}</Badge>
                      </span>
                    )}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </Card>
      {orgId === null && <span className="sr-only">no organization</span>}
    </div>
  );
}
