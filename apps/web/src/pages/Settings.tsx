import { useEffect, useState, type FormEvent } from "react";
import { api, CSRF, apiErrorMessage, type Meta, type Org, type Member, type Role, type SsoConfigView } from "../lib/api";
import { relativeAge } from "../lib/format";
import { can } from "../lib/rbac";
import { useAuth } from "../lib/auth";
import { Button, Card, ErrorText, Field, Input } from "../components/ui";

const PROVIDERS = ["google", "microsoft"] as const;
type Provider = (typeof PROVIDERS)[number];
type SsoView = SsoConfigView;

export default function Settings() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  const emailVerified = state.status === "authed" && state.user.email_verified;
  const [meta, setMeta] = useState<Meta | null>(null);
  const [org, setOrg] = useState<Org | null>(null);
  const [myRole, setMyRole] = useState<Role | undefined>(undefined);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const [{ data: m }, { data: orgs, error: orgErr }] = await Promise.all([
          api.GET("/api/v1/meta"),
          api.GET("/api/v1/organizations"),
        ]);
        if (cancelled) return;
        if (m) setMeta(m);
        if (orgErr) return setError(apiErrorMessage(orgErr, "Could not load your organizations."));
        const first = orgs?.[0];
        if (!first) return setError("You are not a member of any organization yet.");
        setOrg(first);
        // My role comes from my own row in the roster (no dedicated endpoint yet).
        const { data: members } = await api.GET("/api/v1/organizations/{orgId}/members", {
          params: { path: { orgId: first.id } },
        });
        if (!cancelled) setMyRole((members as Member[] | undefined)?.find((mm) => mm.user_id === myId)?.role);
      } catch {
        if (!cancelled) setError("Could not reach the API.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [myId]);

  const isAdmin = can(myRole, "org:update");

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Settings</h1>
      <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>
      <ErrorText>{error}</ErrorText>

      {org && !isAdmin && (
        <Card className="mt-6">
          <p className="text-sm text-slate-400">Organization settings are managed by owners and admins.</p>
        </Card>
      )}

      {org && isAdmin && (
        <>
          <OrgSection org={org} canEdit={emailVerified} onSaved={(o) => setOrg(o)} />
          {/* SSO config is enterprise-only; hidden in the open edition per /meta
              (watch-item b), with a muted note rather than a dead form. */}
          {meta?.edition === "enterprise" ? (
            <SsoSettings orgId={org.id} canEdit={emailVerified} />
          ) : (
            <Card className="mt-4">
              <h2 className="text-sm font-semibold text-slate-300">Single sign-on</h2>
              <p className="mt-1 text-xs text-slate-500">SSO (Google / Microsoft) is a Tunnex Enterprise feature.</p>
            </Card>
          )}
        </>
      )}
    </div>
  );
}

function OrgSection({ org, canEdit, onSaved }: { org: Org; canEdit: boolean; onSaved: (o: Org) => void }) {
  const [name, setName] = useState(org.name);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    setSaved(false);
    const { data, error } = await api.PATCH("/api/v1/organizations/{orgId}", {
      params: { path: { orgId: org.id } },
      headers: CSRF,
      body: { name },
    });
    setBusy(false);
    if (error || !data) return setErr(apiErrorMessage(error, "Could not save."));
    setSaved(true);
    onSaved(data);
  }

  return (
    <form onSubmit={submit} className="mt-6">
      <Card>
        <h2 className="text-sm font-semibold text-slate-300">Organization</h2>
        <div className="mt-3 flex flex-wrap items-end gap-3">
          <div className="min-w-[14rem] flex-1">
            <Field label="Name">
              <Input value={name} onChange={(e) => { setName(e.target.value); setSaved(false); }} required disabled={!canEdit} />
            </Field>
          </div>
          <Button type="submit" disabled={busy || !canEdit || name === org.name}>
            {busy ? "Saving…" : "Save"}
          </Button>
        </div>
        {/* Slug is immutable (identity); shown read-only. */}
        <p className="mt-2 font-mono text-xs text-slate-500">slug: {org.slug}</p>
        {saved && <p className="mt-2 text-xs text-accent-400">Saved.</p>}
        <ErrorText>{err}</ErrorText>
      </Card>
    </form>
  );
}

function SsoSettings({ orgId, canEdit }: { orgId: string; canEdit: boolean }) {
  return (
    <div className="mt-4 space-y-3">
      <h2 className="text-sm font-semibold text-slate-300">Single sign-on</h2>
      {PROVIDERS.map((p) => (
        <SsoProvider key={p} orgId={orgId} provider={p} canEdit={canEdit} />
      ))}
    </div>
  );
}

function SsoProvider({ orgId, provider, canEdit }: { orgId: string; provider: Provider; canEdit: boolean }) {
  const [view, setView] = useState<SsoView | null>(null);
  const [configured, setConfigured] = useState(false);
  const [clientId, setClientId] = useState("");
  const [clientSecret, setClientSecret] = useState("");
  const [tenantId, setTenantId] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // load fetches the current (non-secret) config. sso_not_configured (404) is the
  // normal "no config yet" state, not an error. Guarded against setState after
  // unmount via the cancelled flag the caller passes.
  async function load(isCancelled: () => boolean) {
    const { data, error } = await api.GET("/api/v1/organizations/{orgId}/sso/{provider}", {
      params: { path: { orgId, provider } },
    });
    if (isCancelled()) return;
    if (error || !data) {
      setConfigured(false);
      return;
    }
    setView(data);
    setConfigured(true);
    setClientId(data.client_id);
    setEnabled(data.enabled);
    setTenantId(data.tenant_id ?? "");
  }
  useEffect(() => {
    let cancelled = false;
    void load(() => cancelled);
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgId, provider]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    setSaved(false);
    const { error } = await api.PUT("/api/v1/organizations/{orgId}/sso/{provider}", {
      params: { path: { orgId, provider } },
      headers: CSRF,
      body: { client_id: clientId, client_secret: clientSecret, tenant_id: tenantId || undefined, enabled },
    });
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not save the SSO config."));
    setClientSecret(""); // never keep the secret in page state after save
    setSaved(true);
    await load(() => false); // refresh to pick up the new fingerprint
  }

  return (
    <form onSubmit={submit}>
      <Card>
        <div className="flex items-center justify-between">
          <h3 className="text-sm font-medium text-white capitalize">{provider}</h3>
          {configured && view && (
            <span className="text-xs text-slate-500">
              {view.enabled ? "enabled" : "disabled"} · updated {relativeAge(view.updated_at)}
            </span>
          )}
        </div>
        <div className="mt-3 space-y-3">
          <Field label="Client ID">
            <Input value={clientId} onChange={(e) => setClientId(e.target.value)} required disabled={!canEdit} />
          </Field>
          {/* WRITE-ONLY secret: the current secret is NEVER fetched or shown. We
              display only its keyed fingerprint as proof-of-storage, and the
              input is a "replace" affordance (blank = leave unchanged is not
              supported by the API, so a save requires re-entering it). */}
          <Field label={configured ? "Client secret (enter to replace)" : "Client secret"}>
            <Input
              type="password"
              value={clientSecret}
              onChange={(e) => setClientSecret(e.target.value)}
              required
              disabled={!canEdit}
              placeholder="••••••••"
            />
          </Field>
          {configured && view?.secret_fingerprint && (
            <p className="font-mono text-xs text-slate-500">stored secret fingerprint: {view.secret_fingerprint}</p>
          )}
          {provider === "microsoft" && (
            <Field label="Tenant ID (Entra)">
              <Input value={tenantId} onChange={(e) => setTenantId(e.target.value)} disabled={!canEdit} />
            </Field>
          )}
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} disabled={!canEdit} />
            Enabled
          </label>
        </div>
        <div className="mt-4 flex items-center gap-3">
          <Button type="submit" disabled={busy || !canEdit}>
            {busy ? "Saving…" : configured ? "Replace config" : "Configure"}
          </Button>
          {saved && <span className="text-xs text-accent-400">Saved.</span>}
        </div>
        <ErrorText>{err}</ErrorText>
      </Card>
    </form>
  );
}
