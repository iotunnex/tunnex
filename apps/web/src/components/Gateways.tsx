import { useState } from "react";
import { api, apiErrorMessage, type Node, type Org } from "../lib/api";
import { policyHealthBadge, badgeClass } from "../lib/healthview";
import { relativeAge } from "../lib/format";
import { Button, Card, ErrorText, Field, Input } from "./ui";
import { OneTimeSecretModal } from "./OneTimeSecret";

// enrollCommand builds the COMPLETE, copy-paste command an operator runs in their Tunnex install
// folder to bring a gateway online (S6.6 / POC ledger item 6): the join-token env INLINE plus the
// full `docker compose … up -d --force-recreate node-agent` — the piece a POC operator had to know
// by heart. A pinned node_name is shell-quoted (arbitrary charset) so a space can't silently
// truncate it and resurrect the node_name_mismatch loop.
export function enrollCommand(token: string, pinnedName: string | null): string {
  const name = pinnedName ? ` TUNNEX_NODE_NAME="${pinnedName.replace(/(["\\$`])/g, "\\$1")}"` : "";
  return `TUNNEX_JOIN_TOKEN=${token}${name} docker compose -f tunnex.yml up -d --force-recreate node-agent`;
}

/**
 * Gateways renders a org's enrolled tunnex-node agents and the enroll ceremony
 * (S4.7). Enrolling mints a ONE-TIME join token — a secret with the same handling
 * as the device config (S4.5 ceremony): it exists only in page state, is never
 * re-fetched (the server shows it exactly once), and must be explicitly
 * acknowledged to dismiss. The token is redeemed by the agent on its first
 * connect, at which point the node appears in this list.
 */
export function Gateways({ org, nodes }: { org: Org; nodes: Node[] }) {
  const [open, setOpen] = useState(false);
  const [nodeName, setNodeName] = useState("");
  const [token, setToken] = useState<string | null>(null);
  // The name the token was PINNED to at issue time — the server refuses this
  // token from an agent enrolling under any other name, so the ceremony must
  // hand the operator the COMPLETE env line. (Round-2 friction F1: the modal
  // omitted TUNNEX_NODE_NAME and the agent looped node_name_mismatch.)
  const [pinnedName, setPinnedName] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function issue() {
    setBusy(true);
    setError(null);
    const pinned = nodeName.trim() || null;
    try {
      const { data, error } = await api.POST("/api/v1/organizations/{orgId}/nodes/join-token", {
        params: { path: { orgId: org.id } },
        // node_name is optional; only send it when the user named the gateway.
        body: pinned ? { node_name: pinned } : {},
      });
      if (error || !data) {
        setError(apiErrorMessage(error, "Could not issue a join token."));
        return;
      }
      setToken(data.join_token); // shown once — never re-served
      setPinnedName(pinned);
      setOpen(false);
      setNodeName("");
    } catch {
      // A network-level failure rejects instead of returning {error}; without this
      // the button would stay stuck on "Generating…".
      setError("Could not reach the API.");
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card>
      <div className="flex items-center justify-between">
        <h2 className="text-sm font-semibold text-slate-300">Gateways</h2>
        <Button variant="ghost" onClick={() => setOpen((v) => !v)}>
          Enroll gateway
        </Button>
      </div>

      {open && (
        <div className="mt-3 flex flex-wrap items-end gap-3 border-t border-white/5 pt-3">
          <div className="min-w-[12rem] flex-1">
            <Field label="Gateway name (optional)">
              <Input value={nodeName} onChange={(e) => setNodeName(e.target.value)} placeholder="office-gw" maxLength={100} />
            </Field>
          </div>
          <Button onClick={issue} disabled={busy}>
            {busy ? "Generating…" : "Generate join token"}
          </Button>
        </div>
      )}

      <ErrorText>{error}</ErrorText>

      <ul className="mt-3 space-y-2">
        {nodes.map((n) => (
          <li key={n.id} className="flex items-center justify-between rounded-lg border border-white/5 bg-ink-900 px-4 py-2.5">
            <div>
              <span className="text-sm text-white">{n.name}</span>
              <span className="ml-2 font-mono text-xs text-slate-500">{n.agent_version}</span>
              {n.status === "revoked" && <span className="ml-2 text-xs text-rose-400">revoked</span>}
              {(() => {
                const b = policyHealthBadge(n);
                return b ? <span className={`ml-2 text-xs ${badgeClass(b.tone)}`}>{b.label}</span> : null;
              })()}
            </div>
            <span className="text-xs text-slate-500">
              {n.last_seen_at ? `last seen ${relativeAge(n.last_seen_at)}` : "never connected"}
            </span>
          </li>
        ))}
        {nodes.length === 0 && (
          <li className="text-sm text-slate-500">
            No gateway enrolled yet. Enroll one to start serving WireGuard peers.
          </li>
        )}
      </ul>

      {/* One-time join-token CEREMONY — the token authenticates a new agent on its
          first connect and is shown exactly once (shared OneTimeSecretModal). The
          node itself only appears in the list above once the agent redeems the
          token on first connect. */}
      {token && (
        <OneTimeSecretModal
          title="Enroll your gateway — run this once"
          caption={
            <>
              Run this command in your Tunnex install folder (where <span className="font-mono">tunnex.yml</span> lives)
              to bring the gateway online — it re-creates the <span className="font-mono">node-agent</span> with this join
              token. It is shown <span className="font-semibold">exactly once</span>, is single-use, and cannot be
              retrieved again — copy it now.
              {pinnedName && (
                <>
                  {" "}
                  The token is <span className="font-semibold">pinned to the name</span>{" "}
                  <span className="font-mono">{pinnedName}</span> — the agent must enroll with exactly that{" "}
                  <span className="font-mono">TUNNEX_NODE_NAME</span> or the server refuses it.
                </>
              )}
            </>
          }
          // The COMPLETE runnable command (env inline + the compose line) — see enrollCommand.
          secret={enrollCommand(token, pinnedName)}
          copyLabel="Copy command"
          onDismiss={() => {
            setToken(null);
            setPinnedName(null);
          }}
        />
      )}
    </Card>
  );
}
