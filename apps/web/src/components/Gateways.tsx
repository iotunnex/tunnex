import { useEffect, useState } from "react";
import { api, apiErrorMessage, type Node, type Org, type Meta } from "../lib/api";
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
  image: string; // WF-2: the agent image (CP-configured, digest-pinnable). One-truth over the artifact version.
}

// remoteEnrollCommand builds the ONE true `docker run` for a REMOTE cloud gateway (S8.2c D4) — a SINGLE
// LINE (D4b: a multi-line/compose line LOOKS copyable and got mis-pasted twice in the cross-cloud demo; a
// one-line docker run with every env inline cannot be). It bakes in EVERYTHING the demo needed by hand:
// `--network host` (so wg0 lives on the host + reaches real host LANs, not the bridge), `wgctrl` (real
// WireGuard, not the mem fake), `/dev/net/tun` + NET_ADMIN, the public CP URLs + servername, the token,
// the optional public endpoint. Pasted verbatim on a clean VM it reaches agent_ready with ZERO edits.
// q shell-quotes an env VALUE (single charset, one rule for the whole command — review: an unquoted
// space/metachar in ANY operator-supplied value corrupts the zero-touch command). Applied uniformly to the
// name, endpoint AND the CP urls (the urls now come from operator config, not the browser origin).
const q = (s: string) => `"${s.replace(/(["\\$`])/g, "\\$1")}"`;

export function remoteEnrollCommand(o: RemoteEnrollOpts): string {
  const nameEnv = o.name ? ` -e TUNNEX_NODE_NAME=${q(o.name)}` : "";
  const endpointEnv = o.endpoint ? ` -e TUNNEX_NODE_ENDPOINT=${q(o.endpoint)}` : "";
  return (
    `docker run -d --name tunnex-node --restart unless-stopped --network host ` +
    `--cap-add NET_ADMIN --device /dev/net/tun -v tunnex_node_state:/var/lib/tunnex-node ` +
    `-e TUNNEX_JOIN_TOKEN=${o.token}${nameEnv}${endpointEnv} ` +
    `-e TUNNEX_API_URL=${q(o.apiURL)} -e TUNNEX_AGENT_URL=${q(o.agentURL)} ` +
    `-e TUNNEX_AGENT_SERVERNAME=${q(o.serverName)} -e TUNNEX_WG_BACKEND=wgctrl ${o.image}`
  );
}

// CpEndpoints is a DISCRIMINATED result (re-review budget-rule reduce: one state model for CP-url consumption
// instead of scattered empty-string sentinels). The emitted command must NEVER silently carry a broken url.
//   { ok: true }  — usable urls; usedFallback=true means we used the dashboard origin (the CP has no
//                   configured public url), which the caller flags when the meta fetch FAILED (vs was unset).
//   { ok: false } — the CP's CONFIGURED public url is unparseable (operator APP_BASE_URL typo); the caller
//                   BLOCKS token mint on this (a one-time token minted against a broken url is worse than the
//                   block) and surfaces `reason`.
export type CpEndpoints =
  | { ok: true; apiURL: string; agentURL: string; serverName: string; usedFallback: boolean }
  | { ok: false; reason: string };

// cpEndpoints derives the public CP urls the remote agent dials from the CP's OWN configured public base URL
// (meta.public_base_url — AUTHORITATIVE), NOT window.location: the browser URL is whatever path the admin
// happened to use (a tunnel / internal alias / bare IP), which would bake an unreachable endpoint into the
// pasted command. Falls back to the dashboard origin ONLY when the CP didn't configure a public url. REST
// rides the origin (nginx); the agent TLS channel is :8443 with the standard cert SAN. PURE.
export function cpEndpoints(publicBaseURL: string | undefined, fallbackOrigin: string): CpEndpoints {
  const configured = publicBaseURL && publicBaseURL.trim() ? publicBaseURL.trim() : "";
  const usedFallback = configured === "";
  const base = configured || fallbackOrigin;
  let u: URL;
  try {
    u = new URL(base);
  } catch {
    // Only a CONFIGURED url reaches here (the browser origin always parses) → an operator APP_BASE_URL typo.
    return { ok: false, reason: `The control plane's configured public URL (${base}) is not a valid URL.` };
  }
  if (!u.hostname) return { ok: false, reason: `The control plane's configured public URL (${base}) has no host.` };
  return { ok: true, apiURL: u.origin, agentURL: `https://${u.hostname}:8443`, serverName: "tunnex-control", usedFallback };
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
  // The CP's authoritative public base URL for the emitted command (review #1 — not window.location).
  // metaError distinguishes "fetch FAILED" from "fetch ok, field unset" (re-review #2): both leave
  // publicBaseURL undefined, but only a genuine unset is a clean origin-fallback; a failure that silently
  // falls back must be flagged, else a tunnel/alias origin gets baked in with no signal.
  // metaLoaded makes the IN-FLIGHT fetch a first-class state (re-review round-3, budget-rule reduce
  // COMPLETION): before it settles, publicBaseURL is undefined so ep transiently narrows to the origin
  // fallback — minting THEN would either strand the token (a late-arriving broken URL flips ep.ok false and
  // hides the modal) or silently bake the browser origin. Gate the mint on metaLoaded so the emitted command
  // is only ever built from a SETTLED CP address — the whole in-flight window becomes a disabled button.
  const [publicBaseURL, setPublicBaseURL] = useState<string | undefined>(undefined);
  const [nodeAgentImage, setNodeAgentImage] = useState<string | undefined>(undefined); // WF-2: CP-configured (digest-pinnable) agent image
  const [metaError, setMetaError] = useState(false);
  const [metaLoaded, setMetaLoaded] = useState(false);
  useEffect(() => {
    api
      .GET("/api/v1/meta")
      .then(({ data }) => {
        setPublicBaseURL((data as Meta | undefined)?.public_base_url);
        setNodeAgentImage((data as Meta | undefined)?.node_agent_image);
        setMetaError(false);
      })
      .catch(() => setMetaError(true))
      .finally(() => setMetaLoaded(true));
  }, []);
  // ONE derivation of the CP urls (re-review budget-rule reduce). Recomputed each render — cheap + pure.
  const ep = cpEndpoints(publicBaseURL, window.location.origin);

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
          <Button onClick={issue} disabled={busy || !metaLoaded || !ep.ok}>
            {busy ? "Generating…" : !metaLoaded ? "Checking control plane…" : "Generate join token"}
          </Button>
        </div>
      )}

      {/* Block the mint (not just the emit) when the CP's configured public URL is unparseable — a one-time
          token minted against a broken URL is worse than refusing. The remedy is operator-side (APP_BASE_URL).
          Only judged once meta has SETTLED (metaLoaded) — an in-flight fetch isn't an error. */}
      {open && metaLoaded && !ep.ok && (
        <ErrorText>{ep.reason} Fix the control plane's public address (APP_BASE_URL) before enrolling a gateway.</ErrorText>
      )}
      {open && ep.ok && ep.usedFallback && metaError && (
        <p className="mt-2 text-xs text-amber-400">
          Couldn't confirm the control plane's public URL (metadata unavailable) — the command below uses this
          dashboard's origin. Verify the gateway can reach <span className="font-mono">{ep.apiURL}</span>.
        </p>
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
      {token && ep.ok && (
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
              (Installing on the SAME host as the control plane? See{" "}
              <span className="font-mono">docs/deploy-cloud-gateway.md</span> for the co-located compose form —
              it carries this same token.)
            </>
          }
          // D4: the ONE true remote-gateway docker run — single line, host networking + wgctrl baked in.
          // CP urls from the CP's own configured public base URL (review #1), not window.location.
          secret={remoteEnrollCommand({
            token,
            name: pinnedName,
            endpoint: pinnedEndpoint,
            apiURL: ep.apiURL,
            agentURL: ep.agentURL,
            serverName: ep.serverName,
            image: nodeAgentImage && nodeAgentImage.trim() ? nodeAgentImage.trim() : GATEWAY_IMAGE, // WF-2: CP-pinned, else default
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
