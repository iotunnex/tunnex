import { HelperClient, PROTOCOL_VERSION, type TunnelConfig, type TunnelStatus } from "./helperclient";

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
// the bearer token, never enters the renderer. Wiring the real provider (create /
// fetch the device config, mirroring the CLI's atomic device flow) is the next
// integration step; the transport + control plane below are independent of it.
export type ConfigProvider = () => Promise<TunnelConfig>;

// TunnelController is MAIN's view of tunnel control: it builds the typed helper
// requests (always stamping the protocol version + current auth mode) and turns
// helper error responses into thrown errors the IPC layer surfaces to the UI.
export class TunnelController {
  constructor(
    private readonly client: HelperClient,
    private readonly resolveConfig: ConfigProvider,
  ) {}

  async up(): Promise<TunnelStatus> {
    const config = await this.resolveConfig();
    const r = await this.client.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "tunnel_up", config });
    if (!r.ok) throw new Error(r.code ? `${r.code}: ${r.error ?? ""}` : (r.error ?? "tunnel up failed"));
    return r.status ?? { state: "up" };
  }

  async down(): Promise<void> {
    const r = await this.client.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "tunnel_down" });
    if (!r.ok) throw new Error(r.code ? `${r.code}: ${r.error ?? ""}` : (r.error ?? "tunnel down failed"));
  }

  async status(): Promise<TunnelStatus> {
    const r = await this.client.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "status" });
    if (!r.ok) throw new Error(r.code ? `${r.code}: ${r.error ?? ""}` : (r.error ?? "tunnel status failed"));
    return r.status ?? { state: "down" };
  }
}
