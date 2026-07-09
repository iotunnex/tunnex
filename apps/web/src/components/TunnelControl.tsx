import { useEffect, useState } from "react";
import { desktop, type TunnelStatus } from "../lib/desktop";
import { Button, Card, ErrorText } from "./ui";

// TunnelControl is the desktop VPN connect/disconnect surface. Renders ONLY in the
// Electron client (window.tunnex present); the browser build sees nothing. Main
// owns the WG config (bearer-fetched) + the privileged helper; the renderer only
// invokes verbs + shows status (incl. the loud fail-closed / revoked states).
export function TunnelControl() {
  const bridge = desktop();
  const [status, setStatus] = useState<TunnelStatus>({ state: "down" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fullTunnel, setFullTunnel] = useState(false);

  useEffect(() => {
    const d = desktop();
    if (!d) return;
    d.tunnel.status().then(setStatus).catch(() => {});
    // Live updates: heartbeat status + the fail-closed / revoked signals from main.
    return d.tunnel.onStatusChanged(setStatus);
  }, []);

  if (!bridge) return null; // desktop only

  async function connect() {
    setBusy(true);
    setError(null);
    try {
      setStatus(await bridge!.tunnel.up(fullTunnel));
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
  const revoked = status.state === "revoked"; // this device was revoked server-side

  const statusText = live
    ? "Connected"
    : connecting
      ? "Connecting…"
      : failed
        ? "Disconnected — tunnel failed (kill-switch active)"
        : revoked
          ? "Device revoked — reconnect to re-enroll"
          : "Not connected";
  const statusClass = live ? "text-emerald-400" : connecting ? "text-amber-400" : failed || revoked ? "text-red-400" : "text-slate-400";

  return (
    <Card className="mt-6">
      <div className="flex items-center justify-between">
        <div>
          <div className="text-sm font-medium text-white">VPN tunnel</div>
          <div className={`mt-1 text-xs ${statusClass}`}>{statusText}</div>
        </div>
        {isUp ? (
          <Button variant="ghost" onClick={disconnect} disabled={busy}>
            {busy ? "…" : "Disconnect"}
          </Button>
        ) : (
          <Button onClick={connect} disabled={busy}>
            {busy ? "Connecting…" : failed || revoked ? "Reconnect" : "Connect"}
          </Button>
        )}
      </div>

      {revoked && (
        <div className="mt-3 rounded-md border border-red-500/30 bg-red-500/5 px-3 py-2 text-xs text-red-300">
          Your device was revoked or removed on the server. The local profile has been cleared — reconnecting will
          enroll a fresh device.
        </div>
      )}

      {/* Split-tunnel toggle: only meaningful before a device profile exists (the
          client owns creation and reuses an existing profile as-is), so it's shown
          only while disconnected. Full-tunnel carries an HONEST caveat — until S3.7
          ships gateway egress, a full tunnel blackholes internet traffic. */}
      {!isUp && (
        <div className="mt-4 border-t border-white/5 pt-3">
          <label className="flex items-center gap-2 text-xs text-slate-300">
            <input type="checkbox" checked={fullTunnel} onChange={(e) => setFullTunnel(e.target.checked)} />
            Route all traffic (full tunnel)
          </label>
          {fullTunnel ? (
            <p className="mt-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-amber-300">
              Full tunnel sends <strong>all</strong> your traffic through the gateway. This gateway does not provide
              internet egress yet — with full tunnel on you will have <strong>no internet access</strong> until that
              ships. Use split tunnel (org network only) for now.
            </p>
          ) : (
            <p className="mt-1 text-xs text-slate-500">Split tunnel: only your organization&rsquo;s network is routed through Tunnex.</p>
          )}
          <p className="mt-1 text-[11px] text-slate-600">
            Changing this replaces your device profile — the current device is revoked and a new one is created on your
            next connect.
          </p>
        </div>
      )}

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
  if (m.includes("helper_install_canceled")) return "Helper install was canceled. Reconnect and approve the admin prompt to install the Tunnex helper.";
  if (m.includes("helper_install_failed") || m.includes("helper_asset_missing")) return "Couldn't install the Tunnex helper. Reinstall the app, then reconnect.";
  if (m.includes("device_config_unavailable") || m.includes("not_authenticated")) return "Sign in again, then reconnect.";
  if (m.includes("no_active_gateway")) return "No gateway is enrolled on your organization yet.";
  // Forward-looking: when S3.7 lands the gateway-egress refusal, the server rejects a
  // full-tunnel device with this typed code — the UI mirrors it cleanly rather than
  // showing a raw status. (The refusal itself is S3.7; this is just the mapping.)
  if (m.includes("gateway_no_egress")) return "This gateway can't route full-tunnel internet traffic yet. Turn off full tunnel and reconnect.";
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
