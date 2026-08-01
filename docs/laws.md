# Tunnex engineering laws (central registry)

Laws minted across stories, previously scattered in `docs/S*-decisions.md`. New laws land here; existing ones get lifted over time. A law is a rule the review probes for and the build must not regress.

## ZERO-TOUCH GATEWAY LAW (founder-ratified 2026-07-18) — S8.2c acceptance bar
**A gateway is brought online by pasting the ONE install command the dashboard emits — and nothing else.** Sites, subnets, enforcing mode, the site→site grant, and a genuinely-separate host *behind* the gateway reaching a far site are ALL achieved by clicking in the dashboard — never by SSH'ing the gateway to add networking. **Any manual networking step on the gateway is a DEFECT, not a runbook line:** no hand-added `--network host`, no `TUNNEX_WG_BACKEND` flag, no `src`-hint on a route, no forward rule, no `ip route` edit. The cross-cloud demo (`walk-artifacts/cross-cloud-demo/`) re-runs clean under this bar — fresh org, two cloud VMs, the only terminal action a pasted join command — and THAT re-run is S8.2c's box-walk.
**BOUNDARY CLAUSE (S8.2c D3):** the law's boundary is the **gateway VM itself** — zero SSH to the gateway after join stands. The **cloud console gets ONE named, guided visit per side** (Azure UDR, AWS route-table + src/dst-check) — un-codeable fabric routing that the site/subnet UI SURFACES as detected/declared, copy-paste snippets + doc links. Guided ≠ manual-gateway-touch; the boundary holds.

## Fixture-fidelity law — TOPOLOGY SIBLING (minted 2026-07-18, cross-cloud demo)
**A site-to-site fixture MUST include a genuinely separate, FORWARDED host behind the gateway — not an interface inside the gateway's own netns.** S8.2's walk used dummy LANs *inside* the gateway container (`10.1.0.1` on a dummy interface); that traffic was **locally-originated, never forwarded**, so the forward chain's LAN→tunnel asymmetry (finding #5) was **invisible** — it survived a full box-walk. Locally-originated ≠ forwarded. A fixture that only originates locally cannot exercise the forward path; the first genuinely-separate behind-gateway host (the CP in the cross-cloud demo) exposed the gap immediately. (Sibling of the [[fixture-fidelity law]]: a test double must not out-capability the substrate; here, a test topology must not under-exercise the path.)

## REPORTED ≢ STORED law — FIXTURE-FIDELITY FAMILY (minted 2026-07-19, S8.5 Slice 5)
**A ruling that assumes plumbing exists must cite the WRITE PATH, not the wire.** S8.5's L1 ruling said "existing plumbing extended, not new telemetry" — true of the *wire* (the agent reports every peer's bytes/handshake) but FALSE of *storage*: `UpsertDeviceStatus` maps a peer pubkey to the active DEVICE on the node, so a site-link peer's pubkey (a remote GATEWAY's wg key, no device row) is a silent no-op — reported, never stored. The verify-before-build instinct (trace the upsert, not the report) caught it at ruling-premise depth; the halt-and-surface followed. Data that crosses the wire is not data at rest — before building on "the CP already has X," find the SELECT that reads X back out, or the column that holds it. (Sibling of the [[fixture-fidelity law]] and [[reported ≢ stored law]] itself: the map is not the territory — a report is not a row.)

## ONE-TRUTH law (lifted to registry 2026-07-18, S8.2c) — a consumer never re-derives a fact its authority already owns
**React-tier corollary (S8.5 Slice 5):** two components each holding a copy of one server list is the one-truth violation in UI form — a mutation in one leaves the other's copy (and everything derived from it, e.g. a button's enabled state) stale. Fix by INVALIDATING the copy (a parent-owned revision signal the mutator bumps and the reader re-loads on) or LIFTING the state — never by patching the stale-fed symptom, which leaves every other consumer of the copy still stale.
**When the control plane owns a fact authoritatively, every other layer CONSUMES it — never computes a second, independent derivation.** A second derivation agrees in the easy case and quietly diverges in exactly the hard cases the feature exists to make safe. Confirmed instances (each a review probe; a new derivation of an owned fact is a finding):
1. **Backend site-hub election** (S8.3) — the web reads the backend-elected `is_site_hub` (`electSiteHub`), never re-elects in TS.
2. **`meta.protocol_version` ceiling** (S8.3) — the CW-confirm reads the server's version ceiling, no TS hardcode.
3. **D2 `LocalSubnets`** (S8.2c) — the agent uses the CP-sent subnet for its route src-hint; it does NOT guess its own site address (egress probe / interface heuristic diverges on-prem WAN≠LAN / multi-homed).
4. **`meta.public_base_url`** (S8.2c, instance #5 of the pattern) — the gateway-enroll command derives the CP's REST/agent URLs from the CP's OWN configured public base URL, NOT `window.location`: the browser URL is whatever alias/tunnel/bare-IP the admin opened, which would bake an unreachable endpoint into the pasted zero-touch command. (Numbered #5 in the running tally though listed 4th here — instance #4 was the S8.1 D2-topology backend-hub overrule, folded into row 1's lineage.)

## SHARED-TERRITORY OWNERSHIP law (founder-ratified 2026-07-19, S8.4) — mark, touch-only-own, refuse-on-collision, AND sweep on every exit
**When the agent (or helper) writes into shared system territory it does not exclusively own — kernel routes, another daemon's firewall chain, the OS resolver directory — it MARKS what it owns, TOUCHES ONLY what it marked, and REFUSES on a collision with a foreign entry rather than clobbering it. AND ownership includes CLEANUP on every exit — graceful, crash, and next-start: a discipline that marks and refuses but cannot sweep its own residue after an unclean exit is HALF a discipline.** A stranded owned entry after a crash is as much an ownership failure as clobbering a foreign one. Confirmed instances (each a review probe; writing into shared territory without the mark/own/refuse/sweep quartet is a finding):
1. **Foreign routes logged** (S3.7/egress) — the agent's route reconcile logs, never blindly deletes, a route it didn't install.
2. **DOCKER-USER chain comment-marked** (S8.2c WF-4) — the agent's forward-accept rules carry a `tunnex-site-fwd` comment; the full-sweep keys on that marker, so a foreign rule in Docker's chain is never removed.
3. **`/etc/resolver` owned-marker + refuse-on-collision** (S8.4) — resolver files carry a `# tunnex-managed` first line; a desired domain colliding with a foreign resolver file is REFUSED (`resolver_domain_conflict`), never overwritten.
4. **Resolver sweep — startup ONLY in S8.4; crash/owner-loss sweep DEFERRED to S8.4b/S8.5** (the F6 → rider → removal arc, terminal form). S8.4 keeps ONLY `CleanStaleResolvers` at helper startup (race-free, `SelfHeal`-precedented). The crash/owner-loss sweep was attempted twice — an eager per-exit sweep (diverged from the kill-switch grace, raced a reconnect) and a release-rider parasitic on the kill-switch (moved FS I/O under the Supervisor lock, left a StateDown persistence gap, added an unsynchronized callback) — **three defect rounds in one component before the terminal move: REMOVE it.** The machinery was dormant — the client resolver path is inert until S8.5 (no resolver files are installed in S8.4), so there was never any crash residue to sweep. It is deferred to S8.4b/S8.5 where it is exercisable, red-able, and walk-provable, with a NAMED ORDERING PRECONDITION: the sweep must land BEFORE the client resolver path activates. **DORMANT-MACHINERY ADDENDUM (the arc's one-sentence law):** build lifecycle machinery only for code paths that are LIVE in the story that builds them — dormant machinery cannot be walk-proven, and unproven lifecycle code is where defect clusters breed (instances: Windows NRPT staging, the resolver release-rider; the S8.2-#5 forward-chain gap as the original demonstration). A sweep that is a *decision-maker* is the smell; a sweep that *rides a decision* is better; a sweep built for code that isn't live yet is the disease.

## Prior laws (lifted from decision docs — pointers)
- **Fixture-fidelity law** (S8.2): a test double must not be more capable than the real substrate (the fake stripped `SiteLink` on read). Contrapositive (S8.3): when the kernel genuinely reports a field, PARSE and COMPARE it (keepalive), so convergence is real not fixtured.
- **Four-word reconcile model** (S8.2): {atomic fetch, fail-static, full-sweep, keep-last-value} — any deviation is a finding.
- **DesiredState-atomic law** (S8.2): a multi-section artifact assembly error fails the WHOLE fetch; the agent holds last-good.
- **Swallowed-audit law** (S8.1): an in-tx `InsertAuditLog` error must PROPAGATE (return), else a mystery commit-rollback.
- **Validator-input-filtering law** (S8.1): never filter the disjointness validator's comparison set to exempt a collision; its value is that it can't be bypassed. **UI corollary** (S8.3): no client-side re-check — one validator, never a second copy in JS.
- **Reassuring-comment law / reassuring-empty law** (S7.x/S8.3): a load failure must never render as a reassuring empty/healthy state; the loudest line on a page must never lie in the reassuring direction (`rulesSummary` failed-state).
- **Render-floor law** (S8.3): render only wire-truth (no decorative telemetry); applies to DERIVED truth too — the UI reads the backend-elected hub, never re-elects (`electSiteHub` one-election).
- **Unlock-then-opt-in** (founder): enterprise features unlock a capability; they never turn enforcement on.

## GATE-REPORT-NEEDS-SHA law (founder-ratified 2026-07-20, S8.6 protocol finding) — a gate report describes committed, pushed state; the sha is in the report, or it is a plan not a gate
**A gate report (build/test/review "passed", "walk-ready", "green") describes COMMITTED, PUSHED state. It leads with the commit sha the gate ran against, or it is a PLAN — a description of intended work — not a gate. A reader accepting a gate demands the sha as a HARD FIELD; a sha-less gate report is void on its face.** The failure this closes: a review disposition, a "walk-ready close", or a slice "accept" can be produced for work that was PAPERED but never built — the report reads identically to a real gate. This stayed invisible precisely because MOST reports already led with a sha (`bb844f6`, `2df19df`, …); the ones that lacked one read the same as the ones that had one, so a paper-only "S8.6 walk-ready" and a real "S8.6 reduce gated (`6d94b79`)" were indistinguishable in prose. The producer side: every gate report leads with its sha. The acceptor side: no gate is passed until the sha is shown and the commit is real (`git log` confirms it, not a summary). Rulings (design dispositions — warn-not-refuse, an approach chosen) STAND independently of code; a GATE (state proven) does not exist without the commit. Confirmed instance: the S8.6 reduce — dispositioned + papered (`fba643c`, `440be19`) and accepted as "walk-ready close" while the #1 enterprise-enforcing cross-site blackhole was live in-branch and the reduce unbuilt; corrected by building + gating it with shas (`6d94b79`/`1bf1e47`/`599409e`), and by voiding every sha-less acceptance in the S8.6/S8.7 train.

## WRITER-OWNERSHIP law — CLAUSE (S8.6 re-review, 2026-07-20): two writers to one field are legal iff both write the SAME pure derivation
The writer-ownership law (a persisted authority with multiple writers must PARTITION its fields by writer) gains a bounded exception: **two writers MAY write the same field iff both write the output of the SAME pure function of the same inputs — convergent by construction, so a race resolves to the same value and the next pass re-derives it.** S8.6 instance: after the failover-tick corrector reduce, BOTH `ReconcileHubSet` (on a bind/unbind/pin/revoke event) and the failover tick write `org_hub_set.configured`, each = `electSiteHubSet`'s output. Legal — a racing stale write self-heals the next tick; the per-field atomic `IS DISTINCT FROM` generation bump stays monotonic. This is NOT a license for two writers with two different derivations (the original clobber class); the guard is *same pure function, same inputs*. The demoted field stays single-writer (the controller); the partition holds where the derivations differ.

## KILL-SWITCH-NO-UNBOUNDED-I/O law (founder-ratified 2026-07-23, WF-A slice-3 review finding #1) — the fail-closed enforcement path must never queue behind latency an attacker or a bad network can set
**No network call, no filesystem stall, nothing whose latency is not locally bounded, may hold a lock that the fail-closed (kill-switch) enforcement path needs to acquire.** The failure this closes: the helper's `SetGatewayPeer` resolved a re-home endpoint (a DNS lookup) *while holding `b.mu`* — the same mutex the dead-man's `FailClosed` takes via the Supervisor. A slow/timing-out resolve would then delay kill-switch enforcement, and it does so during a FAILOVER — precisely the moment a device re-homes AND precisely the moment the kill-switch matters most. Fix (trivial): resolve BEFORE taking the lock; the lock guards only local state mutation. This is the **RR2 lesson** (bounded route syscalls / FS I/O must run OUTSIDE the Supervisor lock — S8.5 crash-sweep) recurring at the DNS tier: same law, new I/O class. Stating it as a rule means the next helper feature that adds a privileged verb meets it at design time, not at review. **REGISTERED companion:** `darwinBackend.Up` / `windowsBackend.Up` resolve the WG endpoint (and now the CP endpoint) under `b.mu` too — the same stall applies, but on a path WF-A did not create; TRIGGER to fix = *the next helper session touching Up's endpoint-resolve path* (out of WF-A scope, not a silent drop).

## NEVER-TRIAGE-FROM-A-TRUNCATED-READ probe (founder-ratified 2026-07-29, EPIC 11 slice 1) — cite the complete output, or state that it is partial

Reading a FRAGMENT and reporting it as the WHOLE. Three instances, all self-caught, all in the same fortnight:

1. **S10.2 merge-time e2e claim.** The e2e job failed in 2m22s on the first run and again later; the DURATION
   matched, so it was reported as "the same pre-existing failure" — without reading the log. It was neither
   pre-existing nor the same: it was a regression that S10.2 itself introduced. A signal that RESEMBLES a
   known state is not evidence of that state.
2. **The S11 govulncheck triage.** CI logs were grepped with `head -8`, which cut the output after the first
   vulnerability per module. "One dependency, two modules" was reported; the complete scan showed **five**
   vulnerabilities across chi, pgx and x/net — three core-dependency bumps that had been invisible.
3. **The gofmt count** (caught before it mattered): an UNPINNED host toolchain flagged ~120 files; the pinned
   one flagged 31. Reporting the first number would have described a defect that did not exist.

**THE PROBE:** never triage from a truncated read. Cite the COMPLETE output, or state explicitly that the read
is partial and what was cut. `head`, `tail`, `grep -m`, and a scrolled terminal all truncate — and a truncated
read of a scanner, a log, or a test run yields a confident, specific, wrong conclusion. The corollary, from
instance 1: **an attribution to a pre-existing cause must cite a GREEN RUN AT A SPECIFIC SHA**, not a
resemblance. (Companion to the census law, which says the same thing about artifacts: only reading it proves
it.)

## CENSUS-THE-MIRROR-SURFACE law (founder-ratified 2026-07-29, S11-6) — on a guard-not-mirrored finding, measure the surface before fixing the instance

**GUARD-NOT-MIRRORED** has now appeared five times across three epics: WF-OVPN-10's keyless peer · the
identity-binding invariant across three consumers · the e2e fixture drift · M1b's two audit helpers ·
S11-5's four unguarded 500 paths on the agent channel. Every instance was found by tripping over it.

**S11-6 is the first time the width of a mirror surface was measured BEFORE it failed.** M1b was diagnosed as
"two audit helpers, one taught the machine branch and one not"; a census run for an unrelated reason (D3.5's
vocabulary question) found **fourteen**, across nine packages — a seven-fold sizing error in the ledger, and
enough to change the item's disposition from a slice to its own story.

**THE LAW: when a guard-not-mirrored instance is found, CENSUS THE MIRROR SURFACE — do not merely fix the
site that failed.** The instance is one member of a set; the fix's real size is the SET'S COUNT, not the
instance. A census costs minutes and answers three things a point-fix cannot: how many siblings exist,
whether the correct fix is "mirror it" or "collapse them", and whether the work is a slice or a story.
Sizing a mirrored-guard item from the instance systematically under-estimates it.

**Corollary — the trigger gets specific.** A censused surface yields a NAMED trigger ("the next change to
audit behaviour", because that change is what must be mirrored N times) rather than a vague one ("someday
unify these"). The next person is forced into the work anyway; the ledger should say so.

## PROVE-A-GUARD-REJECTS law (founder-ratified 2026-07-29, EPIC 11) — a new guard is not accepted until it has failed on a planted violation

**A guard that has only ever passed is indistinguishable from a guard that does nothing.** Green is the state
a correct guard and an inert guard share; only a REJECTION distinguishes them. So a new gate, census, red or
scanner is not accepted on "it runs and passes" — it is accepted when it has been shown to FAIL on a
deliberate violation, and then to pass again once the violation is removed.

Instances that made the case, all in EPIC 11 slice 1–2:
- **govulncheck** — its first honest run exited 3 on `GO-2026-5856`, a reachable `crypto/tls` flaw in the
  pinned toolchain that builds every shipped binary. It rejected because REALITY demanded it, which is
  stronger evidence than a planted vuln would have been.
- **The advisory-job guard** — built after a 3-second Trivy no-op reported green, it then caught the very
  next instance of its own bug (the corrected pin was still wrong) and failed VISIBLY.
- **The toolchain-pin agreement check** — partial bump → exit 1, agreement → exit 0.
- **The 500-path census** (S11-5) — a planted `http.Error(..., 500)` fails with its file:line; removed, passes.
- **The health-kind census** (D3.1) — a planted 14th kind fails by name with the reason; reverted, passes.

**COROLLARY (S11-7) — prove it rejects the HARDEST instance of what it claims to cover, not the easiest.**
A guard that enforces a SUBSET of its own ruling is worse than no guard: it manufactures confidence in
coverage that does not exist. The D3.5 audit census is the measurement. Version one inspected call ARGUMENTS
and found 51 actions — and would have passed while **sixteen branch-selected literals** (`action :=
"x.disabled"; if c { action = "x.enabled" }`) survived untouched, because those are assignments, not
arguments. Extending it to assignments took the count to **68**. Had it shipped at version one, the registry
would have looked complete, the red would have been green, and a quarter of the vocabulary would still have
been bare literals. So: enumerate the SHAPES the defect can take, and plant the awkward one.

**THE LAW:** when you add a guard, plant the violation it exists to catch, watch it fail, then revert and
watch it pass. Record both outcomes. The cost is a minute; the alternative is a green check that has never
once done its job and will not do it the first time it matters. (Companion to ARTIFACT-EXISTS ≠
ARTIFACT-WORKS: this is that law applied to the gates themselves.)

---

## A WITNESS MUST PROVE IT WAS ALIVE ACROSS THE WINDOW IT CERTIFIES

*Minted: EPIC 11 box-walk, Leg 5. A corollary of PROVE-A-GUARD-REJECTS, pointed at evidence-gathering rather
than at guards.*

**A silent witness is indistinguishable from a clean witness, and it fails toward "pass."**

The measurement: Leg 5's first attempt certified "no data-path loss across the roll" from a `ping` log whose
last line was timestamped **nine minutes before the roll began**. The process had died. Its `icmp_seq` gap
detector returned **clean** — a spotless bill of health for a window it never observed. The check could not have
failed, so its passing carried no information at all.

That is the same defect PROVE-A-GUARD-REJECTS exists to catch, one level up. There, the question is whether a
guard can reject a violation. Here it is whether an *instrument* can register the event it is aimed at. A gap
detector over a dead log, a metric scraped before its collector runs, an audit query over a table the code
never wrote to — each returns a confident, meaningless pass.

**THE LAW:** evidence of continuity requires evidence the instrument was running. Three checks, never fewer:

1. **Before** the leg — confirm the witness is replying *now*, with fresh timestamps.
2. **After** the leg — check its timestamp bounds against the leg's own start and end. `head -1` and `tail -1`
   must straddle the window.
3. **Then** the continuity check, grepping **the window explicitly** rather than trusting an aggregate over the
   whole file.

The generalisation beyond witnesses: before believing any negative result — no gaps, no errors, no findings,
zero rows — establish that the thing producing it was in a position to produce a positive one. "Nothing was
observed" and "nothing happened" are different claims, and only one of them is evidence.

---

## COULD THIS CHECK HAVE FAILED? — the censuses need censusing

*Minted: EPIC 11 box-walk. PROVE-A-GUARD-REJECTS generalized from guards to **evidence**.*

**The epic that built five censuses also demonstrated that the censuses needed censusing.**

Three checks in one session could not fail. Every one was green. Every one was vacuous:

1. **A witness dead nine minutes before the leg it certified.** The `ping` log ended before the roll began, and
   its `icmp_seq` gap detector returned **clean** — a spotless bill of health for a window it never observed.
   Accepted, it would have recorded "no data-path loss across the roll" from evidence predating the roll.
2. **A red asserting a tautology.** `degradedKind(KindInput{CertExpired: false})` does not return the
   cert-expired kind — true by construction. The production fix was removed and **everything still passed**. The
   decision under test was never the projection; it was *which rows count as expired*, and that lived in an
   untestable inline expression.
3. **A provenance census verifying the commit but not the product.** Leg 0 asserted the sha and the toolchain on
   an **open-core** codebase and never the edition, so four rebuilds silently swapped the open image for the
   enterprise one — `go build -tags ""` printed in every log, read every time, noticed never. The walk drew
   conclusions from the wrong product for several legs.

None was caught by running it again. Each was caught by one question:

> **Could this check have failed?**

Not *did it pass*. A check that cannot fail is worse than a missing one, because a missing check is visibly
absent while a vacuous check is visibly **green** — and green is what people act on.

**THE LAW:** before believing any negative or confirming result — no gaps, no errors, no findings, zero rows,
"all N are fine" — establish that the thing producing it was in a position to produce the opposite. Concretely:

- **Guards:** remove the fix, watch the guard fail, restore it. (PROVE-A-GUARD-REJECTS, and its S11-7 corollary:
  plant the *hardest* instance, not the easiest.)
- **Instruments:** prove the instrument was running across the window it reports on, with timestamp bounds
  straddling the event. (A WITNESS MUST PROVE IT WAS ALIVE.)
- **Censuses:** census the census. Ask what it enumerates over and name what it therefore cannot see. A census
  of *lookups* said nothing about *pickers*. A census that a health kind **reaches** each surface said nothing
  about whether each surface **decides correctly** about it. A pattern of `[a-z_]+` silently dropped
  `k8s_endpoints_unavailable` because the name contains a digit — the same incomplete-pattern bug the census was
  hunting, inside the census.
- **Provenance:** name every dimension of "the thing under test", not just the convenient one. A commit is not a
  build; a build is not an edition; an edition is not a configuration.

**A check written in the same breath as its fix encodes the author's belief about the fix rather than the
behaviour of the system.** Separating them costs a minute. Not separating them costs the first incident the
check was supposed to prevent.

## A CORRECT ASSERTION, SILENTLY INVERTED BY A PREVIOUS TEST'S STATE (minted 2026-08-01, web component tier slice 1)

**Belongs to the [[COULD THIS CHECK HAVE FAILED?]] family, and it is DISTINCT from every member of it.** The
others are bad assertions — they cannot fail, or they fail for the wrong reason. **This one is a CORRECT
assertion, correctly written, whose verdict is inverted by state left behind by a test that already ran.**

**THE INSTANCE.** `apps/web/vitest.config.ts` sets no `globals: true` and no setup file, so
`@testing-library/react`'s automatic `afterEach` cleanup **never registers**. Renders accumulate in one
document across every test in a file. The existing foothold never hit it because it renders exactly once; the
first multi-render file hit it immediately.

**And the direction it failed in is the whole point:**

```
it("offers no revoke control on an already-revoked gateway", …)
  →  Found multiple elements with the role "button" and name "Revoke"
```

**An assertion about a button's ABSENCE became a false PRESENCE** — because a *previous* test's revoke button
was still in the document. Reverse the leak and the same mechanism turns a genuine presence into a false
absence. **Either way the test reports on a document nobody wrote.**

**WHY IT IS INFRASTRUCTURE AND NOT AN AUTHORING ERROR: it cannot be caught by reading any single test.** Every
test in the file is individually correct. The defect lives in what the harness does *between* them, which no
amount of care inside one test can see. That is what distinguishes it from the rest of the family — those are
found by reading the check and asking whether it can fail; this one is found only by running two checks in one
file, or by knowing the harness.

**THE GUARD: an explicit `afterEach(cleanup)` with its REASON INLINE**, not as boilerplate. Boilerplate gets
deleted by whoever is tidying imports; a line that says why it exists does not. **A tier convention, adopted
before the second screen was written rather than after a false green shipped.**

**THE ASYNC FORM (added 2026-08-01):** the same defect arrives without any leak when a test asserts against a
tree it has **not finished waiting for**. Sites' first test waited on the PENDING chip and asserted the APPROVED
one, which renders later; its sibling test, over the SAME two elements, passed only because it happened to wait
on the later one. **Two tests over the same elements disagreeing is the tell.** Both forms are one sentence: **a
correct assertion made against a tree that is not yet — or no longer — the tree it describes.** Guard: a
`waitFor` must cover EVERY element the assertions touch (tier query rule 5).

**GENERALISED, past React:** any harness where one case can leave state the next case reads — a shared temp
dir, a package-level fixture, a module-level cache, a database not rolled back — has this shape available to
it. **The question is not "is my assertion right" but "what did the previous test leave behind that my
assertion can read?"**

*(The six other mechanisms in this family — half-fold, tautological guard, fixture-restates-production,
TRUE-BY-STRUCTURE, SAMPLED-SLOWER-THAN-THE-EVENT, ASSERTS-A-DIFFERENT-EVENT-THAN-IT-WAITS-ON — were minted
during EPIC 13 and arrive on `main` with that epic's merge. This entry is written self-contained so it reads
correctly before and after.)*

### TWO MORE INSTANCES, BOTH FOUND BY USING THE TOOL RATHER THAN READING IT (2026-08-01, web tier slice 2)

**INSTANCE — fixture-restates-production, written INSIDE the check guarding against it.** D4's sibling
assertion exists to prove three surfaces agree about revoked-row suppression. Its first draft covered the third
surface with a three-line `DeviceRowProbe` that re-encoded the production guard
(`status !== "revoked" && <badge>`) in the test file. **It would have passed forever even if `Devices.tsx` lost
its guard**, because the assertion would have been reading the test's own copy of the rule. **Caught
pre-commit** and replaced with the real page; **the near-miss is recorded in the test file itself**, not just in
the commit, because the next author to reach for a probe will read the file and not the history.

**INSTANCE — the SIXTH mechanism applied to the TOOL.** `mutate.sh` asserted *"the test failed"* and concluded
*"the guard rejects the mutation."* A **broken test command fails identically.** It happened: invoked from the
repo root as `vitest run --root apps/web test/x.test.tsx`, the relative path in `vi.mock("../src/lib/api")`
stopped resolving, nothing was mocked, and **all four tests failed — including two the mutation cannot affect.**
The script printed *"test failed under the mutation, as required."*

That is **ASSERTS-A-DIFFERENT-EVENT-THAN-IT-WAITS-ON** with the tool as subject: it waits on *exited non-zero*
and asserts *the guard bit*.

**THE FIX: re-run the command UNMUTATED and refuse if it also fails.** `prove-fix.sh` has always had the mirror
of this — *"the red must FAIL before the edit"* — and **`mutate.sh` never had it.** Now it does, proven to bite
with a deliberately broken command.

**THE PATTERN ACROSS BOTH TOOL DEFECTS THIS WEEK:** the `set -u` abort and this false verdict were **both found
by USING the tool, neither by reading it.** A self-test proves a tool runs; **only a real subject proves it
concludes correctly.** Keep the self-tests, and keep distrusting a green verdict whose baseline nobody checked.

## APPLY THE DETECTOR TO THE MEASUREMENT (minted 2026-08-01, EPIC 13 + web tier)

**A MEASUREMENT ERROR THAT PRODUCES A PLAUSIBLE FINDING IS MORE DANGEROUS THAN ONE THAT PRODUCES NONSENSE.**
Nonsense gets re-run. **A plausible finding gets written down and acted on.**

**Two instances in one day, and BOTH failed in the dangerous direction — plausibly:**

1. **`grep -c` on a 2.9 MB file of 405 lines**, counting `<div`. It counts **LINES containing a match**, not
   occurrences, so it undercounts by orders of magnitude — **uniformly**, which is what makes it dangerous.
   Nothing looks anomalous; every figure is simply small. The correct measure (`grep -o … | wc -l`) returned
   **1,018**.
2. **`grep -o 'case "[a-z_]*"'` excluding DIGITS**, so `k8s_endpoints_unavailable` did not match. The conclusion
   available from that output was *"WF-S11-7's kind is unrendered on main"* — **a live regression of a
   named, famous finding.** It is handled (`healthview.ts:44`). **The pattern was wrong, and the wrong answer
   was the interesting one.**

**THE DIAGNOSTIC: before reporting anything a grep found, verify the PATTERN against an input whose answer is
already known.** The detector this repo applies to checks applies to measurements: *could this measurement have
produced a different answer for a reason unrelated to its subject?*

**Neither instance reached a claim.** Both were caught by re-measuring before writing — instance 1 by asking why
a 2.9 MB file would hold so few divs, instance 2 by reading the source at the line the count implied was empty.
**Recorded because they were caught, not because they were harmless: the same error one step later is a false
finding in a walk record.**

**THE POSITIVE FORM, from the same session:** the responsive audit counted **`min-width:0` = 104
SEPARATELY** — a number taken **deliberately so it could not be misread as responsiveness** (it is the flexbox
min-content idiom). **Measuring the thing that would produce a wrong reading, in order to exclude it, is the
same care in its constructive direction.**

### POSITIVE INSTANCES — two seams where fixture-restates-production was AVOIDED (web tier slice 3)

**1. THE MIRROR CENSUS AS A DELIBERATE LITERAL.** `test/kuberneteswiring.test.tsx` lists every
`policy_degraded_kind` the contract allows **as a hand-maintained literal**, and asserts each reaches a
renderer. **Deriving that list from the generated type would prove nothing** — it would compare the source to
itself and pass by construction. **Two lists, maintained separately, shown to agree.** Same family as D10's
golden vector and the twin canonical-hash goldens: *the coupling is asserted, not assumed.*

**2. THE REAL `AuthProvider`, NOT A STUB.** The Kubernetes screen reads `useAuth()` for its role gate. Stubbing
the context would put **the test's copy of the gate** under assertion instead of **the product's** — 
fixture-restates-production **at the seam where it is easiest to fall into**, because stubbing a context is the
obvious move and the test still goes green.

## A GUARD ENFORCED BY TYPES BEATS ONE ENFORCED BY DISCIPLINE (minted 2026-08-01, web tier slice 4)

**Discovered by trying to break it, which is the only way this is ever discovered.**

The `loadOne` law says a failed load must never render as an empty or a defaulted value. On the Access screen
that would be *"0 rules — ALL traffic denied."* on a load that never returned — **an authorization claim
invented by the client.**

**The naive mutation for it DOES NOT COMPILE.** `Loaded<T>` is a discriminated union:

```ts
export type Loaded<T> = { ok: true; data: T } | { ok: false; error: string };
```

**`.data` is unreachable without narrowing `.ok`.** Dropping the `!rulesResult.ok` check does not produce a
wrong render — **it produces a type error.** The mutation had to attack the failed branch's *output* instead.

**THE FINDING IS THE STRENGTH, NOT THE INCONVENIENCE.** The law is enforced **by construction** on this path:
a future author cannot reintroduce the defect by forgetting a check, because the compiler will not let them
read the value they forgot to check for.

**THE STANDARD, AND THE REGRESSION RISK: `Loaded<T>` MUST NOT BE LOOSENED.** Widening it to
`{ ok: boolean; data?: T; error?: string }` — the shape a hurried refactor reaches for, because it is easier to
construct — **would silently convert a compile-time guarantee into a discipline nobody is auditing.** Nothing
would fail. No test would go red. The guard would simply stop existing.

**BINDING ON THE REDESIGN.** A re-architecture touches every screen's load path. **`Loaded<T>`'s discriminated
shape is a thing the redesign must not regress**, and it is exactly the kind of guard that disappears without
anyone noticing, because its absence looks like ordinary code.

**GENERALISED:** when a law can be encoded in a type, encode it there. A rule enforced by review is enforced
until the reviewer is busy; a rule enforced by the compiler is enforced at 3am by someone who never read the law.

## A COMMAND THAT PRODUCES OUTPUT BUT NOT ITS EFFECT READS AS GREEN IN EVERY LOG (minted 2026-08-01)

**TWO TOOLS, SAME SHAPE: each ANNOUNCED SUCCESS WITHOUT DOING ITS WORK, and each was believed.**

1. **`mutate.sh` printed `Restoring.` and did not restore** (`3c9c16f`). Every run left the mutated file in the
   tree while claiming it had been put back.
2. **`npx tsc --noEmit` run from the REPO ROOT** resolved to a different package, which prints
   *"This is not the tsc command you are looking for"* and exits. **Four slices were reported "typecheck
   clean" on that output.** Nothing was checked. Nothing said so.

**THE SHARED SHAPE: output is not effect.** A log line proves a command RAN. It proves nothing about what it
DID, and a human scanning for red sees neither.

**THE DIAGNOSTIC: RUN THE COMMAND THE GATE RUNS, FROM WHERE THE GATE RUNS IT — never a convenient equivalent.**
`npx tsc` from the repo root is not `pnpm --filter @tunnex/web typecheck`. `vitest --root apps/web` from the
root is not `vitest` from `apps/web` — that one broke a relative mock path and produced a **false mutation
verdict** the same day. The convenient equivalent is where the difference hides.

### THIRD INSTANCE OF THE MEASUREMENT CLASS — and it compounds with the first two

With `grep -c` on a 405-line file and `grep -o '[a-z_]*'` excluding digits, this makes **three measurement
errors in one session**, all of which produced a **plausible** answer. See *APPLY THE DETECTOR TO THE
MEASUREMENT* above: nonsense gets re-run; a plausible answer gets written down.

### THE SHARPEST INSTANCE — the near-miss that found all of it

**`tsconfig.json` included only `src`.** So `tsc --noEmit` — the gate behind
`pnpm --filter @tunnex/web typecheck` — **had never typechecked a single file in the component test tier.**
Five slices were written and reported green against a check that never looked at them.

**It was found by asking WHERE THE ASSERTION WOULD ACTUALLY RUN, not by reading anything.** The `Loaded<T>`
contract was about to be placed in `test/`, where it would have been **a check that cannot fail — inside the
artifact written to prevent checks that cannot fail.** One question ("does the gate see this directory?")
caught the contract's placement, the tier's missing type coverage, and the vacuous `tsc` invocation together.

**WHAT THE FIX FOUND, stated plainly rather than assumed:** scoping `test/` in and running the gate's own
command surfaced **exactly two errors, neither in the new tier** — a duplicate `import { ruleRow }` in
`test/policyview.test.ts` re-importing a symbol already imported at the top of the same file. **Behaviourally
benign** (both bindings resolved to production), **genuinely a TS2300**, and **invisible to every gate for as
long as it has existed.** The five slices were **clean once scoped correctly — which is a different statement
from "assumed clean", and only one of them was ever true.**

### FOURTH INSTANCE — A SUITE PASSING ON A PHANTOM DEPENDENCY (2026-08-01)

`@testing-library/react` and `jsdom` were **not in `apps/web/package.json` and not in `pnpm-lock.yaml`**. They
existed only physically in one machine's `node_modules`, installed while on a different branch. **Five slices of
the component tier ran green on them.** On a clean checkout — or on CI — nothing would have resolved.

**THE SHAPE ACROSS ALL FOUR, and it is one reading, not four:**

| # | mechanism | the output |
|---|---|---|
| 1 | `mutate.sh` printed `Restoring.` and did not restore | a success line |
| 2 | `npx tsc` from the repo root resolved to a different package | a banner, then exit |
| 3 | `vitest --root apps/web` from the root broke the relative mock path | a red that meant nothing |
| 4 | the suite resolved deps present on **one machine only** | 217 passing tests |

**THE CHECK RAN. IT PRODUCED OUTPUT. THE OUTPUT DID NOT MEAN WHAT IT APPEARED TO MEAN.** Four different
mechanisms, one reading — and in three of the four the output was *green*, which is the direction nobody
re-examines.

**THIS IS A REPEAT OF THIS REPO'S OWN HISTORY.** S6.0b: an unanchored `secrets/` in `.gitignore` kept
`apps/api/internal/secrets` **out of git** — built fine locally, **broke every fresh clone**. Same class,
already learned once, rediscovered here in a different costume. **A lesson learned in one toolchain does not
transfer to another by itself; it has to be re-earned or encoded.**

**THE RULE THIS EARNS: A SLICE IS NOT GREEN UNTIL THE GATE PASSES — the gate AS CI RUNS IT, not a local
equivalent, and EVERY slice, not once at the end.** For this tier that is `make web-gate` (typecheck + test +
build, Node 20 container). `vitest` passing in a developer shell is a useful signal and is **not** evidence.

**THE SHARPEST FRAMING:** the tier exists to catch defects a surface's own tests cannot see — **and its first
five slices carried a defect its own gate could not see.** That is not irony. It is the evidence that *"the gate
is the authority"* has to mean **the gate as CI runs it, on every slice.**

## A DRIFT GUARD PROTECTS THE SOURCE↔ARTIFACT RELATIONSHIP, NOT THE ARTIFACT FROM TAMPERING (2026-08-01, S14.1)

**`make generate-check` depends on `make generate`.** So it REGENERATES before it diffs — and a hand-edit of an
emitted artifact is **silently overwritten**, leaving a clean tree and a **green** check.

```
hand-edit the emitted file  →  generate-check  →  generate overwrites it  →  diff clean  →  GREEN
```

**A HAND-EDIT IS SELF-HEALING, NOT DETECTED.**

**What the guard DOES catch is a STALE COMMIT:** source changed, artifact not regenerated. Proven by editing
`packages/shared/src/tokens.ts` without regenerating — the guard printed the before/after lines and failed.

**THIS IS TRUE OF EVERY GENERATED ARTIFACT IN THIS REPO** — `api.d.ts`, `rbac-policy.json`, the sqlc output,
the RBAC mirror. The property is the same for all of them because the target is the same shape.

**Why it matters even though it is arguably fine:** the edit cannot survive the next `make generate`, and CI
runs the check on every PR, so tampering never reaches `main`. **But "the artifact is guarded" and "the artifact
matches its source" are different claims**, and only the second is true. Anyone reasoning about what the drift
guard protects should be reasoning about the second.

**NOT FIXED — registered as a repo-wide property. TRIGGER: the next change to `make generate-check`, or a
finding that depends on artifact tampering being detected.**
