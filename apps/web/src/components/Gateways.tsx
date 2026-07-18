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

// The published gateway image (S6.6 zero-build deploy). Pulled by the emitted docker run — nothing builds.
export const GATEWAY_IMAGE = "ghcr.io/iotunnex/tunnex-node-agent:latest";

export interface RemoteEnrollOpts {
  token: string;
  name: string | null;
  endpoint: string | null; // public ip:port peers dial (D4a: admin-entered). null → NAT'd spoke, no endpoint.
  apiURL: string; // public CP REST origin (nginx), e.g. https://cp.example.com
  agentURL: string; // public CP agent TLS channel, e.g. https://cp.example.com:8443
  serverName: string; // CP cert SAN the agent pins, e.g. tunnex-control
}

// remoteEnrollCommand builds the ONE true `docker run` for a REMOTE cloud gateway (S8.2c D4) — a SINGLE
// LINE (D4b: a multi-line/compose line LOOKS copyable and got mis-pasted twice in the cross-cloud demo; a
// one-line docker run with every env inline cannot be). It bakes in EVERYTHING the demo needed by hand:
// `--network host` (so wg0 lives on the host + reaches real host LANs, not the bridge), `wgctrl` (real
// WireGuard, not the mem fake), `/dev/net/tun` + NET_ADMIN, the public CP URLs + servername, the token,
// the optional public endpoint. Pasted verbatim on a clean VM it reaches agent_ready with ZERO edits.
export function remoteEnrollCommand(o: RemoteEnrollOpts): string {
  const q = (s: string) => `"${s.replace(/(["\\$`])/g, "\\$1")}"`;
  const nameEnv = o.name ? ` -e TUNNEX_NODE_NAME=${q(o.name)}` : "";
  const endpointEnv = o.endpoint ? ` -e TUNNEX_NODE_ENDPOINT=${o.endpoint}` : "";
  return (
    `docker run -d --name tunnex-node --restart unless-stopped --network host ` +
    `--cap-add NET_ADMIN --device /dev/net/tun -v tunnex_node_state:/var/lib/tunnex-node ` +
    `-e TUNNEX_JOIN_TOKEN=${o.token}${nameEnv}${endpointEnv} ` +
    `-e TUNNEX_API_URL=${o.apiURL} -e TUNNEX_AGENT_URL=${o.agentURL} ` +
    `-e TUNNEX_AGENT_SERVERNAME=${o.serverName} -e TUNNEX_WG_BACKEND=wgctrl ${GATEWAY_IMAGE}`
  );
}

// cpEndpoints derives the public CP URLs the remote agent dials, from the dashboard's own origin (the
// browser is served BY the control plane). REST rides nginx on the origin; the agent TLS channel is :8443
// with the standard cert SAN. Pure over a location-like input so it unit-tests without a DOM.
export function cpEndpoints(loc: { protocol: string; hostname: string; origin: string }): { apiURL: string; agentURL: string; serverName: string } {
  return { apiURL: loc.origin, agentURL: `https://${loc.hostname}:8443`, serverName: "tunnex-control" };
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
  const [endpoint, setEndpoint] = useState(""); // D4a: admin-entered public ip:port (blank = NAT'd spoke)
  const [pinnedEndpoint, setPinnedEndpoint] = useState<string | null>(null);
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
      setPinnedEndpoint(endpoint.trim() || null);
      setOpen(false);
      setNodeName("");
      setEndpoint("");
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
          <div className="min-w-[12rem] flex-1">
            <Field label="Public endpoint (optional — ip:port peers dial)">
              <Input value={endpoint} onChange={(e) => setEndpoint(e.target.value)} placeholder="203.0.113.7:51820" maxLength={100} />
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
              Paste this <span className="font-semibold">single command</span> on the gateway VM (Docker installed) to
              bring it online — it pulls the agent and comes up on real WireGuard with{" "}
              <span className="font-semibold">no edits</span>. Shown <span className="font-semibold">exactly once</span>,
              single-use — copy it now.
              {pinnedName && (
                <>
                  {" "}
                  Pinned to the name <span className="font-mono">{pinnedName}</span> — the agent enrolls under exactly
                  that or the server refuses it.
                </>
              )}
              {!pinnedEndpoint && (
                <>
                  {" "}
                  No public endpoint set → this gateway is treated as a <span className="font-semibold">NAT'd spoke</span>{" "}
                  (it dials the hub; other peers can't dial it).
                </>
              )}{" "}
              Co-located with the control plane instead? Run{" "}
              <span className="font-mono">docker compose up -d node-agent</span> in your install folder.
            </>
          }
          // D4: the ONE true remote-gateway docker run — single line, host networking + wgctrl baked in.
          secret={remoteEnrollCommand({
            token,
            name: pinnedName,
            endpoint: pinnedEndpoint,
            ...cpEndpoints({ protocol: window.location.protocol, hostname: window.location.hostname, origin: window.location.origin }),
          })}
          copyLabel="Copy command"
          onDismiss={() => {
            setToken(null);
            setPinnedName(null);
            setPinnedEndpoint(null);
          }}
        />
      )}
    </Card>
  );
}
