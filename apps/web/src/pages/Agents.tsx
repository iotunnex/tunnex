import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, loadOne, type Loaded } from "../lib/api";
import {
  attributionNote, enrolmentKind, NO_AGENTS,
  UNDETERMINED_DETAIL, UNDETERMINED_LABEL, sortAgents, type AgentRow,
} from "../lib/agentview";
import { Badge, Button, Card, Field, Input } from "../components/ui";
// ⛔ THE SAME COMMAND BUILDER THE GATEWAY CEREMONY USES — imported, never re-implemented. Two places
// emitting enrolment commands is the one-truth risk the founder refused shape C over; what makes this an
// agent enrolment is the TOKEN's marker, not a different command.
import { enrollCommand } from "../components/Gateways";
import { OneTimeSecretModal } from "../components/OneTimeSecret";

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
  // The enrolment ceremony — mint a MARKED token, show the command once.
  const [name, setName] = useState("");
  const [token, setToken] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);

  async function enrol() {
    if (!orgId) return;
    setBusy(true);
    setErr(null);
    // ⛔ enrols_kind: "agent" IS THE WHOLE DIFFERENCE. Same endpoint, same ceremony, same emitted command —
    // the operator's declaration rides the token, captured at the same instant as the issuer.
    const { data, error } = await api.POST(
      "/api/v1/organizations/{orgId}/nodes/join-token",
      {
        params: { path: { orgId } },
        body: { node_name: name.trim() || undefined, enrols_kind: "agent" },
      },
    );
    setBusy(false);
    if (error || !data) {
      setErr("Could not create the enrolment token.");
      return;
    }
    setToken((data as { join_token: string }).join_token);
    setName("");
    setReload((n) => n + 1);
  }

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
  }, [reload]);

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
      {/* ⛔ THE CREATION PATH. The screen listed agents and offered no way to make one — a capability the
          product had and the operator could not reach, on the screen built to name that capability.
          ⚠ It is the EXISTING ceremony, named for agents: same endpoint, same emitted command. What makes
          it an agent is the marker on the token. */}
      <Card>
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-[14rem] flex-1">
            <Field label="Agent name (optional — pins the token to this name)">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="mcp-agent-prod"
              />
            </Field>
          </div>
          <Button onClick={() => void enrol()} disabled={busy}>
            {busy ? "Creating…" : "Enrol an agent"}
          </Button>
        </div>
        {err && <p className="mt-2 text-xs text-danger">{err}</p>}
        <p className="mt-2 text-[11px] text-ink-secondary">
          Enrolling mints a single-use token and records <strong>you</strong> as the person who authorised
          this agent. Run the command it gives you on the host that will run the agent.
        </p>
      </Card>

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
        ) : rows.data.filter((a) => a.enrolment_kind === "agent").length === 0 &&
          rows.data.length === 0 ? (
          <p data-state="no-agents" className="mt-3 text-xs text-ink-secondary">{NO_AGENTS}</p>
        ) : (
          <>
            {(() => {
              const declared = sortAgents(rows.data.filter((a) => a.enrolment_kind === "agent"));
              const undetermined = rows.data.filter((a) => a.enrolment_kind === "undetermined");
              return (
                <>
                  {declared.length === 0 ? (
                    <p data-state="no-agents" className="mt-3 text-xs text-ink-secondary">{NO_AGENTS}</p>
                  ) : (
                    <ul className="mt-3 space-y-1">
                      {declared.map((a) => {
                        const note = attributionNote(a);
                        return (
                          <li
                            key={a.node_id}
                            data-kind={enrolmentKind({ enrolled_kind: a.enrolment_kind })}
                            data-unattributable={a.unattributable ? "yes" : "no"}
                            className="flex items-center justify-between gap-3 rounded-md bg-white/5 px-3 py-2 text-sm"
                          >
                            <span className="min-w-0 text-slate-200">
                              {a.name}
                              <span className="ml-2 font-mono text-xs text-slate-500">
                                {a.address ?? "no address"}
                              </span>
                              <span className="ml-2 text-xs text-ink-secondary">
                                {a.owner_email
                                  ? <>· authorised by <span className="text-slate-300">{a.owner_email}</span></>
                                  : "· no owner recorded"}
                              </span>
                            </span>
                            <span className="flex shrink-0 items-center gap-2">
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

                  {/* ⛔ UNDETERMINED IS GROUPED, NOT INTERLEAVED — AND THIS IS A JUDGEMENT, RECORDED.
                      Every node enrolled before the marker is undetermined, so listing each one beside the
                      declared agents would make the screen mostly legacy gateways AND would imply each is a
                      candidate agent — a stronger claim than "we do not know".
                      ⚠ It is still RENDERED, never hidden: the ruling was that it must not arrive as a
                      blank, and a counted line in its ruled words is not a blank. Excluding them would
                      assert they are not agents, which is the fact nobody has. */}
                  {undetermined.length > 0 && (
                    <p
                      data-state="undetermined-group"
                      className="mt-3 rounded-md border border-line bg-white/5 px-3 py-2 text-xs text-ink-secondary"
                      title={UNDETERMINED_DETAIL}
                    >
                      <strong className="text-slate-300">
                        {undetermined.length} {undetermined.length === 1 ? "node" : "nodes"}:{" "}
                        {UNDETERMINED_LABEL}
                      </strong>{" "}
                      {UNDETERMINED_DETAIL}
                    </p>
                  )}
                </>
              );
            })()}
          </>
        )}
      </Card>
      {token && (
        <OneTimeSecretModal
          title="Enrol your agent: run this once"
          caption={
            <>
              Paste this <span className="font-semibold">single command</span> on the host that will run
              the agent. Shown <span className="font-semibold">exactly once</span>, single-use: copy it now.
            </>
          }
          secret={enrollCommand(token, null)}
          onDismiss={() => setToken(null)}
        />
      )}
      {orgId === null && <span className="sr-only">no organization</span>}
    </div>
  );
}
