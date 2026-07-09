import { useEffect, useState, type FormEvent } from "react";
import { desktop } from "../lib/desktop";
import { Button, Card, ErrorText, Field, Input } from "./ui";

// DesktopSettings is the Electron-only account surface (S6.4): it shows the CURRENT
// tenant server and gives the two lifecycle actions the POC used to require deleting
// userData files for — change server and sign out. Both go through main's verb
// allowlist (config.setServerUrl / auth.logout), which already sequences the
// credential + tunnel + config teardown; main reloads the window on success. Renders
// nothing in the browser build (no window.tunnex).
export function DesktopSettings() {
  const bridge = desktop();
  const [server, setServer] = useState<string>("");
  const [next, setNext] = useState<string>("");
  const [busy, setBusy] = useState<"idle" | "changing" | "signout">("idle");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const d = desktop();
    if (!d) return;
    d.config
      .getServerUrl()
      .then((u) => {
        setServer(u);
        setNext(u);
      })
      .catch(() => {});
  }, []);

  if (!bridge) return null; // desktop only

  async function changeServer(e: FormEvent) {
    e.preventDefault();
    if (!next || next === server) return;
    setBusy("changing");
    setError(null);
    try {
      // On a real origin change main revokes+clears the old credential/tunnel BEFORE
      // switching, then reloads — so this promise may not resolve visibly (the window
      // reloads). A validation failure (unreachable server, bad URL) rejects here.
      await bridge!.config.setServerUrl(next.trim());
    } catch (err) {
      setError(friendly((err as Error)?.message));
      setBusy("idle");
    }
  }

  async function signOut() {
    setBusy("signout");
    setError(null);
    // Full-sweep: main stops the tunnel, clears the config + revokes the device, then
    // clears the credential and reloads to the signed-out state. Best-effort by design.
    await bridge!.auth.logout().catch(() => {});
    setBusy("idle");
  }

  return (
    <Card className="mt-6">
      <h2 className="text-sm font-semibold text-slate-300">This device</h2>
      <p className="mt-1 text-xs text-slate-500">Desktop client — server connection and session.</p>

      <form className="mt-4" onSubmit={changeServer}>
        <Field label="Tenant server">
          <Input value={next} onChange={(e) => setNext(e.target.value)} placeholder="https://vpn.example.com" spellCheck={false} />
        </Field>
        <p className="mt-1 text-xs text-slate-500">
          Changing the server signs you out of the current one and clears its tunnel profile — a credential is never
          reused across servers.
        </p>
        <div className="mt-3">
          <Button type="submit" disabled={busy !== "idle" || !next || next === server}>
            {busy === "changing" ? "Switching…" : "Change server"}
          </Button>
        </div>
      </form>

      <div className="mt-5 border-t border-white/5 pt-4">
        <Button variant="ghost" onClick={signOut} disabled={busy !== "idle"}>
          {busy === "signout" ? "Signing out…" : "Sign out"}
        </Button>
        <p className="mt-1 text-xs text-slate-500">Disconnects the tunnel, revokes this device, and clears the saved credential.</p>
      </div>

      <ErrorText>{error}</ErrorText>
    </Card>
  );
}

function friendly(msg?: string): string {
  const m = msg ?? "Could not change the server.";
  if (m.includes("invalid server url")) return "That doesn't look like a valid server URL.";
  if (m.includes("unreachable") || m.includes("healthz") || m.includes("fetch")) return "Couldn't reach that server. Check the URL and try again.";
  return m;
}
