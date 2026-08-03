# REGISTER — destructive server behaviours reachable by a principal the UI does not describe accurately

**Against the CURRENT product. None of these wait for EPIC 15.**

Three rows, one class. Each is a **server-side destructive or privilege-affecting behaviour**, each is
**reachable by or about a principal the interface misrepresents**, and each was found by reading the grant
table or the FK actions rather than by using the screen.

---

## ⛔ 1 — `operator` HOLDS `PermPolicyManage`, SO A NON-HUMAN PRINCIPAL CAN WRITE ITS OWN ACCESS RULES

**MEASURED** (`rbac.go`, `machine_credentials`): a machine credential's role is **fixed at `operator` at mint**
(D3), and `operator` holds `PermOrgView · PermPolicyView · **PermPolicyManage** · PermMemberList`.

> ## **THE SAME GRANT IS CORRECT FOR A GitOps OPERATOR AND INVERTS THE THREAT MODEL FOR AN AGENT.** A
> ## GitOps operator writing policy IS the product. A principal that can be talked into a request, and
> ## can then write the rule that permits it, is a compromised principal granting itself what it was denied.

**Why it is live today, not an EPIC-15 concern:** machine credentials ship now, the UI presents them as an
integration credential, and **nothing on that screen says the holder can author access policy.** S14.13
already registered that no revoke control ships over `managed_by_machine`; this is the other half — the
capability itself is under-described.

**Not fixed here** because splitting the role is a grant-table change with a generated mirror and a drift
guard, and it must not be guessed at in the tail of another story.

---

## ⛔ 2 — `CountOwners` COUNTS OWNER *ROWS*, NOT OWNERS WHO CAN SIGN IN

**PROVEN unrecoverable lockout** (S14.11): deactivate both owners and the last-owner invariant holds on paper
while nobody can sign in. Red written: `docs/probes/lockout_probe_test.go.txt`. The client `ownerCount` mirrors
the server deliberately and **must not be fixed independently**.

---

## ⛔ 3 — `managed_by_machine` IS A PRIVILEGE CHANGE DISGUISED AS A CLEANUP ACTION

Registered S14.13. An inbound FK with **`SET NULL`** means revoking the credential does not cascade-delete —
something referencing it goes null instead. **No revoke control ships over `managed_by_machine`** (founder
ruling) precisely because the blast radius is not described.

---

# WHY THESE THREE SIT TOGETHER

Each is a **server behaviour the UI does not describe**, and in each case the misdescription is about a
**principal or an invariant rather than a widget**:

| row | the principal or invariant | what the UI implies |
|---|---|---|
| `operator` + `PermPolicyManage` | a non-human principal | an integration credential, not a policy author |
| `CountOwners` | the last-owner invariant | that it protects sign-in access; it counts rows |
| `managed_by_machine` | a machine-owned object | that revoking is cleanup, not a privilege change |

> ## **THEY ARE THE SAME DEFECT AT THREE SITES: A CAPABILITY OR A GUARANTEE THAT IS TRUE OF THE DATABASE**
> ## **AND NOT TRUE OF THE SENTENCE THE OPERATOR READS.**

**All three are server changes with generated mirrors or proven reds already written. They belong to one
server story, not to three screens.**
