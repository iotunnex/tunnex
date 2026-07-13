import { InsecureStorageError, type Persistence, type SafeStorageLike } from "./credential";
import type { TunnelConfig } from "./helperclient";

// A device's config is ORIGIN-KEYED (like the bearer) and NEVER used cross-origin:
// a stored config belongs to exactly the server it was minted against. The map is
// encrypted at rest via the OS keychain — the WG private key lives here + in the
// helper only, never in the renderer.
export interface StoredTunnelConfig {
  origin: string; // the server origin this config/device belongs to
  deviceId: string; // for best-effort revoke (against THIS origin only)
  config: TunnelConfig;
  // pending (S7.3) = the device is AWAITING admin approval: the config is valid but the
  // gateway won't serve the peer yet, so resolveTunnelConfig refuses to arm the helper and
  // re-signals PendingApprovalError. Cleared by the ApprovalMonitor on approval.
  pending?: boolean;
}

type ConfigMap = Record<string, StoredTunnelConfig>;

// TunnelConfigStore refuses-by-default when there is no keychain (same posture as
// CredentialStore) — a plaintext WG private key on disk is exactly the mistake D2
// called out about the browser flow.
export class TunnelConfigStore {
  constructor(
    private safe: SafeStorageLike,
    private persist: Persistence,
    private allowInsecure: boolean,
  ) {}

  available(): boolean {
    return this.safe.isEncryptionAvailable() || this.allowInsecure;
  }

  private readMap(): ConfigMap {
    const raw = this.persist.read();
    if (!raw) return {};
    const json = this.safe.isEncryptionAvailable() ? this.safe.decryptString(raw) : raw.toString("utf8");
    try {
      return JSON.parse(json) as ConfigMap;
    } catch {
      return {};
    }
  }

  private writeMap(m: ConfigMap): void {
    const json = JSON.stringify(m);
    if (this.safe.isEncryptionAvailable()) {
      this.persist.write(this.safe.encryptString(json));
      return;
    }
    if (!this.allowInsecure) throw new InsecureStorageError();
    this.persist.write(Buffer.from(json, "utf8"));
  }

  get(origin: string): StoredTunnelConfig | null {
    return this.readMap()[origin] ?? null;
  }

  put(sc: StoredTunnelConfig): void {
    const m = this.readMap();
    m[sc.origin] = sc;
    this.writeMap(m);
  }

  // remove drops the entry for origin and returns it (so the caller can revoke the
  // device against THAT origin). No-op returns null.
  remove(origin: string): StoredTunnelConfig | null {
    const m = this.readMap();
    const existing = m[origin] ?? null;
    if (existing) {
      delete m[origin];
      this.writeMap(m);
    }
    return existing;
  }

  // list is for the UI to surface orphaned (non-current-origin) devices with a
  // remove-or-switch-back affordance.
  list(): StoredTunnelConfig[] {
    return Object.values(this.readMap());
  }
}
