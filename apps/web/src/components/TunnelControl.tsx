import { useEffect, useState } from "react";
import { desktop, type TunnelStatus } from "../lib/desktop";
import { Button, Card, ErrorText } from "./ui";

// TunnelControl is the desktop VPN connect/disconnect surface. Renders ONLY in the
// Electron client (window.tunnex present); the browser build sees nothing. Main
// owns the WG config (bearer-fetched) + the privileged helper; the renderer only
// invokes verbs + shows status (incl. the loud fail-closed state).
export function TunnelControl() {
  const bridge = desktop();
  const [status, setStatus] = useState<TunnelStatus>({ state: "down" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const d = desktop();
    if (!d) return;
    d.tunnel.status().then(setStatus).catch(() => {});
    // Live updates: heartbeat status + the fail-closed signal from main.
    return d.tunnel.onStatusChanged(setStatus);
  }, []);

  if (!bridge) return null; // desktop only

  async function connect() {
    setBusy(true);
    setError(null);
    try {
      setStatus(await bridge!.tunnel.up());
    } catch (e) {
      setError(friendly((e as Error)?.message));
    } finally {
      setBusy(false);
    }
  }

  async function disconnect() {
    setBusy(true);
    setError(null);
    try {
      await bridge!.tunnel.down();
      setStatus({ state: "down" });
    } catch (e) {
      setError((e as Error)?.message ?? "Disconnect failed.");
    } finally {
      setBusy(false);
    }
  }

  // Connection status is derived from HANDSHAKE LIVENESS, not just the interface
  // being up. WireGuard rekeys roughly every 2 min, so a handshake older than this
  // (or none at all) means the link is dead — revoked, gateway unreachable — even
  // though the interface is still up. Showing green "Connected" in that state (while
  // the subtext says "handshaking…") is the defect this fixes.
  const HANDSHAKE_STALE_SEC = 180;
  const isUp = status.state === "up";
  // last_handshake_sec is an ABSOLUTE unix timestamp (0 = never), NOT an age — and
  // wireguard-go runs client-side, so it's on THIS machine's clock. Age = now - it.
  const nowSec = Math.floor(Date.now() / 1000);
  const handshakeAgeSec = status.last_handshake_sec ? Math.max(0, nowSec - status.last_handshake_sec) : null;
  const live = isUp && handshakeAgeSec != null && handshakeAgeSec <= HANDSHAKE_STALE_SEC;
  const connecting = isUp && !live; // interface up but no fresh handshake — not "Connected"
  const failed = status.state === "failed";

  return (
    <Card className="mt-6">
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm font-medium text-white">VPN tunnel</div>
          <div className={`mt-1 text-xs ${live ? "text-emerald-400" : connecting ? "text-amber-400" : failed ? "text-red-400" : "text-slate-400"}`}>
            {live ? "Connected" : connecting ? "Connecting…" : failed ? "Disconnected — tunnel failed (kill-switch active)" : "Not connected"}
          </div>
        </div>
        {isUp ? (
          <Button variant="ghost" onClick={disconnect} disabled={busy}>
            {busy ? "…" : "Disconnect"}
          </Button>
        ) : (
          <Button onClick={connect} disabled={busy}>
            {busy ? "Connecting…" : failed ? "Reconnect" : "Connect"}
          </Button>
        )}
      </div>

      {isUp && (
        <div className="mt-3 space-y-1 text-xs text-slate-400">
          {status.address && (
            <div>
              Your IP: <span className="text-slate-300">{status.address.split("/")[0]}</span>
            </div>
          )}
          <div className="grid grid-cols-3 gap-2">
            <span>↓ {fmtBytes(status.rx_bytes)}</span>
            <span>↑ {fmtBytes(status.tx_bytes)}</span>
            <span>{handshakeAgeSec != null ? `handshake ${handshakeAgeSec}s ago` : "handshaking…"}</span>
          </div>
        </div>
      )}
      <ErrorText>{error}</ErrorText>
    </Card>
  );
}

// friendly maps the known helper/config error codes to something a user can act on.
function friendly(msg?: string): string {
  const m = msg ?? "Could not connect.";
  if (m.includes("device_config_unavailable") || m.includes("not_authenticated")) return "Sign in again, then reconnect.";
  if (m.includes("no_active_gateway")) return "No gateway is enrolled on your organization yet.";
  if (m.includes("peer_resolution_needs_cgo") || m.includes("caller-auth")) return "The Tunnex helper is a dev/stub build — reinstall the signed helper.";
  if (m.includes("ECONNREFUSED") || m.includes("connect")) return "The Tunnex helper isn't running. Install/start it and try again.";
  return m;
}

function fmtBytes(n?: number): string {
  if (!n) return "0 B";
  const u = ["B", "KB", "MB", "GB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i ? 1 : 0)} ${u[i]}`;
}
