import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api, apiErrorMessage, type Member, type Org, type Role } from "../lib/api";
import { can, canManageMembership } from "../lib/rbac";
import { useAuth } from "../lib/auth";
import { Button, Card, ErrorText, Field, Input, Modal } from "../components/ui";
import { OneTimeSecretModal } from "../components/OneTimeSecret";

const ROLES: Role[] = ["owner", "admin", "member"];
const selectCls =
  "rounded-md border border-white/10 bg-ink-900 px-2 py-1 text-sm text-white focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent-400 disabled:opacity-50";

export default function Users() {
  const { state } = useAuth();
  const myId = state.status === "authed" ? state.user.id : "";
  // The server gates every MUTATING permission on the actor's verified email
  // (authorize() -> email_not_verified 403), separately from RBAC. Mirror that
  // here so we don't offer invite/role/deactivate controls that would only 403.
  // The global VerifyEmailBanner (AppShell) is the standing explanation, so we
  // hide rather than repeat a per-control message.
  const emailVerified = state.status === "authed" && state.user.email_verified;
  const [org, setOrg] = useState<Org | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [resetTarget, setResetTarget] = useState<Member | null>(null);
  const [resetBusy, setResetBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // My role in this org comes from my own row in the roster — no extra endpoint.
  const myRole = useMemo(() => members.find((m) => m.user_id === myId)?.role, [members, myId]);
  // Owner count drives the last-owner disable (mirrors the server's CountOwners).
  const ownerCount = useMemo(() => members.filter((m) => m.role === "owner").length, [members]);

  async function loadMembers(orgId: string) {
    const { data, error } = await api.GET("/api/v1/organizations/{orgId}/members", { params: { path: { orgId } } });
    if (error) return setError(apiErrorMessage(error, "Could not load members."));
    setMembers(data ?? []);
  }

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const { data: orgs, error: orgErr } = await api.GET("/api/v1/organizations");
        if (cancelled) return;
        if (orgErr) return setError(apiErrorMessage(orgErr, "Could not load your organizations."));
        const first = orgs?.[0];
        if (!first) return setError("You are not a member of any organization yet.");
        setOrg(first);
        if (!cancelled) await loadMembers(first.id);
      } catch {
        if (!cancelled) setError("Could not reach the API.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // The last-owner invariant is deterministic client-side: disable the control
  // that would demote/deactivate the sole owner. The server refusal (409
  // last_owner) stays the real enforcement — see mutate()'s refetch-on-error,
  // which self-corrects a stale roster after a lost race.
  const isSoleOwner = (m: Member) => m.role === "owner" && ownerCount <= 1;

  async function mutate(fn: () => Promise<{ error?: unknown }>, fallback: string) {
    if (!org) return;
    setError(null);
    const { error } = await fn();
    if (error) setError(apiErrorMessage(error, fallback));
    // Always refetch: on success to reflect the change, on error (esp. 409
    // last_owner) so the disabled-control state self-corrects if the roster
    // changed underneath us.
    await loadMembers(org.id);
  }

  const changeRole = (m: Member, role: Role) =>
    mutate(
      () =>
        api.PUT("/api/v1/organizations/{orgId}/members/{userId}/role", {
          params: { path: { orgId: org!.id, userId: m.user_id } },
          body: { role },
        }),
      "Could not change the role.",
    );

  const setActive = (m: Member, activate: boolean) => {
    const path = { params: { path: { orgId: org!.id, userId: m.user_id } } } as const;
    return mutate(
      () =>
        activate
          ? api.POST("/api/v1/organizations/{orgId}/members/{userId}/reactivate", path)
          : api.POST("/api/v1/organizations/{orgId}/members/{userId}/deactivate", path),
      activate ? "Could not reactivate the member." : "Could not deactivate the member.",
    );
  };

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Users</h1>
      <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>
      <ErrorText>{error}</ErrorText>

      {can(myRole, "member:invite") && emailVerified && org && <InviteForm orgId={org.id} onInvited={() => loadMembers(org.id)} />}

      <ul className="mt-6 space-y-2">
        {members.map((m) => {
          const isSelf = m.user_id === myId;
          // Role is editable on any target the actor may manage — INCLUDING self
          // (an owner handing off ownership). Deactivate is never offered on self
          // (it would log you out — a footgun, not a feature). The last-owner
          // disable therefore surfaces on the sole owner's OWN role control.
          const canManage = emailVerified && canManageMembership(myRole, m.role, "");
          const assignable = ROLES.filter((r) => canManageMembership(myRole, m.role, r));
          return (
            <li key={m.user_id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-white/5 bg-ink-800 px-4 py-3">
              <div className="min-w-0">
                <span className="text-sm text-white">{m.name || m.email}</span>
                {isSelf && <span className="ml-2 text-xs text-slate-500">(you)</span>}
                <span className="ml-2 font-mono text-xs text-slate-500">{m.email}</span>
                {m.status === "deactivated" && <span className="ml-2 text-xs text-warn">deactivated</span>}
                {!m.email_verified && m.status === "active" && <span className="ml-2 text-xs text-slate-600">unverified</span>}
              </div>
              <div className="flex items-center gap-2">
                {/* Role: editable only for a manageable target and only among
                    roles the actor may assign; disabled when it would demote the
                    sole owner. Otherwise a static label. */}
                {canManage && assignable.length > 0 ? (
                  <select
                    className={selectCls}
                    value={m.role}
                    disabled={isSoleOwner(m)}
                    title={isSoleOwner(m) ? "An organization must always have at least one owner." : undefined}
                    onChange={(e) => changeRole(m, e.target.value as Role)}
                  >
                    {assignable.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                ) : (
                  <span className="text-xs uppercase tracking-wide text-slate-400">{m.role}</span>
                )}

                {canManage && !isSelf &&
                  (m.status === "active" ? (
                    <Button
                      variant="danger"
                      onClick={() => setActive(m, false)}
                      disabled={isSoleOwner(m)}
                      title={isSoleOwner(m) ? "An organization must always have at least one owner." : undefined}
                    >
                      Deactivate
                    </Button>
                  ) : (
                    <Button variant="ghost" onClick={() => setActive(m, true)}>
                      Reactivate
                    </Button>
                  ))}

                {/* Admin-reset MFA (enterprise; open build answers edition_required). Disenroll-only —
                    it clears the member's 2FA, never signs in as them. */}
                {canManage && !isSelf && (
                  <Button variant="ghost" onClick={() => setResetTarget(m)}>
                    Reset 2FA
                  </Button>
                )}
              </div>
            </li>
          );
        })}
        {members.length === 0 && !error && <li className="text-sm text-slate-500">No members yet.</li>}
      </ul>
      {resetTarget && (
        <Modal
          title="Reset two-factor authentication"
          danger
          onDismiss={() => setResetTarget(null)}
          actions={
            <>
              <Button variant="ghost" onClick={() => setResetTarget(null)}>
                Cancel
              </Button>
              <Button variant="danger" onClick={doReset} disabled={resetBusy}>
                {resetBusy ? "Resetting…" : "Reset 2FA"}
              </Button>
            </>
          }
        >
          <p className="text-sm text-slate-300">
            Remove two-factor authentication for <span className="font-semibold">{resetTarget.email}</span>?
          </p>
          <p className="mt-2 text-xs text-slate-400">
            Their 2FA and recovery codes are cleared and they will be notified by email. If your organization
            requires MFA, they will be asked to set it up again at their next sign-in. This does not sign you in
            as them.
          </p>
        </Modal>
      )}
    </div>
  );

  async function doReset() {
    if (!org || !resetTarget) return;
    const target = resetTarget;
    setResetBusy(true);
    await mutate(
      () =>
        api.POST("/api/v1/organizations/{orgId}/members/{userId}/mfa-reset", {
          params: { path: { orgId: org.id, userId: target.user_id } },
        }),
      "Could not reset the member’s two-factor authentication.",
    );
    setResetBusy(false);
    setResetTarget(null);
  }
}

// InviteForm is enumeration-resistant by construction: the server returns the
// same 202 whether the email is new, already a member, or already has an
// account, and we render one fixed confirmation regardless. Reactivating a
// frozen member is a DIFFERENT verb (the row's Reactivate button) — invite is
// only ever for bringing in a new address.
function InviteForm({ orgId, onInvited }: { orgId: string; onInvited: () => void }) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [busy, setBusy] = useState(false);
  const [inviteLink, setInviteLink] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    const { data, error } = await api.POST("/api/v1/organizations/{orgId}/invitations", {
      params: { path: { orgId } },
      body: { email, role },
    });
    setBusy(false);
    if (error || !data) return setErr(apiErrorMessage(error, "Could not create the invitation."));
    setEmail("");
    // Build the accept link from THIS origin (correct host regardless of the API's
    // APP_BASE_URL) and show it once for the admin to copy + hand to the invitee —
    // the delivery path when email isn't configured. The email is best-effort on top.
    setInviteLink(`${window.location.origin}/accept-invite?token=${data.invite_token}`);
    onInvited();
  }

  return (
    <form onSubmit={submit} className="mt-6">
      <Card>
        <div className="flex flex-wrap items-end gap-3">
          <div className="min-w-[14rem] flex-1">
            <Field label="Invite by email">
              <Input
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                placeholder="name@company.com"
              />
            </Field>
          </div>
          <select className={selectCls} value={role} onChange={(e) => setRole(e.target.value as Role)} aria-label="Role">
            {ROLES.map((r) => (
              <option key={r} value={r}>
                {r}
              </option>
            ))}
          </select>
          <Button type="submit" disabled={busy}>
            {busy ? "Sending…" : "Send invite"}
          </Button>
        </div>
        {/* Success uses the accent, not green (green = liveness only, S4.4). The
            copy is deliberately generic — it never reveals whether the address
            already had an account. */}
        <ErrorText>{err}</ErrorText>
      </Card>
      {inviteLink && (
        <OneTimeSecretModal
          title="Invitation link"
          caption="Copy this link and send it to the invitee. It works once, expires, and won't be shown again. If email is configured, they also received it."
          secret={inviteLink}
          copyLabel="Copy link"
          onDismiss={() => setInviteLink(null)}
        />
      )}
    </form>
  );
}
