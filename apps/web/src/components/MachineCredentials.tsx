import { useEffect, useState, type FormEvent } from "react";
import { api, apiErrorMessage, type MachineCredential } from "../lib/api";
import { relativeAge } from "../lib/format";
import { Button, Card, ErrorText, Field, Input } from "./ui";
import { OneTimeSecretModal } from "./OneTimeSecret";

// MachineCredentials (S10.2) — the owner-only Settings panel to mint / list / revoke the GitOps operator's
// machine credential. The token is shown ONCE via the shared OneTimeSecretModal (the same ceremony as a
// device config / .ovpn / recovery codes); the list shows the keyed FINGERPRINT only — the server never
// re-serves the secret. Rendered only for machine:manage (owner) — the endpoints are owner-gated, so a
// non-owner would only get 403s here.
export function MachineCredentials({ orgId, canManage }: { orgId: string; canManage: boolean }) {
  const [creds, setCreds] = useState<MachineCredential[] | null>(null);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [secret, setSecret] = useState<string | null>(null); // the tnxm_ token, in state ONLY, never re-fetched

  async function load() {
    const { data, error } = await api.GET("/api/v1/organizations/{orgId}/machine-credentials", {
      params: { path: { orgId } },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not load machine credentials."));
    setCreds((data as MachineCredential[]) ?? []);
  }
  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgId]);

  async function mint(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    const { data, error } = await api.POST("/api/v1/organizations/{orgId}/machine-credentials", {
      params: { path: { orgId } },
      body: { name: name.trim() },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not mint the credential."));
    setName("");
    setSecret(data?.token ?? null); // shown once — the server never re-serves it
    void load();
  }

  async function revoke(id: string) {
    const { error } = await api.DELETE("/api/v1/organizations/{orgId}/machine-credentials/{credentialId}", {
      params: { path: { orgId, credentialId: id } },
    });
    if (error) return setErr(apiErrorMessage(error, "Could not revoke the credential."));
    void load();
  }

  return (
    <Card>
      <h2 className="text-sm font-semibold text-slate-300">GitOps operator credentials</h2>
      <p className="mt-1 text-xs text-slate-500">
        A machine credential the Tunnex Kubernetes operator uses to manage this organization over the API. It
        authenticates as a system actor — audited as <span className="font-mono">operator:&lt;name&gt;</span>,
        never a user. The token is shown once at mint; if lost, revoke and re-mint.
      </p>
      <ErrorText>{err}</ErrorText>

      {canManage && (
        <form onSubmit={mint} className="mt-3 flex items-end gap-2">
          <div className="flex-1">
            {/* "Credential name", NOT "Name" — the Settings page already has an org "Name" field, and two
                controls sharing an accessible name are announced identically by a screen reader (S11-1). */}
            <Field label="Credential name">
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="gitops" />
            </Field>
          </div>
          <Button type="submit" disabled={busy || name.trim() === ""}>
            {busy ? "Minting…" : "Mint credential"}
          </Button>
        </form>
      )}

      {creds && creds.length > 0 ? (
        <ul className="mt-3 space-y-1">
          {creds.map((c) => (
            <li key={c.id} className="flex items-center justify-between rounded-md bg-white/5 px-3 py-2 text-sm">
              <span className="text-slate-200">
                {c.name}
                <span className="ml-2 font-mono text-xs text-slate-500">{c.fingerprint}</span>
                <span className="ml-2 text-xs text-slate-500">
                  created {relativeAge(c.created_at)}
                  {c.last_used_at ? ` · used ${relativeAge(c.last_used_at)}` : " · never used"}
                </span>
              </span>
              {canManage && (
                <Button variant="ghost" onClick={() => revoke(c.id)}>
                  Revoke
                </Button>
              )}
            </li>
          ))}
        </ul>
      ) : (
        creds && <p className="mt-3 text-xs text-slate-500">No machine credentials yet.</p>
      )}

      {secret && (
        <OneTimeSecretModal
          title="Machine credential"
          caption={
            <>
              This is the operator&rsquo;s bearer token. It is shown <span className="font-semibold">exactly once</span>{" "}
              and can never be retrieved again — save it into the operator&rsquo;s Secret now. If lost, revoke and re-mint.
            </>
          }
          secret={secret}
          onDismiss={() => setSecret(null)}
        />
      )}
    </Card>
  );
}
