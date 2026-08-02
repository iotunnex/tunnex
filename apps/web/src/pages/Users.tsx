import { useEffect, useMemo, useState, type FormEvent } from "react";
import {
  api,
  apiErrorMessage,
  type Device,
  type Member,
  type Org,
  type Role,
} from "../lib/api";
import { can, canManageMembership } from "../lib/rbac";
import { useAuth } from "../lib/auth";
import {
  Button,
  Card,
  DataTable,
  ErrorText,
  Field,
  Input,
  Modal,
} from "../components/ui";
import { OneTimeSecretModal } from "../components/OneTimeSecret";
import {
  deviceCountFor,
  deviceCountLabel,
  filterMembers,
  groupAccessLabel,
  groupAccessState,
  LAST_OWNER_NOTE,
  roleDistribution,
  roleTallyLabel,
  rosterSubtitle,
  rosterShape,
} from "../lib/usersview";

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
  const [query, setQuery] = useState("");
  const [isEnterprise, setIsEnterprise] = useState(false);
  // ⛔ `null` MEANS "NOT LOADED", AND IT IS NOT THE SAME AS `[]`. An empty array is a fetched answer; null is
  // the absence of one, and deviceCountFor / groupAccessState each have a DISTINCT arm for it. Initialising
  // these to `[]` would make a page that has not finished loading claim every member owns nothing.
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [groupCount, setGroupCount] = useState<number | null>(null);

  // My role in this org comes from my own row in the roster — no extra endpoint.
  const myRole = useMemo(
    () => members.find((m) => m.user_id === myId)?.role,
    [members, myId],
  );
  // Owner count drives the last-owner disable (mirrors the server's CountOwners).
  //
  // ⛔ AND IT MIRRORS THE SERVER'S FLAW, DELIBERATELY. `CountOwners` is
  //   SELECT count(*) FROM memberships WHERE org_id=$1 AND role='owner'
  // with NO join to `users`, so a DEACTIVATED owner counts as an owner. Proven reachable (S14.11 probe,
  // docs/probes/lockout_probe_test.go.txt): deactivate owner A, then deactivate owner B — the guard permits
  // both because two owner ROWS exist, and the org ends with 2 owner rows and 0 accounts that can sign in
  // and act. Recovery needs direct database access.
  //
  // ⛔ DO NOT ADD `&& m.status === "active"` HERE. It would make the client a SECOND AUTHORITY that disagrees
  // with the server about who the last owner is — the control would grey out while the server still permits
  // the change, or the reverse. The S4.7 precedent is that the server owns the refusal.
  //
  // THIS LINE FOLLOWS THE SERVER FIX. When CountOwners learns to join `users.status`, this changes with it,
  // in the same change, not before.
  const ownerCount = useMemo(
    () => members.filter((m) => m.role === "owner").length,
    [members],
  );

  async function loadMembers(orgId: string) {
    const { data, error } = await api.GET(
      "/api/v1/organizations/{orgId}/members",
      { params: { path: { orgId } } },
    );
    if (error)
      return setError(apiErrorMessage(error, "Could not load members."));
    setMembers(data ?? []);
  }

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        // Edition comes from /meta, the same source Access.tsx uses — never inferred from whether an
        // enterprise call happened to 403 (which conflates edition with permission, the bug this screen's
        // view-model was fixed for).
        const { data: meta } = await api.GET("/api/v1/meta");
        if (cancelled) return;
        setIsEnterprise(meta?.edition === "enterprise");
        const { data: orgs, error: orgErr } = await api.GET(
          "/api/v1/organizations",
        );
        if (cancelled) return;
        if (orgErr)
          return setError(
            apiErrorMessage(orgErr, "Could not load your organizations."),
          );
        const first = orgs?.[0];
        if (!first)
          return setError("You are not a member of any organization yet.");
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

  // ⛔ THE TWO GATED READS ARE NOT ISSUED WHEN THEIR GATE FAILS. Firing them anyway would put a 403 into the
  // page's single error surface, so a member's ordinary, correct page load would show an error — and the gate
  // note already says the same thing calmly. Depends on `myRole`, which arrives with the members list, so this
  // effect runs after it rather than in the load above.
  useEffect(() => {
    let cancelled = false;
    if (!org || !myRole) return;
    (async () => {
      if (can(myRole, "member:manage")) {
        const { data, error } = await api.GET(
          "/api/v1/organizations/{orgId}/devices",
          { params: { path: { orgId: org.id } } },
        );
        // A failure leaves `devices` NULL on purpose — deviceCountFor renders "could not load", which is
        // honest, rather than a zero that would read as "this person has no devices".
        if (!cancelled && !error) setDevices(data ?? []);
      }
      if (isEnterprise && can(myRole, "policy:view")) {
        const { data, error } = await api.GET(
          "/api/v1/organizations/{orgId}/groups",
          { params: { path: { orgId: org.id } } },
        );
        if (!cancelled && !error) setGroupCount((data ?? []).length);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [org, myRole, isEnterprise]);

  // The last-owner invariant is deterministic client-side: disable the control
  // that would demote/deactivate the sole owner. The server refusal (409
  // last_owner) stays the real enforcement — see mutate()'s refetch-on-error,
  // which self-corrects a stale roster after a lost race.
  const isSoleOwner = (m: Member) => m.role === "owner" && ownerCount <= 1;

  async function mutate(
    fn: () => Promise<{ error?: unknown }>,
    fallback: string,
  ) {
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
    const path = {
      params: { path: { orgId: org!.id, userId: m.user_id } },
    } as const;
    return mutate(
      () =>
        activate
          ? api.POST(
              "/api/v1/organizations/{orgId}/members/{userId}/reactivate",
              path,
            )
          : api.POST(
              "/api/v1/organizations/{orgId}/members/{userId}/deactivate",
              path,
            ),
      activate
        ? "Could not reactivate the member."
        : "Could not deactivate the member.",
    );
  };

  const shape = rosterShape({ role: myRole, isEnterprise });
  // ⛔ ACTIONS IS ABSENT WHEN NO ROW HAS ONE — the same rule as the Devices column, for the same reason.
  //
  // A COLUMN HEADER IS A CLAIM THAT THE COLUMN HAS CONTENT. On the member view every ACTIONS cell was empty,
  // which tells a member there are actions they cannot see when there are none for them AT ALL. If Devices is
  // absent for lack of permission, ACTIONS is absent for the same reason; shipping one and not the other
  // would undercut the rule the screen is built on.
  //
  // ⛔ AND THE TEST IS "DOES ANY ROW HAVE AN ACTION", NOT "DOES THE VIEWER HOLD A ROLE". An admin on a roster
  // of owners can act on nobody — `canManageMembership(admin, owner, …)` is false — and a role-based test
  // would keep an empty column for them. It mirrors the CELL's own condition exactly, so the two cannot drift.
  const anyRowHasAction = members.some(
    (m) =>
      emailVerified &&
      canManageMembership(myRole, m.role, "") &&
      m.user_id !== myId,
  );
  const groupAccess = groupAccessState({ isEnterprise, role: myRole, groupCount });
  const shown = filterMembers(members, query);

  return (
    <div>
      <h1 className="text-xl font-semibold text-white">Users</h1>
      <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>
      <ErrorText>{error}</ErrorText>

      {/* ── Access posture ────────────────────────────────────────────────────────────────────────────────
          The wireframe's subtitle promises `role hierarchy · MFA coverage · authentication sources` and the
          product projects ONE of the three. This panel ships that one and NAMES the two it does not have,
          rather than printing a subtitle that promises all three. `MFA enrolled 5/7` in particular is a
          NUMBER, and a reader trusts a number more than prose. */}
      <div className="mt-6">
        <Card>
          <h2 className="text-sm font-semibold text-white">Access posture</h2>
          {/* ⛔ STATES WHAT IS COUNTED. The first version read "Role hierarchy across N members", which claims
              WHO CAN ACT — and the tally counts accounts on the roster, deactivated included. A roster of 7
              with 1 deactivated is TWO FACTS, NOT ONE NUMBER. */}
          <p className="mt-1 text-xs text-slate-400">{rosterSubtitle(members)}</p>
          <dl className="mt-3 flex flex-wrap gap-x-6 gap-y-2">
            {roleDistribution(members).map((t) => (
              <div key={t.role}>
                {/* The zero is rendered, not dropped: an omitted role reads as a role that does not exist. */}
                <dt className="text-xs uppercase tracking-wide text-slate-500">
                  {t.role}
                  {t.n === 1 ? "" : "s"}
                </dt>
                <dd className="text-lg font-semibold text-white">{t.n}</dd>
                {/* The split, per role, only where it exists — so "1 owner" cannot hide a deactivated one. */}
                {t.deactivated > 0 && (
                  <dd className="text-xs text-warn">{t.deactivated} deactivated</dd>
                )}
                <span className="sr-only">{roleTallyLabel(t)}</span>
              </div>
            ))}
          </dl>
          {/* ⛔ THE TWO MISSING FACTS ARE NAMED, NOT OMITTED. Silence here would read as "this org has no MFA
              story", which is false — MFA is enforced and enrollable, it is the per-member PROJECTION that
              does not exist (D1), as with authentication sources (D1b). */}
          {/* ── Groups: OUT OF THE STAT ROW, and registered as a DELIBERATE ADDITION ─────────────────────
              ⛔ THE WIREFRAME HAS NO GROUPS STAT. Its Access posture panel is:
                   title · "role hierarchy · MFA coverage · authentication sources" · {{ teamMap }}
                   · legend (role tiers, MFA enrolled 5/7) · the last-owner copy
              Groups appear ONLY as one axis inside `{{ teamMap }}` — the tripartite role↔user↔group graph,
              which is D2, held, cut on the permission boundary.

              I had put a `Groups 3` tile in the stat row beside owner/admin/member, where a group count reads
              as A FOURTH ROLE TIER — and I did it WITHOUT REGISTERING IT, breaking my own §2.6 rule
              ("additions get the same discipline as cuts") in the story that states the rule.

              THE REASON IT STAYS AT ALL: it is the honest placeholder for the held graph, and it is the only
              thing on this screen that renders the edition/permission seam — the four-gate shape the section
              exists to demonstrate. So it keeps its own line, named as standing in for teamMap.
              Registered: docs/DEFERRAL-REGISTER.md. */}
          <p className="mt-3 border-t border-white/5 pt-3 text-xs text-slate-400">
            <span className="text-slate-500">Group membership</span>{" "}
            {groupAccess.kind === "edges"
              ? `— ${groupAccessLabel(groupAccess)} in this organization.`
              : `— ${groupAccessLabel(groupAccess)}.`}{" "}
            <span className="text-slate-500">
              The role-and-group map is not built yet; this stands in for it.
            </span>
          </p>
          <p className="mt-2 text-xs text-slate-500">
            MFA coverage and authentication sources are not shown per member yet:
            both are enforced by the server but not carried on the roster
            response. Two-factor can still be reset per member from the row
            actions.
          </p>
          {shape.gateNote && (
            <p className="mt-2 text-xs text-slate-400">{shape.gateNote}</p>
          )}
        </Card>
      </div>

      {can(myRole, "member:invite") && emailVerified && org && (
        <InviteForm orgId={org.id} onInvited={() => loadMembers(org.id)} />
      )}

      <div className="mt-6 max-w-sm">
        <Field label="Filter members">
          <Input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="name, email or role"
          />
        </Field>
      </div>

      {/* S14.3 slice A: a real <table>. The roster is tabular — person, role, state, actions per row — and as
          <li> blocks the tier could only find a member by matching their email as free text. The role control
          and the action buttons keep their own accessible names, so they stay queryable INSIDE a cell. */}
      <div className="mt-6">
        <DataTable
          caption="Members"
          rows={shown}
          rowKey={(m) => m.user_id}
          // ⛔ THE FILTER'S EMPTY STATE IS NOT THE ROSTER'S. "No members yet" under an active query would tell
          // an admin their org is empty when they simply typed a name that does not match.
          empty={
            query.trim() !== "" && members.length > 0
              ? `No members match “${query.trim()}”.`
              : "No members yet."
          }
          failed={error != null}
          columns={[
            {
              key: "person",
              header: "Member",
              cell: (m) => {
                // The primary label falls back to the email; the secondary line then has nothing to add.
                const primary = m.name || m.email;
                return (
                <>
                  <span className="text-sm text-white">{primary}</span>
                  {m.user_id === myId && (
                    <span className="ml-2 text-xs text-slate-500">(you)</span>
                  )}
                  {/* ⛔ THE EMAIL IS THE SECONDARY LINE ONLY WHEN A NAME TOOK THE PRIMARY ONE. Unconditionally
                      it rendered the address TWICE for a nameless member — and that is not a corner case:
                      `users.name` is `NOT NULL DEFAULT ''` and `acceptInvitation.name` is OPTIONAL, so 144 of
                      241 users in the review database have an empty name.
                      Found because a MOCK omitted `name` while every seeded member had one — the fixture was
                      LESS representative than the double. The inverse of S14.10, where the double was more
                      permissive than the substrate; the lesson is the same one from the other side. */}
                  {primary !== m.email && (
                    <span className="ml-2 font-mono text-xs text-slate-500">
                      {m.email}
                    </span>
                  )}
                </>
                );
              },
            },
            {
              key: "state",
              header: "State",
              cell: (m) => (
                <>
                  {m.status === "deactivated" && (
                    <span className="text-xs text-warn">deactivated</span>
                  )}
                  {!m.email_verified && m.status === "active" && (
                    <span className="text-xs text-slate-600">unverified</span>
                  )}
                </>
              ),
            },
            // ⛔ SPLICED IN, NOT DIMMED. `...(cond ? [col] : [])` means a viewer without member:manage gets
            // NO <th> and NO cell — nothing in the DOM, nothing announced, nothing keyboard-reachable. An
            // `opacity-40` column would be "gone only to a sighted mouse user".
            //
            // And the reason it is gated at all is the FALSE ZERO: /devices is audience-scoped at the handler
            // (ListForOrg for member:manage, ListForUser otherwise), so a member's list holds only their own
            // devices and a group-by over it would print `0 devices` against every colleague. Measured live:
            // owner@ sees 13 devices / 2 owners, member@ sees 6 / 1.
            ...(shape.showDeviceCount
              ? [
                  {
                    key: "devices",
                    header: "Devices",
                    numeric: true,
                    cell: (m: Member) => {
                      const c = deviceCountFor({
                        role: myRole,
                        devices,
                        userId: m.user_id,
                      });
                      return (
                        <span
                          className={
                            c.kind === "count"
                              ? "text-sm text-white"
                              : "text-xs text-slate-500"
                          }
                        >
                          {c.kind === "count" ? c.n : deviceCountLabel(c)}
                        </span>
                      );
                    },
                  },
                ]
              : []),
            {
              key: "role",
              header: "Role",
              cell: (m) => {
                // Role is editable on any target the actor may manage — INCLUDING self (an owner handing off
                // ownership). The last-owner disable therefore surfaces on the sole owner's OWN role control.
                const canManage =
                  emailVerified && canManageMembership(myRole, m.role, "");
                const assignable = ROLES.filter((r) =>
                  canManageMembership(myRole, m.role, r),
                );
                if (!canManage || assignable.length === 0)
                  return (
                    <span className="text-xs uppercase tracking-wide text-slate-400">
                      {m.role}
                    </span>
                  );
                return (
                  <select
                    className={selectCls}
                    aria-label={`Role for ${m.email}`}
                    value={m.role}
                    disabled={isSoleOwner(m)}
                    title={
                      isSoleOwner(m)
                        ? "An organization must always have at least one owner."
                        : undefined
                    }
                    onChange={(e) => changeRole(m, e.target.value as Role)}
                  >
                    {assignable.map((r) => (
                      <option key={r} value={r}>
                        {r}
                      </option>
                    ))}
                  </select>
                );
              },
            },
            // Spliced, not dimmed — identical to the Devices column above. See `anyRowHasAction`.
            ...(anyRowHasAction
              ? [{
              key: "actions",
              header: "Actions",
              numeric: true,
              cell: (m: Member) => {
                const canManage =
                  emailVerified && canManageMembership(myRole, m.role, "");
                const isSelf = m.user_id === myId;
                // Deactivate is never offered on self — it would log you out, which is a footgun, not a feature.
                if (!canManage || isSelf) return null;
                return (
                  <span className="inline-flex items-center gap-2">
                    {m.status === "active" ? (
                      <Button
                        variant="danger"
                        onClick={() => setActive(m, false)}
                        disabled={isSoleOwner(m)}
                        title={
                          isSoleOwner(m)
                            ? "An organization must always have at least one owner."
                            : undefined
                        }
                      >
                        Deactivate
                      </Button>
                    ) : (
                      <Button
                        variant="ghost"
                        onClick={() => setActive(m, true)}
                      >
                        Reactivate
                      </Button>
                    )}
                    {/* Admin-reset MFA (enterprise; open build answers edition_required). Disenroll-only —
                        it clears the member's 2FA, never signs in as them. */}
                    <Button variant="ghost" onClick={() => setResetTarget(m)}>
                      Reset 2FA
                    </Button>
                  </span>
                );
              },
            }]
              : []),
          ]}
        />
        {/* ⛔ §2.5 OF THE COMMIT-ONE SAID "NO CLIENT-SIDE OWNER COUNT" AND THE SCREEN ALREADY HAD ONE, with a
            written rationale (see isSoleOwner). I ruled on this screen's behaviour without reading the screen
            — the same method error as grepping `Member` and concluding the product did not know.
            Reconciled rather than overwritten, because the existing design is right and the ruling's CONCERN
            is also right, and they are about different things:
              the DISABLE is client-side  — it stops a pointless round-trip and the tooltip teaches WHY
              the REFUSAL is the server's — mutate() surfaces apiErrorMessage(error, fallback), server text
                                            first, and refetches so a lost race self-corrects
            What §2.5 must forbid is PREDICTING THE REFUSAL TEXT, not disabling a control. */}
        {can(myRole, "member:manage") && (
          <p className="mt-2 text-xs text-slate-500">{LAST_OWNER_NOTE}</p>
        )}
      </div>
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
            Remove two-factor authentication for{" "}
            <span className="font-semibold">{resetTarget.email}</span>?
          </p>
          <p className="mt-2 text-xs text-slate-400">
            Their 2FA and recovery codes are cleared and they will be notified
            by email. If your organization requires MFA, they will be asked to
            set it up again at their next sign-in. This does not sign you in as
            them.
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
function InviteForm({
  orgId,
  onInvited,
}: {
  orgId: string;
  onInvited: () => void;
}) {
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("member");
  const [busy, setBusy] = useState(false);
  const [inviteLink, setInviteLink] = useState<string | null>(null);
  const [err, setErr] = useState<string | null>(null);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    const { data, error } = await api.POST(
      "/api/v1/organizations/{orgId}/invitations",
      {
        params: { path: { orgId } },
        body: { email, role },
      },
    );
    setBusy(false);
    if (error || !data)
      return setErr(apiErrorMessage(error, "Could not create the invitation."));
    setEmail("");
    // Build the accept link from THIS origin (correct host regardless of the API's
    // APP_BASE_URL) and show it once for the admin to copy + hand to the invitee —
    // the delivery path when email isn't configured. The email is best-effort on top.
    setInviteLink(
      `${window.location.origin}/accept-invite?token=${data.invite_token}`,
    );
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
          <select
            className={selectCls}
            value={role}
            onChange={(e) => setRole(e.target.value as Role)}
            aria-label="Role"
          >
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
