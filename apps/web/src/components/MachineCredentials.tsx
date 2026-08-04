import { useEffect, useState, type FormEvent } from "react";
import {
  api,
  apiErrorMessage,
  loadOne,
  type Loaded,
  type MachineCredential,
  type Member,
} from "../lib/api";
import { endSentence, relativeAge } from "../lib/format";
import { Button, Card, ErrorText, Field, Input } from "./ui";
import { OneTimeSecretModal } from "./OneTimeSecret";

// MachineCredentials (S10.2) — the owner-only Settings panel to mint / list / revoke the GitOps operator's
// machine credential. The token is shown ONCE via the shared OneTimeSecretModal (the same ceremony as a
// device config / .ovpn / recovery codes); the list shows the keyed FINGERPRINT only — the server never
// re-serves the secret. Rendered only for machine:manage (owner) — the endpoints are owner-gated, so a
// non-owner would only get 403s here.

/**
 * How many credentials this organization is currently REFUSING.
 *
 * ⛔ Not "how many are untidy". `MachineAuth` returns `nil, nil` on a NULL owner, so this is a count
 * of live failures — exported so the count in the banner and the per-row badge cannot drift apart.
 */
export function refusedCount(creds: readonly MachineCredential[]): number {
  return creds.filter((c) => !c.owner_user_id).length;
}

export function MachineCredentials({
  orgId,
  canManage,
}: {
  orgId: string;
  canManage: boolean;
}) {
  // ⛔ Loaded<T>, NOT `T[] | null`. A bare array cannot distinguish "no credentials" from "the list failed to
  // load", and on THIS screen that difference is the whole point: an unreachable query rendering as an empty
  // list is "migration complete" written by an error path.
  const [creds, setCreds] = useState<Loaded<MachineCredential[]> | null>(null);
  const [owner, setOwner] = useState<Record<string, string>>({});
  // The org roster, for the owner picker. ⚠ A FAILED ROSTER IS NOT AN EMPTY ORG — if it cannot load, the
  // picker offers nothing and the copy below says so, rather than implying there is nobody to choose.
  const [members, setMembers] = useState<Member[]>([]);
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const [secret, setSecret] = useState<string | null>(null); // the tnxm_ token, in state ONLY, never re-fetched

  // ⛔ ROUTED THROUGH loadOne. A raw api.GET was here, and it is review-refused on a list whose emptiness is
  // user-meaningful: `error` and a network REJECTION are two different paths, and a component that reads only
  // `data` renders a reassuring empty state for both.
  async function load() {
    setCreds(
      await loadOne<MachineCredential[]>(() =>
        api.GET("/api/v1/organizations/{orgId}/machine-credentials", {
          params: { path: { orgId } },
        }),
      ),
    );
  }

  // Assign the human this credential acts for. The admin CHOOSES — see the copy below.
  async function assign(credentialId: string) {
    const userId = owner[credentialId];
    if (!userId) return;
    setBusy(true);
    setErr(null);
    const { error } = await api.PUT(
      "/api/v1/organizations/{orgId}/machine-credentials/{credentialId}",
      { params: { path: { orgId, credentialId } }, body: { user_id: userId } },
    );
    setBusy(false);
    if (error) return setErr(apiErrorMessage(error, "Could not assign an owner."));
    setOwner((o) => ({ ...o, [credentialId]: "" }));
    void load();
  }
  useEffect(() => {
    void load();
    void (async () => {
      const r = await loadOne<Member[]>(() =>
        api.GET("/api/v1/organizations/{orgId}/members", {
          params: { path: { orgId } },
        }),
      );
      if (r.ok) setMembers(r.data);
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgId]);

  async function mint(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr(null);
    const { data, error } = await api.POST(
      "/api/v1/organizations/{orgId}/machine-credentials",
      {
        params: { path: { orgId } },
        body: { name: name.trim() },
      },
    );
    setBusy(false);
    if (error)
      return setErr(apiErrorMessage(error, "Could not mint the credential."));
    setName("");
    setSecret(data?.token ?? null); // shown once — the server never re-serves it
    void load();
  }

  async function revoke(id: string) {
    const { error } = await api.DELETE(
      "/api/v1/organizations/{orgId}/machine-credentials/{credentialId}",
      {
        params: { path: { orgId, credentialId: id } },
      },
    );
    if (error)
      return setErr(apiErrorMessage(error, "Could not revoke the credential."));
    void load();
  }

  return (
    <Card>
      <h2 className="text-sm font-semibold text-slate-300">
        GitOps operator credentials
      </h2>
      <p className="mt-1 text-xs text-slate-500">
        A machine credential the Tunnex Kubernetes operator uses to manage this
        organization over the API. It authenticates as a system actor — audited
        as <span className="font-mono">operator:&lt;name&gt;</span>, never a
        user. The token is shown once at mint; if lost, revoke and re-mint.
      </p>
      <ErrorText>{err}</ErrorText>

      {canManage && (
        <form onSubmit={mint} className="mt-3 flex items-end gap-2">
          <div className="flex-1">
            {/* "Credential name", NOT "Name" — the Settings page already has an org "Name" field, and two
                controls sharing an accessible name are announced identically by a screen reader (S11-1). */}
            <Field label="Credential name">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="gitops"
              />
            </Field>
          </div>
          <Button type="submit" disabled={busy || name.trim() === ""}>
            {busy ? "Minting…" : "Mint credential"}
          </Button>
        </form>
      )}

      {/* ⛔ THREE DISTINGUISHABLE STATES, AND THE THIRD IS THE REASON THIS SCREEN IS HARD.
          none · all owned · THE LIST FAILED TO LOAD. An unreachable query rendering as an empty list is
          "migration complete" written by an error path — and on a migration screen that is exactly the
          reassurance that must be earned rather than defaulted to. */}
      {creds === null ? (
        <p className="mt-3 text-xs text-ink-secondary">Loading…</p>
      ) : !creds.ok ? (
        <p
          data-state="load-failed"
          className="mt-3 rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger"
        >
          Could not load machine credentials — {endSentence(creds.error)}{" "}
          <strong>This is not the same as having none.</strong> Retry before concluding anything about
          ownership.
        </p>
      ) : creds.data.length === 0 ? (
        <p data-state="none" className="mt-3 text-xs text-ink-secondary">
          No machine credentials exist in this organization — there is nothing to assign.
        </p>
      ) : (
        <>
          {/* ⚠ ABOVE THE ROWS, DELIBERATELY. A qualifier under a list is read after the list is already
              believed. */}
          {/* ⛔ A CLAIM ABOUT OWNERSHIP, NOT ABOUT HEALTH — AND THE FIRST VERSION SAID
              "the migration is complete for this organization", WHICH IS BOTH.

              It reads as "everything is fine here", and this banner cannot know that. It knows one
              predicate: no credential is being refused FOR WANT OF AN OWNER. A credential can still be
              revoked, expired at the far end, pointed at a dead operator, or owned by someone who left.
              ⚠ AND "the migration is complete" IS FALSE ON ITS OWN TERMS: this is the expand half of an
              expand/contract migration, and step 4 — the NOT NULL contract — has not run. An org can
              reach every-row-owned and still have the column nullable beneath it.

              > It is the exact mirror of the refusal banner: that one names what is failing, this one
              > names what is NOT. Neither says anything about whether the fleet is well. */}
          {creds.data.every((c) => c.owner_user_id) && (
            <p data-state="all-owned" className="mt-3 rounded-md border border-ok/40 bg-ok/5 px-3 py-2 text-xs text-ok">
              Every machine credential has an owner — none is being refused for want of one.
            </p>
          )}
          {/* ⛔ UNASSIGNED IS AN OUTAGE, NOT UNTIDINESS — AND THE FIRST VERSION OF THIS SCREEN SAID SO
              ONLY IN A FOOTNOTE UNDER THE LIST.
              `MachineAuth` returns `nil, nil` on a NULL owner, so every row below without one is being
              REFUSED RIGHT NOW. A calm amber `unassigned` chip beside it renders a live outage as a
              tidy label — the reassuring-empty class inverted: not a zero claiming success, but a
              failure claiming to be housekeeping.
              ⚠ ABOVE THE ROWS AND IN THE DANGER TONE, for the same reason the all-owned banner is
              above them: a qualifier under a list is read after the list is already believed. An
              operator whose GitOps runner is dead must learn it from this screen, not from the
              runner. */}
          {creds.data.some((c) => !c.owner_user_id) && (
            <p
              data-state="some-refused"
              className="mt-3 rounded-md border border-danger/40 bg-danger/5 px-3 py-2 text-xs text-danger"
            >
              <strong>
                {refusedCount(creds.data) === 1
                  ? "1 machine credential is being refused right now."
                  : `${refusedCount(creds.data)} machine credentials are being refused right now.`}
              </strong>{" "}
              A credential with no owner cannot authenticate — any operator using one is already
              failing. Assign an owner to restore it.
            </p>
          )}
          <ul className="mt-3 space-y-1">
            {creds.data.map((c) => (
              <li
                key={c.id}
                data-owned={c.owner_user_id ? "yes" : "no"}
                className="flex items-center justify-between gap-3 rounded-md bg-white/5 px-3 py-2 text-sm"
              >
                <span className="min-w-0 text-slate-200">
                  {c.name}
                  <span className="ml-2 font-mono text-xs text-slate-500">
                    {c.fingerprint}
                  </span>
                  <span className="ml-2 text-xs text-slate-500">
                    created {relativeAge(c.created_at)}
                    {/* ⛔ "last seen", LABELLED. `last_used_at` is stamped on every successful auth — it is
                        LAST AUTHENTICATED AT and nothing more. A credential idle for a day may be an hourly
                        GitOps reconcile or abandoned, and this column cannot tell them apart. The spec now
                        says so explicitly; the UI must not re-invent what the spec refused. No in-use badge,
                        no active/idle, no threshold. */}
                    {c.last_used_at
                      ? ` · last seen ${relativeAge(c.last_used_at)}`
                      : " · never seen"}
                  </span>
                  {/* ⛔ THE MOMENT A CREDENTIAL ACQUIRED AN OWNER, THAT FACT BECAME INVISIBLE. On a
                      screen whose entire purpose is accountability, the assigned state was the one
                      state that said nothing at all.
                      ⛔ AND IT READS `owner_email`, THE SERVER'S RESOLUTION, NOT THE MEMBER ROSTER (D22
                      ruled). The roster lookup that was here is REMOVED rather than kept alongside: two
                      resolvers for one fact is the one-truth violation in its plainest form, and the
                      client's copy is the one that CANNOT SEE A DEPARTED MEMBER.
                      ⚠ AND THAT IS THE ROW THIS SCREEN EXISTS FOR. Nothing pins a membership, so an owner
                      who leaves the org keeps the credential and drops off the roster — the roster-based
                      render went blank at exactly the moment accountability mattered. The server resolves
                      from `users`, which survives both leaving and deactivation.
                      The fallback is for a genuinely unrecoverable identity only; ON DELETE RESTRICT means
                      an assigned credential cannot outlive its user row, so it should be unreachable. */}
                  {c.owner_user_id ? (
                    <span className="ml-2 text-xs text-ink-secondary">
                      · owner{" "}
                      <span className="text-slate-300">
                        {c.owner_email ?? "no longer a known account"}
                      </span>
                    </span>
                  ) : (
                    <span
                      data-badge="refused"
                      className="ml-2 rounded border border-danger/40 px-1.5 py-0.5 text-[10px] text-danger"
                    >
                      no owner — refused
                    </span>
                  )}
                </span>
                <span className="flex shrink-0 items-center gap-2">
                  {canManage && !c.owner_user_id && (
                    <>
                      <label className="sr-only" htmlFor={`owner-${c.id}`}>
                        Owner for {c.name}
                      </label>
                      {/* ⛔ NO SUGGESTED OWNER. `created_by` does not exist, so there is nothing to pre-select
                          from — a default here would be a client-invented value sitting where a server fact
                          belongs. The placeholder says the system does not know. */}
                      <select
                        id={`owner-${c.id}`}
                        value={owner[c.id] ?? ""}
                        onChange={(e) =>
                          setOwner((o) => ({ ...o, [c.id]: e.target.value }))
                        }
                        className="rounded border border-line bg-surface-inset px-2 py-1 text-xs"
                      >
                        <option value="">Choose an owner…</option>
                        {/* ⛔ UNVERIFIED ACCOUNTS ARE NOT OFFERED (D21 ruled). Ownership is an
                            accountability claim, and an account that cannot perform org mutations —
                            requireVerifiedUser gates those — cannot be held accountable for what a
                            credential does.
                            ⚠ THIS FILTER IS PRESENTATION, NOT ENFORCEMENT. The server refuses an
                            unverified owner at the handler AND inside the UPDATE statement; removing
                            this line would change what is offered, not what is permitted. */}
                        {members
                          .filter((m) => m.email_verified)
                          .map((m) => (
                            <option key={m.user_id} value={m.user_id}>
                              {m.email}
                            </option>
                          ))}
                      </select>
                      <Button
                        variant="ghost"
                        disabled={busy || !owner[c.id]}
                        onClick={() => void assign(c.id)}
                      >
                        Assign
                      </Button>
                    </>
                  )}
                  {canManage && (
                    <Button variant="ghost" onClick={() => revoke(c.id)}>
                      Revoke
                    </Button>
                  )}
                </span>
              </li>
            ))}
          </ul>
          {/* The refusal is stated ABOVE the rows now. What is left here is the thing that genuinely
              belongs under the list: why there is nothing to pre-select. */}
          {canManage && creds.data.some((c) => !c.owner_user_id) && (
            <p className="mt-2 text-[11px] text-ink-secondary">
              Tunnex does not record who minted a credential, so it cannot suggest an owner — choose the
              person accountable for what this credential does.
            </p>
          )}
        </>
      )}

      {secret && (
        <OneTimeSecretModal
          title="Machine credential"
          caption={
            <>
              This is the operator&rsquo;s bearer token. It is shown{" "}
              <span className="font-semibold">exactly once</span> and can never
              be retrieved again — save it into the operator&rsquo;s Secret now.
              If lost, revoke and re-mint.
            </>
          }
          secret={secret}
          onDismiss={() => setSecret(null)}
        />
      )}
    </Card>
  );
}
