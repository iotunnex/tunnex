import { Session } from "electron";
import { CredentialStore } from "./credential";

// attachBearer injects `Authorization: Bearer <token>` on requests to the
// CONFIGURED API origin only. The raw token lives in main + the keychain and is
// added here per request — it NEVER crosses into the renderer (no getToken; the
// renderer's fetch goes out and main attaches the header). An expired credential
// (local check — no server oracle) is simply not attached, so the request 401s
// and the app prompts re-login.
export function attachBearer(session: Session, getOrigin: () => string, store: CredentialStore): void {
  session.webRequest.onBeforeSendHeaders((details, callback) => {
    const origin = getOrigin();
    if (origin && details.url.startsWith(origin + "/")) {
      const cred = store.load();
      if (cred && !CredentialStore.isExpired(cred, new Date())) {
        callback({ requestHeaders: { ...details.requestHeaders, Authorization: `Bearer ${cred.token}` } });
        return;
      }
    }
    callback({ requestHeaders: details.requestHeaders });
  });
}
