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

## INTERNAL USE AND REDISTRIBUTION ARE DIFFERENT LICENCE QUESTIONS — AND A SELF-HOSTED PRODUCT IS ALWAYS THE SECOND (2026-08-01, EPIC 14)

**"Free for commercial use" answers the wrong question for this product.**

**Tunnex SHIPS A BUILT BUNDLE to customers who run it themselves.** That is **redistribution** of every
dependency's compiled code — not internal use on a site we operate. The two permissions are granted separately
and a licence may grant one without the other.

**THE INSTANCE.** GSAP 3.15.0 is genuinely free for commercial use. Its licence field is
`"Standard 'no charge' license: https://gsap.com/standard-license"` — **a custom URL, neither SPDX nor
OSI-approved** — and it forbids reverse-engineering and altering notices. **The open edition is Apache-2.0 with
a NOTICE file, and Apache-2.0 grants modification.** A recipient of an Apache-2.0 artifact would not receive,
for the GSAP portion, the freedoms the surrounding licence advertises. **Not adopted. Motion (MIT) instead.**

**THE QUESTION TO ASK OF EVERY DEPENDENCY, in this order:** may we USE it · may we REDISTRIBUTE it · is its
licence COMPATIBLE with the licence of the artifact we redistribute it inside · does NOTICE need an entry.
**Answering only the first is how a licence conflict ships.**

*(Repo precedent: S6.3 pinned wireguard-go as MIT and recorded Wintun's redistribution terms in NOTICE — the
second and fourth questions, asked at the time.)*

## INVISIBLE IS NOT ABSENT — THIRD INSTANCE, NOW IN RESPONSIVE (2026-08-01, S14.2)

**`display:none` leaves an element FOCUSABLE, ANNOUNCED and SUBMITTABLE.** It is gone to a sighted mouse user
and present to everyone else.

**Three instances of one shape, now across three mechanisms:**

| mechanism | the failure |
|---|---|
| **edition gating by style** | an enterprise control coloured away is still in the DOM — a licence boundary that fails open |
| **responsive hiding** | the **access-rule builder** hidden below `compose` is still keyboard-reachable — **a security surface where a mis-tap grants access**, decided by viewport |
| **nav hiding** | a destination hidden by CSS is a navigation surface that exists for some users and not others, **decided by VIEWPORT rather than by PERMISSION** |

**THE RULE: PERMISSION IS A RENDER DECISION. WIDTH NEVER IS.**

- **Composition below `compose` is ABSENT**, not hidden — `ComposeGate` does not render the editor at all.
- **Nav may RE-ARRANGE, never REMOVE** — every destination is in the DOM at every width; only presentation
  collapses.

**AND THE TEST MUST ASSERT ABSENCE BY ROLE**, which is what makes a `display:none` implementation **fail**
rather than pass: `queryByRole` finds a hidden element, so an assertion written against roles distinguishes
*hidden* from *absent* where a visual check cannot.

### DETECTOR'S FOURTH PROSPECTIVE CATCH — jsdom HAS NO LAYOUT ENGINE

**A "responsive test" written in vitest would assert NOTHING and pass at EVERY width.** jsdom does not evaluate
media queries, compute widths, or lay anything out. **That is not a query-rule-4 violation — it is a check that
CANNOT FAIL**, and it was caught **before the test was written**.

| # | instance | when caught |
|---|---|---|
| 1 | B2's 7-second poller vs a 272 ms window | after twelve green samples |
| 2 | the acceptance test waiting on `issued` | after CI went red |
| 3 | the restore-window poller for an event `restore.go` cannot produce | **before it was written** |
| **4** | **a jsdom "responsive" test at five widths** | **before it was written** |
| **5** | **a `prefers-reduced-motion` test in jsdom** — `window.matchMedia` **is not implemented**, so the test would throw or silently no-op | **before the motion gate was written** |

**The three-layer answer is right precisely because it never asks jsdom a question jsdom cannot answer:** a
**pure** `layoutIntent(width)` unit-tested at boundaries · a component tier that stays **width-blind** (and *a
test that needs a viewport to pass IS the finding*) · a responsive contract asserting **absence by role** with
capability **injected, never measured**.

**CATCH 5 IS THE SAME SHAPE ONE MEDIA QUERY OVER, and it is why the shape is worth naming rather than the
instance.** `prefers-reduced-motion` is a **gate, not a courtesy** — so a test of it that quietly no-ops is a
gate that certifies an accessibility property nobody checked. **Found before the motion gate was written, not
after it silently passed**, and answered the same way: a **pure `motionAllowed(prefersReducedMotion)`**
decision, the preference read **once at the app edge**, and the CSS half emitted **unconditionally** as
`@media (prefers-reduced-motion: reduce)` zeroing every duration token — **so a component that forgets to check
still animates for zero milliseconds.**

**THREE OF THE FIVE WERE CAUGHT BEFORE THE CHECK WAS WRITTEN.** That is the detector paying for itself: the
first two cost a green run each; the last three cost a question.

## A COMMENT THAT ASSERTS A LIBRARY'S BEHAVIOUR IS A GUESS UNTIL A MUTATION CONFIRMS IT (2026-08-01, S14.2)

**THE SESSION'S RESULT. Filed at the top of the [[COULD THIS CHECK HAVE FAILED?]] family because of what it
nearly cost, not because of what it was.**

**THE INSTANCE.** The responsive contract's central assertion — the one the entire compose gate exists for —
was written as:

```tsx
// `queryByRole` finds an element hidden with `display:none`, so a display:none implementation FAILS this.
expect(screen.queryByRole("button", { name: /add rule/i })).toBeNull();
```

**The comment is wrong.** testing-library defaults to `hidden: false` and runs `isInaccessible`, which jsdom
evaluates against inline styles. A `display:none` element is **excluded** from the query — so `queryByRole`
returns `null` and the assertion **passes**. Mutation 1 (reimplement the gate as `display:none`) **PASSED**.

**The assertion checked "not in the ACCESSIBLE TREE"; the comment claimed "not in the DOM".** Those two differ
on *exactly* the failure mode being guarded against. A member of the
[[ASSERTS-A-DIFFERENT-EVENT-THAN-IT-WAITS-ON]] family, and the sharpest so far, because **the false claim was
written INSIDE the comment explaining why the assertion was rigorous.**

## ⚠ WHAT IT NEARLY COST — the near miss is the point, and it is worth stating plainly

**Had `ComposeGate` shipped as `display:none`, the access-rule builder below 768px would have been
KEYBOARD-REACHABLE and SCREEN-READER-ANNOUNCED while invisible — and this test would have CERTIFIED IT
ABSENT.**

A control that grants access, present to a keyboard and gone only to a sighted mouse user, is
[[INVISIBLE IS NOT ABSENT]] — the law this epic had already minted twice. **So the near miss happened inside
the guard written to prevent it.** The guard was not weak; it was *aimed one layer off*, and the comment made
the misaim read as rigour.

**THE RULE. A CLAIM ABOUT A LIBRARY'S SEMANTICS IS A HYPOTHESIS. THE MUTATION IS THE EXPERIMENT.** Where an
assertion's rigour depends on what a matcher *includes* — visibility, disabled state, `aria-hidden`, shadow
roots, portals — **write the mutation the claim says would be caught, and run it.** Prose confidence about a
third-party default is not evidence, and it reads exactly like evidence.

**THE COROLLARY, which is the transferable part: THE MORE CONFIDENT THE COMMENT, THE MORE IT NEEDS THE
MUTATION.** A hedged comment invites scrutiny. A comment that explains why an assertion is rigorous *suppresses*
it — from the author first, then from every reviewer after.

**The fix is a flag, not a rewrite:** `{ hidden: true }` searches the whole DOM regardless of visibility, and
the same mutation then goes red. **The cost of finding this was one mutation. The cost of not finding it was a
security-adjacent gate that passed while not gating.**

## VERIFY THE SCAN, NOT THE BADGE — AND ESPECIALLY ON A SLICE WHOSE OWN FINDING WAS A PASSING NON-ASSERTION (2026-08-01, S14.2)

**A green check is a CLAIM ABOUT a scan. The alert list IS the scan.** When a security check flips from red to
green after a fix, read the finding list at the ref — not the check's colour.

```
gh api "repos/<o>/<r>/code-scanning/alerts?ref=refs/pull/<n>/merge&state=open&tool_name=CodeQL"
```

**THE INSTANCE.** The wireframe rename was verified by that query returning **zero** where it had returned
five, before the aggregate check was believed. Which was lucky in the order it happened: the aggregate passed
through `failure` → **`neutral`** → `pass` as analyses re-ran, and `neutral` reads as "not red" to anyone
skimming. **Two of those three states would have been mis-read as success by colour alone.**

**WHY IT BINDS HERE IN PARTICULAR, and this is the transferable part.** This same slice's finding was
[[A COMMENT THAT ASSERTS A LIBRARY'S BEHAVIOUR IS A GUESS UNTIL A MUTATION CONFIRMS IT]] — **an assertion that
PASSED while asserting nothing.** Trusting a check's colour after finding that would have been **the identical
mistake one layer up**: accepting a summarised verdict in place of the thing it summarises.

**THE RULE. A SESSION THAT HAS JUST FOUND A VACUOUS CHECK MUST ASSUME ITS OTHER CHECKS ARE VACUOUS UNTIL READ.**
The finding is not a one-off to be logged and moved past — **it is evidence about the reliability of every
summarised signal in the same session**, and the cheapest response is to open the underlying data once.

## A PAPER THAT CLAIMS COVERAGE IS AN ASSERTION, AND IT NEEDS A GATE LIKE ANY OTHER (2026-08-01, S14.1 → S14.3)

**THE INSTANCE.** S14.1's commit-one claimed five covered token groups. The emitted artifact carried **thirteen
variables, all colour**. Typography scale, spacing, radius, elevation and motion were **claimed and never
emitted**, and the slice shipped CI-green.

**Its gates were not weak — the promise had NO gate.** Theme completeness compared each theme to the names that
existed. Contrast compared colours to colours. The reservation scan compared source to a rule. **Every gate was
aimed at what was there; none at what was promised.**

**THE RULE. A COVERAGE CLAIM IS DATA, AND A CENSUS COMPARES IT TO THE ARTIFACT.** The claim is hand-authored to
mirror the paper; the artifact is generated; **adding a claimed category without emitting it goes red.**
Derive the claim from the implementation and it compares the system to itself and passes by construction —
[[fixture-restates-production]], applied to a design system.

**THE FAMILY THIS JOINS.** It is the [[A COMMENT THAT ASSERTS A LIBRARY'S BEHAVIOUR IS A GUESS]] shape one level
up: **a comment vouching for absent code, and a paper vouching for an absent property, fail identically** — both
read as evidence, both are unchecked prose, and **both are most convincing exactly where they are wrong.**

**A promise with no gate reads exactly like a promise that is kept**, because everything it is measured against
agrees with it.

## THE RENDER-FLOOR AUDIT MUST READ THE SPEC'S SEMANTICS, NOT JUST CONFIRM AN ENDPOINT EXISTS (2026-08-01, S14.3)

**The "Site-Link Throughput" chart is not merely unbacked. `openapi.yaml` describes the field it would draw as:**

> *"Raw gauge since the last handshake **(display only, never summed as monotonic)**."*

**The endpoint EXISTS. The field EXISTS. The spec FORBIDS the use.** A render-floor audit that asks only *"does
an endpoint supply this?"* returns **yes** and lets the chart through.

**THE RULE. THREE QUESTIONS, NOT ONE:** does the data exist · **does its own description permit this reading** ·
and does the render survive absence (a failed load must show the retry, **never an empty axis**; zero data must
say *"no data"*, **never a flat line at zero**).

**THE UNBACKED CASE IS THE EASY ONE.** Nothing to point at, so the audit catches it. **The hard case is a real
field used in a way its definition rules out** — the audit's own evidence argues *for* the chart. That is why
both known violations in this repo are charts, and why `VizSource` is a REQUIRED PROP: **a chart that does not
name its source does not typecheck**, which moves the question from "did anyone check?" to "does it build?".

## A MISSING PRIMITIVE COUPLES **EVERY** TEST LAYER TO MARKUP — AND LIFTING THE WORKAROUND IN ONE LAYER LEAVES THE OTHERS (2026-08-01, S14.3; widened after shipping)

**Measured: ZERO `<table>` elements in the entire web app. Thirty-seven `.map()` calls rendered `<div>` rows.**

**THE SHARED ROOT: with no `<table>` anywhere, there was NO ROLE TO ASK FOR.** Query rule 1 binds the project
to role + accessible name, and `role="table"` / `row` / `cell` did not exist. So **every** layer that needed to
identify a row invented its own workaround — **independently, and none of them could see the others doing it.**

| layer | the workaround it invented | why it looked fine |
|---|---|---|
| **unit tier** (vitest) | `getByText("old-laptop")` — matching row content as **free text** | passes; reads like a normal query |
| **e2e specs** (Playwright) | `page.locator("main ul > li")`, `locator("li", { hasText })` — **DOM structure** | passes; reads like a normal locator |

**THE PART THAT WAS LEARNED BY SHIPPING.** This law was first minted from the unit tier alone. Slice A
converted three screens, re-pointed the unit tier at roles, ran `make web-gate` **green** — **and CI's `e2e`
job went red**, because the Playwright specs were coupled for the *same* reason and had not been touched.
**Lifting the workaround in one layer left the other, and the one left behind was the one the local gate does
not run.**

### THE DIAGNOSTIC — enumerate, do not read

> **WHEN A PRIMITIVE LANDS, ENUMERATE EVERY CONSUMER ACROSS EVERY TEST TIER BEFORE DECLARING IT LANDED.**

**Reading is not enough, and the reason is precise:**

- **reading one tier** shows queries that **work** — they pass, so nothing draws the eye;
- **reading the components** shows markup that **renders** — it looks correct, because it is;
- **only the ENUMERATION** shows the workaround — because a workaround is only visible as *the gap between what
  a layer asks for and what it could have asked for*, and that gap exists in neither artifact alone.

**A primitive that ships while ANY consumer keeps the workaround has only half landed** — and the half left
behind is invisible from both of the places you would naturally look.

## ⑦ THE SEVENTH VACUITY MECHANISM — **AN UNCHECKED CLAIM** (minted 2026-08-01, S14.1 → S14.3)

> ## **A PROMISE WITH NO GATE READS EXACTLY LIKE A PROMISE THAT IS KEPT, BECAUSE EVERYTHING IT IS MEASURED AGAINST AGREES WITH IT.**

**THE OTHER SIX ASK WHETHER A CHECK COULD FAIL. THIS ONE ASKS WHETHER A CLAIM IS CHECKED AT ALL** — and that
is why none of the other six can see it. Every existing member starts from a check and interrogates it. **Here
there is no check to interrogate.** The claim lives in a paper, a README, a coverage table, an interface
comment; the gates that exist are all sound; and the claim is simply **outside the set of things anything
compares.**

**THE INSTANCE.** S14.1's commit-one claimed five covered token groups — colour, typography, spacing,
radius/elevation, motion. The artifact carried **thirteen variables, all colour**. The slice shipped CI-green
with three gates, **all of them correct**:

| gate | what it compared | why it could not see the defect |
|---|---|---|
| theme completeness | each theme → `TOKEN_NAMES` | **compares themes to the names that EXIST** |
| contrast floor | colour pairs → WCAG ratios | **compares colours to colours** |
| `ok` reservation scan | source use-sites → a rule | **compares source to a rule** |

**Every gate internally consistent. Every gate aimed at what was there. None at what was promised.**

### THE DIAGNOSTIC — the question to ask of a paper, and it is one line

> **For every claim a paper makes ABOUT AN ARTIFACT, name the check that would FAIL if the claim became false.**
> **If there is none, the claim is prose.**

Prose is not worthless — but it must be **read as prose**, and a coverage table read as a guarantee is the
whole failure. **The tell is that nothing has to change for the claim to become false**: a promise nothing
measures cannot be broken, only discovered.

### THE FIX'S LOAD-BEARING PROPERTY: **`CLAIMED_COVERAGE` IS HAND-AUTHORED, NOT DERIVED**

**This is the part that does the work, and it looks like duplication.** `CLAIMED_COVERAGE` mirrors the paper by
hand. Deriving it from the scales it describes would make the census **compare the token set to itself** — it
would pass for every possible token set, including the thirteen-colour one. **That is exactly how the original
claim survived: everything that looked at it was downstream of it.**

**SAME FAMILY AS TWO EXISTING RULINGS, and the family is worth naming:** the **mirror census as a deliberate
literal** and the **D10 golden vector**. In all three, **an INDEPENDENT restatement is the mechanism** — and in
all three the instinct to "just derive it, so it can't drift" would destroy the only property that matters.
**A check must be able to DISAGREE with the thing it checks. Derivation removes that ability while looking
like rigour.**

## THE `?raw` ESCAPE WAS LUCK, NOT DESIGN — AND RECORDING IT AS DESIGN WOULD TEACH THE WRONG LESSON (2026-08-01, S14.3)

**`import css from "…/tokens.css?raw"` returns an EMPTY STRING under vitest** — CSS processing is off by
default and the raw query is swallowed with it. The coverage census read `""` and went red.

**IT WENT RED ONLY BECAUSE EVERY COVERAGE ASSERTION IS A LOWER BOUND** (`0 >= 13` is false). **Had one been an
"and nothing unexpected" check — a set difference, a "no extra variables", an exact-match — an empty string
would have satisfied it PERFECTLY**, and the census would have certified an artifact it never read.

**THAT WAS LUCK.** The assertions were written as lower bounds because lower bounds fit the question, not
because anyone had considered an empty artifact. **The guard that asserts the artifact is non-trivial is what
converts the luck into a property** — after it, the direction of the assertions stops mattering.

**THE RULE, and it is about the write-up as much as the code: A LUCKY ESCAPE RECORDED AS A DESIGNED ONE TEACHES
THE WRONG LESSON.** It says *"our assertions are robust"* when the truth is *"our assertions happened to point
the safe way this time."* The first invites the next author to rely on a property nobody built. **Name which
one it was.**

**GENERALLY: AN EMPTY FIXTURE SATISFIES EVERY UNIVERSAL CLAIM.** `every`, `all`, set-difference, "none of these
appear", exact-match against an empty expectation — **all pass on nothing.** Any check reading an external
artifact needs a **non-triviality assertion on the artifact itself**, and it belongs beside the check, not in
the author's head.

## NAME A GATE WITH ITS COMPOSITION, ALWAYS — A PHRASE MUST NOT CARRY MORE WEIGHT THAN ITS TARGET (2026-08-01, founder-ruled)

> ## **`make web-gate` (typecheck + vitest + build — NOT Playwright; e2e runs in CI only)**

**That parenthetical is MANDATORY wherever the target is named**, in papers, commit messages and reports, and
it was **applied retroactively** to the S14.1 / S14.2 / S14.3 papers rather than only going forward.

**THE INSTANCE.** Slice A was reported as *"`make web-gate` GREEN"* — true — and was **broken**, because
`e2e` is not in it. Three Playwright specs selected rows by DOM structure and died the moment the lists became
tables. **The claim was accurate and the sentence was not**, because "the gate" sounds total and the target is
partial.

**THIS PROJECT HAS A DOCUMENTED HISTORY OF EXACTLY THIS CLASS, and the members should be read together:**

| phrase | what it sounds like | what it actually is |
|---|---|---|
| *"CI is green"* | everything ran and passed | **[[CI GREEN BY ABSENCE]]** — a job that never fired reports nothing, and nothing looks like success |
| *"`generate-check` guards the artifacts"* | tampering is detected | **it protects the SOURCE↔ARTIFACT RELATIONSHIP** — a hand-edit is regenerated away and the check goes green |
| *"`mutate.sh` printed `Restoring`"* | the file is back | **restoration is from the BACKUP**, and only the target file — a generated artifact stays mutated |
| *"`make web-gate` passed"* | the web surface is gated | **typecheck + vitest + build. NOT Playwright.** |

**THE RULE. A GATE'S NAME IS A CLAIM ABOUT COVERAGE, AND AN UNQUALIFIED NAME OVERSTATES IT** — not through
dishonesty but through **compression**: the short form survives into summaries, handoffs and re-entry, while
the composition stays behind in the Makefile. **The parenthetical travels; the Makefile does not.**

**AND THE COST IS ASYMMETRIC.** Overstating coverage produces confident wrong decisions — *"the gate passed,
ship it"*. Stating it precisely costs eight words.

## AN OFF-BY-ONE THAT RESEMBLES A PLAUSIBLE PRODUCT DEFECT COSTS MORE TO DIAGNOSE THAN ONE THAT LOOKS ABSURD (2026-08-01, S14.3)

**THE INSTANCE.** Re-pointing `audit.spec.ts` from `main ul > li` to `getByRole("row")` changed what "a row"
means: **`role="row"` INCLUDES THE HEADER.** Both counts needed `+1` (50 → 51, 53 → 54).

**The second one was nearly missed — and it would have failed IN THE SAFE-LOOKING DIRECTION.** That spec
asserts keyset paging stitches 53 events with **no overlap and no gap**. A count of 53-where-54-is-expected
does not read as *"the test counts the header now"*. **It reads as a re-served or dropped row — a paging bug**,
which is a real defect this suite exists to catch, in a subsystem where such bugs genuinely occur.

**THE RULE. THE DANGEROUS FAILURE IS THE ONE THAT LOOKS LIKE A BUG YOU BELIEVE IN.** An absurd red (`expected
54, got 0`) is diagnosed in seconds. A **plausible** red sends someone into the paging logic — the code that is
correct — and the longer they look, the more likely they are to "fix" it there.

**SIBLING OF THE MEASUREMENT-ERROR LAW: a plausible finding is more dangerous than nonsense.** Same shape, one
domain over: **nonsense is self-announcing; plausibility is camouflage.** So when a query's *semantics* change
under a refactor — what counts as a row, a cell, a match — **state the new convention IN the assertion**, so
the next red is read as a convention question rather than a product one.

# ⑧ THE SUBJECT AND ITS CHECK VANISHING TOGETHER (minted 2026-08-01, EPIC 14 merge)

> ## **THE OTHER SEVEN MECHANISMS ALL ASSUME THE SUBJECT IS PRESENT. THIS ONE IS THE SUBJECT AND ITS GUARD DISAPPEARING IN THE SAME MOVE, SO NOTHING REMAINS TO DISAGREE.**

**FILED ABOVE THE REST OF ITS FAMILY. It is the sharpest instance this project has produced.**

**THE INSTANCE.** Rebuilding `story/S14.2-layout-shell` on the new `main` meant cherry-picking its own commits.
The sequence **reported success**. **The commit count agreed.** A repo-wide conflict-marker scan was **clean**.
Every exit code was **0**.

**And 400+ lines of S14.2's product code were missing** — `ComposeGate.tsx`, `layout.ts`, the grouped `AppShell`
nav, the `Access`/`main` wiring. They had originally landed inside `1843df9`, a **merge commit that also
carried uncommitted working-tree files**; replaying only non-merge commits dropped the payload.

## WHY THIS IS NOT MERELY A VACUOUS CHECK

**Had it shipped, CI WOULD LIKELY HAVE PASSED** — because `responsivecontract.test.tsx` and `layout.test.ts`
were dropped **in the same payload**. The feature and its evidence were one commit.

**A vacuous check is a guard pointed at the wrong thing while the subject is still there to be examined.**
Every one of the other seven has that structure — a poller that samples too slowly, an assertion that waits on
a different event, a claim nothing compares, a fixture restating production. **In all of them the subject
exists and something could, in principle, notice.**

**Here there was nothing left to notice with.** The suite would have gone green over a smaller universe and
reported the same word. **Green is a statement about what ran, and a shrinking denominator is invisible in it.**

## THE DIAGNOSTIC THAT CAUGHT IT

> ### **COMPARE THE RESULT TO THE INTENT — NEVER THE PROCESS TO ITS EXIT CODE.**

`git diff --stat backup/S14.2-prerebase HEAD`. **That is what found it.** Commit counts, exit codes and marker
scans **all agreed with each other and all described the process**, not the outcome. Only an artifact-to-artifact
comparison against a known-good reference could see a hole, because **a hole has no representation in any
process signal.**

## PROCEDURAL, NOT ADVISORY

> **ANY REBASE OR REBUILD OF A BRANCH WHOSE CI IS ALREADY GREEN MUST END WITH A TREE DIFF AGAINST THE
> CI-VERIFIED TIP, ASSERTED EMPTY. TAKE THE BACKUP REF BEFORE STARTING. ALWAYS.**

```bash
git branch -f backup/<branch>-prerebase <branch>     # BEFORE anything
…                                                     # rebase / rebuild
git diff --stat backup/<branch>-prerebase HEAD        # MUST be empty
```

**The backup ref costs nothing and is the only thing that makes the assertion possible.** Without it there is
no known-good reference, and the rebuild can only be compared to itself.

## A COMMAND THAT STOPS FOR INPUT IS NOT A COMMAND THAT FAILED — AND `&&` CANNOT TELL THEM APART

**`git rebase` EXITS 0 WHEN IT STOPS ON A CONFLICT.** So `git rebase … && git push --force-with-lease …`
**pushed a branch with 1 of 6 commits replayed.**

**RECORDED AS LUCK, NOT DESIGN:** the truncated state was never consumed. Nothing read it, `main` was
untouched, and the completed rebase restored all six. **That is an outcome, not a safeguard**, and a lucky
escape written up as a designed one teaches the wrong lesson.

**REBASES RUN UNCHAINED FROM THEIR PUSH. PERMANENTLY.**

**AND NOTE THE PAIR, ONE SESSION APART AND ONE LAYER APART:**

| command | exits `0` while… |
|---|---|
| `git rebase` | **incomplete** — stopped mid-sequence awaiting a human |
| `git cherry-pick <list>` | **dropping a payload** — a merge commit's content silently skipped |

**Both are "success" by exit code. Both are only visible by comparing the RESULT to the INTENT.**

# THE FAST-FORWARD PUSH IS THE STANDARD MERGE ROUTE, AND THE REASON IS A PROPERTY WORTH KEEPING (2026-08-01)

**GitHub REFUSES the merge-commit route under `required_linear_history`:**

```
GraphQL: Merge commits are not allowed on this repository. (mergePullRequest)
```

**`gh pr merge --rebase` would work — and it REWRITES the shas**, server-side, producing commits **CI has never
seen**. The recorded green sha and the merged sha would then be different objects, and every *"CI green at
`X`"* line in every paper would refer to something that is not what landed.

**A TRUE FAST-FORWARD PUSH PRESERVES THE EXACT SHAS CI VERIFIED:**

```bash
git push origin origin/story/<branch>:main     # ff only; refused if not a descendant
```

**THE PROPERTY: the sha in the paper, the sha CI checked, and the sha on `main` are ONE OBJECT.** That is what
makes `GATE-REPORT-NEEDS-SHA` mean anything after the merge rather than only before it — and it is what let
this session detect that #44's recorded green had gone stale, because the recorded sha was still findable.

**It also cannot silently succeed on the wrong thing:** a non-descendant is **refused by git**, which is how
#45 and #46 were caught needing a rebase rather than being quietly rewritten onto the new base. **GitHub closes
the PR as MERGED on its own once the head sha becomes an ancestor of `main`.**

## A COMMENT THAT **BECAME CODE** — THE COMMENT-VOUCHING FAMILY, INVERTED (2026-08-01, S14.3 slice B)

**THE INSTANCE.** A `@ts-expect-error` directive was removed because it was unused (`TS2578`). The removal was
explained in a `//` comment that **named the directive**. **`tsc` reported `TS2578` again — on the comment.**

**TypeScript reads the TOKEN, not the sentence around it.** Writing the literal text of a suppression directive
in a line comment **creates a suppression directive**, regardless of the prose wrapped around it.

### WHY THIS IS A NEW MEMBER RATHER THAN ANOTHER INSTANCE — THE MECHANISM IS INVERTED

| | the usual shape | **this one** |
|---|---|---|
| the comment | makes a **FALSE CLAIM** about code | **BECAME code** |
| the claim | asserted by a human, unchecked | **REAL, and asserted by nobody** |
| discovered by | a mutation, or an incident | **the compiler, immediately** |

[[A COMMENT THAT ASSERTS A LIBRARY'S BEHAVIOUR IS A GUESS UNTIL A MUTATION CONFIRMS IT]] and
[[⑦ THE SEVENTH VACUITY MECHANISM]] both describe **prose that says something untrue about the system**. Here
the prose **entered the system** and said something **true that nobody meant to say**. It is the same seam —
the boundary between commentary and code — **crossed in the opposite direction.**

### THE DURABLE HALF, WHICH IS ABOUT SUPPRESSIONS GENERALLY

> ## **A STALE SUPPRESSION IS A STANDING ASSERTION THAT A TYPE ERROR EXISTS — AND IT GOES STALE IN SILENCE.**

`@ts-expect-error` claims *"the next line does not compile."* **When the underlying error is fixed, NOTHING
FAILS** in most configurations — the directive simply outlives its reason, **indefinitely**, and the next
reader takes it as evidence of a problem that no longer exists. TypeScript's `TS2578` is unusually good
behaviour here precisely *because* it makes the stale case loud; **most suppression mechanisms
(`eslint-disable`, `//nolint`, `# type: ignore`) do not.**

### THE PRACTICAL RULE

> **NEVER WRITE A DIRECTIVE'S LITERAL TEXT IN PROSE. REFER TO IT BY DESCRIPTION.**

*"the suppression directive that used to sit here"* — not the token. **A block comment is not a fix; it is a
workaround for one language's lexer.** The rule is general because the failure is: **any tool that scans
comments for magic tokens cannot distinguish an instruction from a discussion of that instruction.**

## THE GATE IS **THREE LEGS**, AND EACH ANSWERS A QUESTION THE OTHERS STRUCTURALLY CANNOT (2026-08-01, S14.3)

**`make web-gate` = `typecheck` + `vitest` + `build`. NOT Playwright; e2e runs in CI only.**

**THE `typecheck` LEG IS NOT REDUNDANT WITH THE `vitest` LEG, and this story produced THREE proofs in a row:**

| # | what `tsc` caught | what `vitest` said |
|---|---|---|
| 1 | `TS6133` — an unused import in the **primitive census** | **13 tests green**, watched pass |
| 2 | `TS2578` — the comment that **became a directive** | **347 tests green** |
| 3 | `TS6133` — an unused `vi` import | **347 tests green** |

**THE REASON IS STRUCTURAL, not a configuration accident: VITEST TRANSPILES PER FILE AND NEVER TYPECHECKS.**
esbuild strips types without checking them, so a test file can be **type-incoherent and behaviourally correct
at the same time** — and the suite reports the second while saying nothing about the first.

**AND THE CONVERSE HOLDS:** `tsc` cannot tell whether a correct-typed assertion asserts anything at all — that
is what the mutation proofs are for. **Three legs, three questions:**

- **`typecheck`** — *is it coherent?*
- **`vitest`** — *does it behave?*
- **`build`** — *does it assemble?* (and `e2e`, **in CI only** — *does it work end to end?*)

> **NAMING A COMPOSITE GATE AS ONE THING IS WHAT LET *"the gate is green"* MEAN LESS THAN IT SOUNDED.**

**Filed beside the `NOT Playwright` rule because it is the same failure one level down:** the first said the
gate omits a leg people assume is there; **this says the legs it DOES have are not interchangeable, so "some of
it passed" is not "it passed."**

## A CORRECTNESS IMPROVEMENT CAN BREAK A TEST THAT DEPENDED ON THE DEFECT (2026-08-01, S14.3 slice C)

**THE INSTANCE.** `Donut`'s `<svg>` gained an accessible `<title>` — a strict improvement, since a graphic with
no name is unannounced. **`getByLabelText("Gateway liveness")` immediately failed: *"Found multiple elements."***

**The test had been passing BECAUSE THE ACCESSIBLE NAMING WAS ABSENT.** One element carried that name only
because the other one did not carry it yet. **The query was never specific — it was unique by accident**, and
the accident was the missing accessibility.

**THE RULE.** When adding semantics breaks a query:

> ## **THE BREAK IS A SIGNAL THE QUERY WAS WEAK — NOT THAT THE IMPROVEMENT WAS WRONG.**

The reflex is to narrow the *fix* (add a `data-testid`, scope to a container, take `[0]`). **All three preserve
the weak query and discard the signal.** The right move is the one the rules already require: **query by ROLE +
accessible name** (`getByRole("figure", { name })`), which is unambiguous precisely *because* it engages the
semantics that just improved.

**WHY THIS MATTERS FOR THE WHOLE EPIC, and it is the reason this is filed rather than fixed and forgotten:**
**S14.4+ adds semantics to THIRTEEN more screens.** Every one of these breaks will look like a regression and
be **evidence of a pre-existing weak assertion**. **Expect them, welcome them, and fix the QUERY.**

**A test that breaks when the product gets more correct was testing the wrong thing** — and it was green the
entire time it was wrong, which is why nothing found it earlier.

## DORMANT MACHINERY IN OUR OWN NEW CODE, ONE SLICE AFTER MINTING THE LAW (2026-08-01, S14.2 → S14.4)

**S14.2 shipped `LayoutCapability.columns` — a 1/2/3/4 budget derived from viewport width, unit-tested,
mutation-proven, and published to the DOM as `data-columns`.**

**NOTHING CONSUMED IT.** Every screen stayed inside `max-w-3xl` — a 768px cap — so there was never any width
for a second column. **The capability was computed, asserted, and ignored.**

**THE UNCOMFORTABLE PART: this epic had already ruled on dormant machinery** (S8.4's round-3 HALT ripped out a
dormant resolver rider), **re-stated it for the viz primitives**, and **wrote it into the EPIC 14 paper as an
enforceable obligation** — and then produced a fresh instance two slices later, in code written by the same
hand that wrote the rule.

**WHY THE TESTS DID NOT CATCH IT, and this is the transferable part: the tests asserted the DECISION, and the
decision was correct.** `capabilityFor("wide").columns === 4` is true and always was. **No assertion asked
whether anything READ it.** A value can be perfectly computed and perfectly tested while being perfectly
inert.

> ## **A PRODUCER-WITHOUT-A-CONSUMER PASSES EVERY TEST OF THE PRODUCER.**

**The repo already has the standing probe for exactly this** — *"for every new channel field, name its consumer
and cite the reading line"* — minted after three producer-without-consumer instances in one epic.
**IT WAS NOT APPLIED TO UI STATE, only to the agent channel.** It applies to any value crossing any seam:
**name the consumer, cite the line, or do not ship the producer.**

## AN EDITION-GATED CAPABILITY IS A FOURTH STATE, AND FOLDING IT INTO `failed` SHOWS AN ERROR FOR A FEATURE NEVER SOLD (2026-08-01, S14.4)

**THE INSTANCE.** The Overview's *Pending approvals* card reads **`— unavailable`** in red on the **open**
edition. `/devices/pending` is enterprise-only, so it answers `403 edition_required`, `loadOne` reports a
failure, and the card renders the failure treatment.

**S14.4 carefully separated three states — `loading` / `failed` / `ok` — and then folded a FOURTH into
`failed`:**

> ### **THIS CAPABILITY DOES NOT EXIST FOR YOUR EDITION.**

**That is not a failure to learn something. It is a correct, successful answer** — and the product renders it
as breakage to the exact users who were never sold the feature.

**EPIC 14 ALREADY RULED THIS: edition gating is a RENDER decision — the surface is ABSENT, not styled away,
not degraded, and NEVER AN ERROR.** It routes through the one gating seam. The rule existed; the fourth state
was simply not noticed while building the other three.

**THE GENERAL SHAPE:** when a design carefully enumerates states, **the danger moves to the state that was not
enumerated** — and it will be absorbed by whichever existing state is nearest, which is almost never the
harmless one. **`403 edition_required` is nearest to "error" in shape and furthest from it in meaning.**

**THE CHECK: for every load, ask what a SUCCESSFUL REFUSAL looks like** — 403 by edition, 403 by permission,
404 by scope. **Each is a real answer. None of them is a failure.**

## A GATE SUITE CAN BE COMPLETE, GREEN, AND BLIND TO THE ONLY QUESTION THAT MATTERED (2026-08-01, EPIC 14, founder-ruled)

**THE INSTANCE.** Four slices of a UI redesign shipped with **388 passing tests, green CI, mutation proofs that
found three real defects, a contrast gate, a coverage census, and a drift guard** — and **did not look like the
design.**

**Every gate asked a question and answered it correctly.** *Is this correct? Is it honest? Could this check
have failed? Does the claim have a check?* **All yes.** Not one of them could ask:

> ## **DOES THIS LOOK LIKE THE THING WE ARE TRYING TO BUILD?**

**THE RULE. WHEN A DELIVERABLE HAS A PROPERTY NO AUTOMATED CHECK CAN EVALUATE, THE HUMAN REVIEW IS NOT A
COURTESY — IT IS THE GATE FOR THAT PROPERTY, AND IT IS REQUIRED.** Naming it as optional is how it gets skipped
under time pressure, and the skip is invisible because everything else is green.

**AND THE SHARPER HALF: A COMPREHENSIVE GREEN SUITE MAKES THIS FAILURE MORE LIKELY, NOT LESS.** The more gates
that pass, the more confident the report, and the more the unmeasured dimension looks like it must have been
covered by *something*. **Rigour on the measurable dimensions is not evidence about the unmeasurable ones — but
it reads exactly like it.**

**RELATED, AND THE ROOT CAUSE HERE: A SESSION-SCOPED INSTRUCTION THAT IS NEVER LIFTED BECOMES A PERMANENT ONE.**
The prohibition on reading the design file was correct for the session it was written in and wrong for every
session after. **Nothing expires an instruction; it has to be revisited.** When a later ruling *implies* an
earlier constraint should lift, **say so and ask** — a contradiction between two instructions is a fork, and
forks halt and surface.

# ⭐ AN ABSENCE FOUND BY ONE ENCODING IS NOT AN ABSENCE (2026-08-01, EPIC 14 — founder-ranked the best finding of this stretch)

**THE INSTANCE.** A design file was scanned for colours with `#[0-9a-fA-F]{6}` and the report read: **"there is
no violet anywhere."** The design's accent is `#7C5CFC`, and the founder was about to rule a correction —
re-pointing the entire token set — on the strength of that sentence.

**Two things were true and neither was what the report said:** the prototype ships **two palettes** with
**mono as the default**, so the rendered file genuinely contains no violet; and the accent, where it *is* used,
appears as **`rgba(124,92,252,…)`** — an **rgb() form a hex scan cannot match.**

**THE REPORT WAS ACCURATE ABOUT THE FILE AND WRONG AS A CLAIM ABOUT THE DESIGN.** The gap between those two is
where the damage was.

**THE RULE. AN ABSENCE FOUND BY ONE ENCODING IS NOT AN ABSENCE.** Colours are `#rgb`, `#rrggbb`, `rgb()`,
`rgba()`, `hsl()`, and named. Paths are absolute, relative, and symlinked. Versions are `v1.2.3` and `1.2.3`.
**Before reporting "X is not present", enumerate the ways X could be spelled and search for each — or report
"not found as <encoding>", which is a different and honest claim.**

**AND THE SHARPER HALF, because it is what nearly caused the damage: A NEGATIVE FINDING DRIVES BIGGER
DECISIONS THAN A POSITIVE ONE.** *"The accent is X"* invites a look. *"There is no accent"* invites a rewrite.
**Absence claims should therefore carry MORE evidence than presence claims, and habitually carry less** —
because there is nothing to point at, so there is nothing to check.

**WHAT CAUGHT IT: a second, independent artifact** — the designer's README — **saying something different.**
Not a better search. **When a measurement drives a large decision, seek a source that could DISAGREE with it**;
re-running the same query more carefully cannot.

**SECOND TIME THIS SESSION A CLAIM OF MINE WAS REFUTED BY MEASUREMENT RATHER THAN ARGUMENT** — the first was
the cherry-pick that reported success while dropping 400 lines, caught by a tree diff. **Founder-recorded as
the process working, not a setback.** Both were caught the same way: **an artifact that could disagree**, not a
more careful re-reading of the one already trusted.

## EAGER OR LAZY IS DECIDED BY WHETHER THE ABSENCE IS VISIBLE MID-RENDER — NOT BY PRECEDENT (2026-08-01, EPIC 14)

**TWO ASSET DECISIONS, ONE WEEK APART, SAME QUESTION, OPPOSITE ANSWERS:**

| asset | ruling | why |
|---|---|---|
| **Motion** (animation) | **LAZY, never critical path** | **a missing animation is INVISIBLE.** Nothing renders wrong; the page is simply still. |
| **Lucide icons** (nav) | **EAGER, must be in the initial bundle** | **a nav that renders iconless and then REFLOWS is worse than one that never had them.** The absence is visible, and then the arrival moves the page under the reader. |

> ## **THE DISCRIMINATOR: IS THE ABSENCE VISIBLE MID-RENDER?**
> **If yes, ship it eagerly. If no, defer it.**

**STATED AS A RULE SO THE NEXT ASSET IS NOT DECIDED BY PRECEDENT.** *"We lazy-loaded the last one"* is not an
argument — **the two rulings above would be inconsistent under any precedent-based reading, and they are both
correct.** Ask the question, not the history.

## A VERIFIED FACT AND A CORRECTLY TRANSCRIBED FACT ARE TWO DIFFERENT CLAIMS (2026-08-01)

**THE INSTANCE.** A Makefile override was verified with `make -n seed NET=tunnex-s141_default` — **correct, and
the output proved it.** The instruction was then written as `NET=tunnex-s141_default make seed`.

**Those are not the same command.** `NET=x make …` sets an **environment variable**, and a Makefile's
`NET := …` **overrides the environment**. `make … NET=x` is a **command-line variable**, which **overrides the
Makefile**. Opposite precedence, decided purely by which side of `make` the assignment sits on — and the wrong
form fails **silently**, using the default.

**THE VERIFICATION WAS SOUND. THE HANDOFF WAS NOT.** The check happened; its result was then restated in a form
the check never covered. **Nothing about "I verified it" is false, and the instruction was still wrong.**

**THE RULE. VERIFY THE ARTIFACT YOU ARE ABOUT TO HAND OVER, NOT THE ONE YOU HAPPENED TO RUN.** Where a command
is being given to someone else, **paste the exact string that was executed** — do not retype it, do not
normalise it, do not move a flag for readability. **The gap between "what I ran" and "what I wrote" is
invisible to every gate**, because the gate only ever saw the first one.

**SIBLING OF `APPLY-THE-DETECTOR-TO-THE-MEASUREMENT`:** there, the measurement needed checking as much as the
thing measured. **Here, the TRANSCRIPTION needs checking as much as the measurement** — it is one more link in
the chain, and it is the only link no tool observes.

## ⛔ VERIFYING IS NOT DELIVERING — AND THIS SESSION PRODUCED THREE INSTANCES (2026-08-01)

**THE THIRD AND WORST INSTANCE.** The S14.4 redesign was built, `make web-gate` ran green at **401 tests**, the
drift guard passed — **and the eight commits were never pushed.** The founder was told to review it on
localhost, pulled, and got *"Already up to date"* — **truthfully**. Docker reported
`CACHED [web build 10/10]` — **correctly**, because nothing in their clone had changed.

> ## **EVERY SIGNAL IN THE CHAIN WAS HONEST. THE ONLY FALSE STATEMENT WAS "GO AND LOOK AT IT."**

### THE FAMILY, IN ONE SESSION

| # | what was VERIFIED | what was DELIVERED | caught by |
|---|---|---|---|
| 1 | `git rebase` exited 0 | a branch with **1 of 6** commits replayed | a later tree diff |
| 2 | `make -n seed NET=…` proved the override works | the instruction written in the form that **silently ignores it** | the founder's failed run |
| 3 | **401 tests green locally** | **nothing — the commits never left the machine** | **the founder losing twenty minutes** |

**THE PROGRESSION IS THE POINT: each was caught later and cost more, and each time the verification itself was
sound.** A green gate says something true about a working tree. **It says NOTHING about whether that working
tree is reachable by anyone else.**

### THE RULE

> **BEFORE TELLING ANYONE TO LOOK AT SOMETHING, VERIFY IT FROM WHERE THEY WILL LOOK.**

Not "did it build" — **"is it where they will fetch it from"**:

```bash
git fetch origin && git rev-parse HEAD && git rev-parse origin/<branch>   # must be equal
```

**A HANDOFF IS AN ACT, NOT A CONSEQUENCE.** Building, testing, committing and pushing are four separate
things, and only the fourth makes the other three visible. **The gate cannot notice the missing one, because
the gate runs on the side where the work already is.**

## EXTENDING A FRAMEWORK'S SCALE WITH ITS OWN KEY NAMES REDEFINES EVERY EXISTING USE (2026-08-01, S14.4)

**THE INSTANCE.** The design specifies spacing in **px** — 4 · 6 · 7 · 8 · 9 · 10 · 12 · 14 · 16 · 20 · 24 —
so the token set was emitted with those numbers as keys and fed to `theme.extend.spacing`.

**Tailwind's scale is keyed in QUARTER-REMS: `4` means `1rem` (16px), `24` means `6rem` (96px).** The extension
did not ADD a scale. **It redefined the existing one**, across **128 use sites in 17 screens**:

| class | was | became |
|---|---|---|
| `p-4` | 16px | **4px** |
| `gap-12` | 48px | **12px** |
| `h-24` | 96px | **24px** |

**NOTHING FAILED.** Every class still resolved, every page still rendered, the type-checker was silent, and
**415 tests stayed green** — because no test asserts a computed size, and jsdom has no layout engine to assert
one with.

### HOW IT SURFACED — and the symptom pointed away from the cause

**A donut "was wired" and did not appear.** It was rendering at `h-24 w-24` = **24×24px instead of 96×96** —
present, correct, and a quarter the size of its own legend text. **The reported fact ("wired") and the observed
fact ("no donut") were both true**, and the gap between them was a unit.

**THE RULE. WHEN EXTENDING A DESIGN-SYSTEM SCALE, CHECK WHETHER THE KEYS COLLIDE WITH THE FRAMEWORK'S OWN.**
If they do, one of three things must happen: **namespace the new scale**, **express the design in the
framework's existing keys** (12px = `3`, 16px = `4`, 24px = `6`), or **migrate every existing use in the same
change**. Silently redefining is the only option that looks like it worked.

**AND THE DEEPER POINT: A UNIT CHANGE IS INVISIBLE TO EVERY GATE WE HAVE.** Types check names, not magnitudes.
Tests assert decisions, not pixels. The drift guard compares artifacts, not meanings.

> ## ⭐ THE STRONGEST ARGUMENT YET FOR THE HUMAN GATE, AND IT IS WORTH STATING AS ONE.
>
> **128 use sites across 17 screens silently changed magnitude. A donut rendered at a QUARTER SIZE. And:**
>
> | gate | verdict it gave |
> |---|---|
> | `tsc --noEmit` | **clean** — every class name still valid |
> | 415 vitest assertions | **green** — every decision still correct |
> | `make generate-check` | **clean** — every artifact matched its source |
> | contrast gate, coverage census, `ok`-reservation scan | **green** |
> | CI, e2e included | **green** |
>
> **Every instrument we own reported success, and the page was wrong.** The defect was found by a founder
> looking at a screenshot and saying *"the donuts are missing."*

**THIS IS NOT AN ARGUMENT FOR FEWER GATES — it is an argument about what gates are FOR.** Ours answer *is this
correct, honest, and non-vacuous?* **None of them can answer *does this look right?*, and no amount of rigour
on the first question produces evidence about the second.** The founder's localhost review is therefore a
**required gate for a dimension nothing else measures** (see the SECTION PROTOCOL), and calling it a courtesy
is how it gets skipped under time pressure — invisibly, because everything else is green.

### ⛔ AND THE SHARPER HALF — **THE EYE GATES ONLY WHAT SOMEONE HAPPENS TO LOOK AT**

**All 17 screens were mis-rendered while the override was live. ONE was being reviewed.**

**Overview had coverage because it was the section in flight. The other sixteen had NONE** — and would have
had none until their own section arrived, **possibly weeks later, by which time the cause would be buried
under dozens of unrelated commits.** The bug would then present as *"Sites has always looked a bit off"*, with
no path back to a spacing key changed in a different story.

> ## **A HUMAN GATE IS A SPOTLIGHT, NOT A FLOODLIGHT. IT PROVES THE SCREEN THAT WAS LOOKED AT AND SAYS NOTHING
> ## ABOUT THE REST — AND ITS SILENCE ABOUT THE REST IS INDISTINGUISHABLE FROM APPROVAL.**

**THIS IS THE ARGUMENT FOR THE PLAYWRIGHT VIEWPORT LEG IN ITS STRONGEST FORM.** A screenshot diff across every
screen at every breakpoint is **the only instrument that could have caught this without a human standing in
front of all seventeen.** Not types, not unit tests, not the drift guard — and not the founder, who can only
be in one place.

> ### **REGISTERED AND UNBUILT. TRIGGER ALREADY FIRED (the first screen slice, S14.4). OWED BEFORE THE EPIC
> ### CLOSES.**

**CONFINEMENT, CHECKED:** the override never reached `main` (`f9b2dfd`), so no other branch or session was
affected — the blast radius was one branch and one reviewer's time.

## `backdrop-filter` MAKES AN ANCESTOR THE CONTAINING BLOCK FOR `position: fixed` — AND jsdom CANNOT SEE IT (2026-08-01, S14.4)

**THE INSTANCE.** `Card` gained the design's glass recipe, which includes `backdrop-filter: blur(24px)`.
**Five modals across four screens render inside a `Card`.** Every one of them silently stopped being
viewport-positioned: `position: fixed` resolves against the nearest ancestor with `filter`, `transform`,
`perspective`, `will-change` **or `backdrop-filter`** — so the overlay was clipped to the card, and the card's
own body sat on top of the modal's buttons. **Clicks never landed.**

### HOW EACH LAYER OF THE GATE ANSWERED

| gate | verdict |
|---|---|
| `tsc` | **clean** |
| 422 component tests | **green** |
| a deliberate click-through of all 12 `Card` consumers | **"nothing is broken"** |
| Playwright `e2e` | ⛔ **ONE click timed out**, with a `Card` named as the intercepting element |

**THE CLICK-THROUGH WAS RUN SPECIFICALLY TO CATCH THIS, AND IT COULD NOT.** It rendered every screen and
asserted content — **jsdom has no layout engine, so a containing-block change is invisible there.** The report
was hedged correctly (*"this gates crashes and content loss; it cannot see overlap"*) and was still, in
substance, reassuring about something it had not measured.

> ## **A CORRECT CAVEAT DOES NOT MAKE AN INADEQUATE CHECK ADEQUATE. IT ONLY MAKES THE INADEQUACY HONEST.**

### THE RULE

**AN OVERLAY'S POSITION MUST NEVER DEPEND ON WHERE IN THE TREE IT IS RENDERED.** `Modal` and
`OneTimeSecretModal` now `createPortal(…, document.body)`. That is the correct fix **independently of the
cause** — the containing-block trap is one of several ways a nested overlay breaks, and the portal closes all
of them at once rather than treating this instance.

**AND THE PROPERTY WORTH REMEMBERING: ADDING A VISUAL EFFECT CHANGED A LAYOUT CONTRACT.** `backdrop-filter`
reads as decoration and behaves as positioning. **The same is true of `transform`, `filter`, `perspective` and
`will-change`** — every one of them is reached for as a visual tweak and every one silently re-parents fixed
descendants. **When adding any of them to a SHARED component, enumerate the fixed-position elements that could
end up inside it.**
