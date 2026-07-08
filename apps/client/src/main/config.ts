import Store from "electron-store";
import { normalizeServerUrl, validateServer, serverChangeRequiresRelogin } from "./serverurl";

// Config owns the server URL (a MAIN-process concern — it's where auth + the
// updater point; the renderer only reads it over the bridge). Backed by
// electron-store in userData.
export class Config {
  private store: Store<{ serverUrl: string }>;

  constructor(store?: Store<{ serverUrl: string }>) {
    this.store = store ?? new Store<{ serverUrl: string }>({ name: "tunnex", defaults: { serverUrl: "" } });
  }

  getServerUrl(): string {
    return this.store.get("serverUrl", "");
  }

  // setServerUrl validates via /healthz BEFORE persisting and reports whether the
  // change requires a forced re-login (a credential must never cross servers).
  async setServerUrl(raw: string, hasCredential: boolean): Promise<{ url: string; reloginRequired: boolean }> {
    const current = this.getServerUrl();
    const url = await validateServer(raw, (u) => fetch(u, { method: "GET" }));
    const reloginRequired = serverChangeRequiresRelogin(current, url, hasCredential);
    this.store.set("serverUrl", url);
    return { url, reloginRequired };
  }

  requireServerUrl(): string {
    const u = this.getServerUrl();
    if (!u) throw new Error("no server configured");
    return normalizeServerUrl(u);
  }
}
