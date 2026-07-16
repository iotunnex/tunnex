import { HelperConnection, PROTOCOL_VERSION, type PostureStatus, type TunnelConfig, type TunnelStatus } from "./helperclient";

// helperSocketPath is the local endpoint the privileged helper listens on. It is
// platform-specific (a unix socket on macOS, a named pipe on Windows). The helper
// creates it with an owner-only ACL; the app connects and its caller identity is
// verified helper-side (path-check now, code-signing at S6.5b).
export function helperSocketPath(platform: NodeJS.Platform = process.platform): string {
  if (platform === "win32") return "\\\\.\\pipe\\tunnex-helper";
  return "/var/run/tunnex/helper.sock";
}

// ConfigProvider yields the WireGuard TunnelConfig for the current device. It runs
// in MAIN and fetches via the bearer-injected API — so the WG PRIVATE KEY, like
// the bearer token, never enters the renderer. (D2: the client OWNS device
// creation and never re-fetches; see PLAN S6.3 ConfigProvider decisions.)
export type ConfigProvider = () => Promise<TunnelConfig>;

// HEARTBEAT_MS must stay well under the helper's read deadline (30s): the app holds
// ONE persistent connection open and this heartbeat is what keeps it the live
// "owner" (and feeds the UI live stats). Miss enough heartbeats and the helper
// drops the owner connection and fails the tunnel closed.
const HEARTBEAT_MS = 10_000;

// TunnelController is MAIN's tunnel control. It holds a PERSISTENT helper
// connection for the tunnel's lifetime (the liveness signal), builds the typed
// requests, and heartbeats while up. onStatus lets main forward live status /
// a fail-closed event to the renderer.
export class TunnelController {
  private readonly conn: HelperConnection;
  private heartbeat: ReturnType<typeof setInterval> | null = null;
  // The active device's tunnel address, cached from the config on `up`. The helper
  // reports runtime stats (rx/tx/handshake) but not the address (it's config), so
  // main attaches it to every status it forwards. Cleared on down / fail-closed.
  private address?: string;

  // withAddress decorates a helper status with the cached tunnel address so the UI
  // can show "Your IP" without the address ever needing to round-trip the helper.
  private withAddress(s: TunnelStatus): TunnelStatus {
    return this.address ? { ...s, address: this.address } : s;
  }

  constructor(
    socketPath: string,
    private readonly resolveConfig: ConfigProvider,
    private readonly onStatus?: (s: TunnelStatus) => void,
  ) {
    this.conn = new HelperConnection(socketPath, () => this.onLost());
  }

  async up(): Promise<TunnelStatus> {
    const config = await this.resolveConfig();
    this.address = config.address;
    const r = await this.conn.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "tunnel_up", config });
    if (!r.ok) throw new Error(r.code ? `${r.code}: ${r.error ?? ""}` : (r.error ?? "tunnel up failed"));
    this.startHeartbeat();
    return this.withAddress(r.status ?? { state: "up" });
  }

  async down(): Promise<void> {
    this.stopHeartbeat();
    this.address = undefined;
    const r = await this.conn.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "tunnel_down" });
    // Graceful: the down told the helper to restore routing, so closing the owner
    // connection now is expected (won't trip fail-closed).
    this.conn.close();
    if (!r.ok) throw new Error(r.code ? `${r.code}: ${r.error ?? ""}` : (r.error ?? "tunnel down failed"));
  }

  async status(): Promise<TunnelStatus> {
    const r = await this.conn.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "status" });
    if (!r.ok) throw new Error(r.code ? `${r.code}: ${r.error ?? ""}` : (r.error ?? "tunnel status failed"));
    return this.withAddress(r.status ?? { state: "down" });
  }

  // posture reads local posture facts via the helper (S7.5.3) — read-only, never
  // touches tunnel state or connection ownership. Throws on refusal (incl. an
  // OLD helper's unknown_verb); the caller treats any throw as "facts
  // indeterminate" and reports them ABSENT, never guessed.
  async posture(): Promise<PostureStatus> {
    const r = await this.conn.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "posture_status" });
    if (!r.ok) throw new Error(r.code ? `${r.code}: ${r.error ?? ""}` : (r.error ?? "posture status failed"));
    return r.posture ?? {};
  }

  private startHeartbeat(): void {
    this.stopHeartbeat();
    this.heartbeat = setInterval(() => {
      this.conn
        .request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "status" })
        .then((r) => {
          if (r.ok && r.status) this.onStatus?.(this.withAddress(r.status));
        })
        .catch(() => {
          /* a dropped connection surfaces via onLost */
        });
    }, HEARTBEAT_MS);
    this.heartbeat.unref?.();
  }

  private stopHeartbeat(): void {
    if (this.heartbeat) {
      clearInterval(this.heartbeat);
      this.heartbeat = null;
    }
  }

  // onLost fires when the persistent connection drops unexpectedly (helper died):
  // stop heartbeating and surface a fail-closed status to the UI.
  private onLost(): void {
    this.stopHeartbeat();
    this.address = undefined;
    this.onStatus?.({ state: "failed" });
  }
}
