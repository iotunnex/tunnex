# Tunnex engineering laws (central registry)

Laws minted across stories, previously scattered in `docs/S*-decisions.md`. New laws land here; existing ones get lifted over time. A law is a rule the review probes for and the build must not regress.

# ⭐ A CORRECT CAVEAT DOES NOT MAKE AN INADEQUATE CHECK ADEQUATE. IT ONLY MAKES THE INADEQUACY HONEST.

**(2026-08-01, S14.4 — the sharpest finding of the day, and it leads this file for that reason.)**

**THE FOUNDER COMMISSIONED A CHECK FOR A SPECIFIC QUESTION:** a shared `Card` primitive had changed, twelve
screens consume it, *"is any of them now visibly broken — overlapping, unreadable, or losing content?"*

**THE CHECK WAS RUN.** Nine screens rendered through their existing wiring tests with content asserted; the
other three got a smoke mount. **It reported "nothing is broken", and it carried an accurate caveat:**

> *"This gates crashes and content loss. It CANNOT see overlap, truncation or unreadable contrast, because
> jsdom has no layout engine."*

**AND `backdrop-filter` HAD ALREADY BROKEN FIVE MODALS ACROSS FOUR SCREENS** — by making `Card` the containing
block for `position: fixed`, so every nested overlay was clipped to the card with the card's own body over its
buttons. **Playwright found it. The commissioned check could not have.**

## THE HEDGE IS NOT THE FAILURE

**The caveat was correct, specific, and written into the test file itself.** It named the exact limitation that
mattered. **Both the author and the founder read the hedged green as reassurance anyway** — because a green
result answers *some* question, and under time pressure the question it answers silently becomes the question
that was asked.

> **THE FAILURE IS TREATING THE CHECK AS AN ANSWER TO THE QUESTION IT WAS COMMISSIONED FOR, WHEN ITS OWN
> CAVEAT SAYS IT IS NOT.**

**SO THE RULE HAS A PROCEDURAL HALF: WHEN A CHECK CANNOT ANSWER THE QUESTION IT WAS ASKED, THE REPORT LEADS
WITH THAT — NOT WITH THE RESULT.** *"I cannot answer this; here is what I could measure"* is a different
message from *"nothing is broken (caveat)"*, and only the first survives being skimmed.


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

# ⑨ ONE-SIDED OBSERVATION (minted 2026-08-02, S14.5 — founder-filed as the family's cleanest form)

> ## **A TEST THAT ONLY EVER OBSERVES ONE VALUE OF A TWO-VALUED THING CANNOT TELL THE VARIABLE FROM THE CONSTANT.**

**IT IS NOT WRONG. IT IS HALF-WRITTEN — which is why it survives review and why it survived a mutation round.**

## The instance

`NodeLink` gained a selection with `aria-pressed={isSel}`. The test rendered a node, asserted
`aria-pressed === "false"`, clicked it, and asserted the handler fired. **Green, and it looked complete.**

**MUTATION: hard-code the attribute to `false`** — deleting the selection state from the announcement
entirely, so no assistive technology could ever learn which node is selected.

**THE TEST PASSED.** `false` is exactly what it expected. It had never once observed `true`, so the constant
and the variable were indistinguishable to it.

## ⛔ THE DIAGNOSTIC

> **FOR EVERY BOOLEAN OR ENUM ASSERTION: DOES THE TEST OBSERVE *BOTH* STATES? IF IT ONLY EVER SEES ONE, IT IS
> ASSERTING A CONSTANT.**

**EXPECT THIS ON THE REMAINING SCREENS.** One-sided observation is the DEFAULT SHAPE when a test is written
from a happy path: you render the common case, assert what it shows, and the other branch is never
instantiated. It takes a deliberate second render to make the assertion mean anything.

## ⚠ AND THE PART THAT MAKES IT WORSE THAN IT LOOKS

**IT CAUGHT ITSELF ONLY BECAUSE THE MUTATION HAPPENED TO TARGET THE CONSTANT SIDE.**

A mutation that changed the **true** branch — `aria-pressed={isSel ? undefined : false}`, or inverting to
`aria-pressed={!isSel}` — **would ALSO have passed**, for the same reason. So the mutation round did not
demonstrate that the technique finds this class. **It demonstrated one lucky hit.** A one-sided test has a
one-sided blind spot, and a single mutation samples one side of it.

**COROLLARY, and the reason this is filed rather than fixed-and-forgotten: MUTATION TESTING INHERITS THE
TEST'S BLIND SPOT.** Mutating a branch that the test never instantiates cannot fail. **"The mutation was
caught" is evidence about the mutation, not about the test's coverage of the other state.**

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

## ⛔ DURING VISUAL ITERATION, REPORT CI STATE ON EVERY PUSH — EVEN WHEN IT IS "STILL RUNNING" (2026-08-01, founder-ruled)

**THE INSTANCE, AND IT IS WORSE THAN THE BUG IT HID.** Four consecutive pushes were reported as
*"`make web-gate` green, IN SYNC"* while **CI had been RED since `1307948` at 16:08Z.** The branch stayed red
for four rounds and would have been merged on the founder's word had the word not arrived with a re-check
attached.

**THE GATE-COMPOSITION RULE WAS MINTED THE SAME MORNING** — *"`make web-gate` = typecheck + vitest + build,
NOT Playwright; e2e runs in CI only"* — **and stopped being applied during the screenshot iteration.**

**WHICH IS PRECISELY WHEN IT MATTERED MOST.** Design changes break exactly what Playwright sees and the local
gate cannot: **nav labels, DOM order, click targets, containing blocks.** Of the four e2e failures, **all four**
were caused by the visual work — renamed nav links, a reordered stat card, and a modal re-parented by
`backdrop-filter`. **The local gate was green for every one of them.**

> ### **A PUSH WITH NO CI LINE IS A PUSH WITH AN UNKNOWN STATE, AND A STRING OF THEM IS HOW A BRANCH STAYS RED
> ### FOR FOUR ROUNDS.**

**THE RULE:**

- **Every push during visual iteration carries a CI line** — `green at <sha>` · `running` · `RED: <job>`.
- **If CI is red, that is the FIRST line of the report**, before anything about what was built. A red branch
  is the most important fact on the page and it must not sit under a description of new panels.
- **"Still running" is a valid and required answer.** The failure mode is silence, not uncertainty.

## THE THIRD TIME ADDING SEMANTICS BROKE A PASSING QUERY — AND IT IS NOT A COINCIDENCE (2026-08-01, EPIC 14)

| # | what was added | what broke |
|---|---|---|
| 1 | real `<table>`/`role="row"` on three screens | unit tier matching row content as **free text**; e2e selecting rows via `main ul > li` |
| 2 | an accessible `<title>` on the donut SVG | `getByLabelText` matching **two** elements |
| 3 | `role="group"` + `aria-label` on the stat card | e2e reading a value via **`xpath=preceding-sibling`** |

> ## **THE TESTS WERE COUPLED TO INCIDENTAL STRUCTURE BECAUSE THE PRODUCT HAD NO SEMANTIC STRUCTURE TO COUPLE
> ## TO.**

**Every one of those queries was the best available at the time it was written.** There was no `role="table"`
to ask for, no accessible name on the graphic, no named group around the stat — **so each test reached for
position, text, or DOM shape, and each was correct until the product acquired the thing it should have been
asking for all along.**

**THE CONSEQUENCE FOR THE TWELVE REMAINING SCREENS: EXPECT THIS EVERY TIME, AND WELCOME IT.** A query that
breaks when the product becomes more semantic **was testing the wrong thing and was green the entire time it
was wrong.** Fix the query, never the semantics — and never by narrowing to a test-id.

## ADDING A VISUAL EFFECT CAN CHANGE A LAYOUT CONTRACT (2026-08-01, S14.4)

**`backdrop-filter`, `transform`, `filter`, `perspective` and `will-change` ALL READ AS DECORATION AND BEHAVE
AS POSITIONING.** Each makes its element the containing block for `position: fixed` descendants.

**BEFORE ADDING ANY OF THEM TO A SHARED COMPONENT, ENUMERATE THE FIXED-POSITION ELEMENTS THAT COULD END UP
INSIDE IT.** For `Card` the answer was **five modals across four screens**, and nothing in the type system, the
component tier, or a deliberate click-through could see it.

**AND THE STRUCTURAL FIX BEATS THE INSTANCE FIX: AN OVERLAY'S POSITION MUST NEVER DEPEND ON WHERE IN THE TREE
IT RENDERS.** `createPortal(…, document.body)` closes the containing-block trap and every other nesting trap at
once, rather than treating the one that happened to be found.

## ⭐ THE STRONGEST FORM OF PROVE-A-GUARD-REJECTS: A GUARD THAT FIRES ON ITS OWN AUTHOR, ON LIVE INPUT (2026-08-01, S14 viewport leg)

**`PROVE-A-GUARD-REJECTS` normally means "break it deliberately and watch it go red".** That is good and it is
the weaker form: **the input is chosen by the person who already knows the answer.**

**THE STRONGER FORM HAPPENED HERE, UNPROMPTED.** `VisualGallery.tsx` was added, and the screen census failed
**by name** in the same session it was written:

```
unaccounted screens (add a wiring+failure test, or a PENDING/EXEMPT entry WITH A REASON): VisualGallery.tsx
```

**Nobody set that up.** The census caught a file its author had not yet thought about — **a real omission, on
live input, from the person who wrote the guard.** A mutation proves a guard *can* fire. **This proves it
fires when nobody is watching for it**, which is the only condition that matters in six months.

**AND THE EXEMPTION IT FORCED IS THE POINT, NOT A FORMALITY.** The census refuses a bare name; it demands a
reason, inline, as data:

> *"test fixture, build-flagged off; gated by the viewport leg and by the unshipped-route assertion"* —
> **a fixture makes no decision, so a wiring test would assert that a fixture equals itself.**

**Without the reason requirement the correct move (exempt) and the lazy move (exempt) are the same keystroke.**
The reason is what makes the two distinguishable to a reader who was not there.

## BASELINES ARE GENERATED WHERE THEY WILL BE COMPARED (2026-08-01, S14 viewport leg)

**THE TEMPTATION IS OBVIOUS: a machine is right there, and `--update-snapshots` runs in seconds.**

**THE PINNED PLAYWRIGHT IMAGE IS `linux/amd64`. EMULATED ON AN arm64 HOST, FONT RASTERISATION DIFFERS** — the
same page, the same browser build, subtly different glyph edges. A baseline rendered on the host is red in CI
on its first comparison.

> ## ⛔ **THE DANGER IS NOT THE MISMATCH. IT IS THE ESCAPE.**
>
> **A red suite nobody can explain leaves exactly one exit: widen the threshold.** And a widened threshold is a
> visual suite that has stopped meaning anything — it now passes the very class of change it was built to
> catch, and reports green while doing so.

**So the rule is about WHERE, not about care:** generate baselines in the same image, on the same architecture,
against the same stack that CI will use. Here that meant **bootstrapping them from a deliberately-failed first
CI run** and committing the artifacts, rather than producing them locally in one command.

**SAME FAMILY AS *"run the command the gate runs, from where the gate runs it."*** Both say: a check's answer
is a property of its ENVIRONMENT as much as its logic, and a result obtained somewhere else is a result about
somewhere else.

## A BUILD-TIME FLAG DELIVERED AT RUNTIME ARRIVES AFTER THE DECISION (2026-08-01, S14 viewport leg)

**THE INSTANCE.** The visual gallery is gated by `import.meta.env.VITE_VISUAL_GALLERY`. The CI job set it in
`.env` — which reaches **compose** and the **running container**, and never reaches the **image build**.

**Vite bakes `import.meta.env` into the bundle at BUILD time.** The route was dead-code-eliminated before the
variable existed. The container then started with the flag set, serving a bundle that had never contained the
route.

**IT FAILED IN THE MOST MISLEADING WAY AVAILABLE:** `toBeVisible` timed out on an element that had never been
compiled in. **Nothing said "the flag did not apply."** The symptom was a missing element, which reads as a
rendering bug, a timing bug, or a bad selector — three wrong places to look.

**THE FIX IS STRUCTURAL: a build-time flag travels as a BUILD ARG**, declared in the Dockerfile and passed
through compose, so the value is present when the decision is made.

> ## **THE RULE: FOR ANY FLAG, ASK *WHEN IS THE DECISION TAKEN?* AND DELIVER IT BEFORE THAT MOMENT.**
> **Build-time, boot-time and request-time flags look identical in a config file and are not interchangeable.**

**AND THE PROCESS POINT THAT CAUGHT IT: THE JOB WAS EXPECTED TO FAIL, AND THE FAILURE WAS STILL READ.** The
first run was *designed* to fail with *"snapshot doesn't exist"* so the baselines could be harvested. **Two of
the five failures were that. Two were this.** Had the log been skimmed for "did it fail? yes, as planned",
the broken gallery specs would have shipped with baselines harvested from a page that never rendered.

**AN EXPECTED FAILURE IS STILL A FAILURE THAT MUST BE READ.** *"It failed as predicted"* is a claim about the
REASON, not the outcome — and the reason is the only part that was predicted.

## `min-width: auto` IS WHY FLEX ROWS OVERFLOW, AND THE SYMPTOM POINTS AT THE WRONG ELEMENT (2026-08-01, S14 viewport leg)

**Flex items default to `min-width: auto`** — they refuse to shrink below their content. A single long string in
a row (an email address, a hostname, a UUID) therefore pushes the row past the viewport, and **the whole PAGE
scrolls sideways**.

**THE INSTANCE, AND THE DIAGNOSTIC ERROR IT PRODUCED.** Overview measured **65px wider than a 390px viewport**.
The first hypothesis was the panel grid — plausible: a `col-span-4` panel at 390px is ~100px and holds a 120px
donut. **The grid was collapsed responsively and the overflow stayed at EXACTLY 65px.**

> **A FIX THAT CHANGES THE NUMBER BY ZERO DID NOT ADDRESS THE CAUSE. THE CONSTANT IS THE EVIDENCE.**

The real source was the **shell header** — an untruncated email in a flex row. It was identifiable in one step
from a fact already in hand: **the gallery passed at 390 and renders OUTSIDE `AppShell`; Overview failed and
renders inside it.** The difference between the passing and failing surface was the shell, not the page.

**THE RULE, PRACTICAL:** any flex row containing user-supplied text needs **`min-w-0` on the shrinking child
and `truncate` on the text**. Absent both, the row's width is set by its longest content forever, and nothing
in the styling says so.

**AND THE DIAGNOSTIC RULE, WHICH IS THE TRANSFERABLE PART: WHEN A FIX LEAVES A MEASURED NUMBER UNCHANGED,
THE HYPOTHESIS IS WRONG — NOT INSUFFICIENT.** The temptation is to add a second fix on top of the first. **The
measurement was already telling us the first fix addressed nothing.**

## FREEZING THE CLOCK FIXES A VARIABLE *NOW*. IT DOES NOTHING ABOUT VARIABLE *DATA*. (2026-08-01, S14 viewport leg)

**THE INSTANCE.** The viewport leg's determinism plan named `relativeAge` — *"3s ago" / "12m ago"* — as the
largest source of false diffs, and prescribed **freezing the browser clock**. That was implemented, and the
Overview snapshot still diverged by **118 pixels** on the next run.

**BECAUSE THE VARIABLE WAS NEVER `Date.now()`. IT WAS `created_at`.** The seed writes its audit rows at SEED
time, which differs every CI run. `frozen_now − varying_created_at` varies, so *"2m ago"* becomes *"5m ago"*
and the image diverges forever.

> ## **A RELATIVE VALUE HAS TWO OPERANDS. PINNING ONE PINS NOTHING.**

**THE FIX AND THE ANTI-FIX, because the wrong one is easier and looks reasonable:**

| | |
|---|---|
| ❌ **`maxDiffPixelRatio: 0.01`** | passes this diff **and every real regression smaller than it**. A threshold is how a visual suite stops meaning anything, and it is the move a red-nobody-can-explain always invites. |
| ✅ **`mask: [page.locator("[data-volatile]")]`** | excludes a **named** region. The snapshot covers LAYOUT; the timestamp VALUE is unit-tested in `relativeAge`. |

**THE DISTINCTION THAT MATTERS: A MASK IS DECLARED IN THE MARKUP AND VISIBLE IN THE IMAGE; A THRESHOLD IS A
NUMBER IN A CONFIG THAT SILENTLY COVERS EVERYTHING.** One says *"this region is not asserted"*; the other says
*"some unspecified amount of anything may change"*. **Both reduce coverage. Only one tells you where.**

**GENERALLY: BEFORE ADDING TOLERANCE, ASK WHAT IS ACTUALLY VARYING AND EXCLUDE THAT.** Tolerance is what gets
reached for when the answer is unknown — and the cost of not knowing is paid by every future regression that
fits under the number.

## MASK WHAT CANNOT BE DETERMINISTIC. **WAIT** FOR WHAT HAS MERELY NOT SETTLED. (2026-08-02, S14 viewport leg)

**TWO VISUAL DIFFS, ONE AFTER THE OTHER, THAT LOOKED IDENTICAL AND NEEDED OPPOSITE FIXES.**

| diff | cause | correct fix |
|---|---|---|
| 118 px, scattered | `relativeAge` over a `created_at` written at SEED time — **varies every run, forever** | **MASK** — it cannot be made deterministic |
| 621 px, one 40px band | `HealthStatus` renders `checking…` then `operational` when `/healthz` answers — **the shot raced the transition** | **WAIT** — the settled state is perfectly deterministic |

**HAD THE SECOND BEEN MASKED — the reflex, since the first one was — A REAL SURFACE WOULD HAVE BEEN EXCLUDED
FROM THE SNAPSHOT PERMANENTLY**, and the control-plane health indicator would never again have been visually
asserted. **The suite would have kept its green and quietly stopped covering a thing it was built to cover.**

> ## **A COMPONENT THAT *CHANGES* IS NOT A COMPONENT THAT IS *VOLATILE*.**

**THE DIAGNOSTIC THAT SEPARATED THEM: LOCALISE THE DIFF BEFORE EXPLAINING IT.** Decoding the diff PNG and
counting changed pixels per row put the entire 621 in `y 921–960` — one band, one component. **A scattered
diff and a banded diff have different causes, and the pixel positions say which** before any hypothesis is
formed. Guessing from "it changed again" would have produced a second mask.

**AND THE COST ASYMMETRY IS WHY THE DEFAULT MUST BE `WAIT`:** an unnecessary wait costs milliseconds; an
unnecessary mask costs a permanently unasserted region that nothing will ever flag.

## ⚠ CORRECTION TO THE ROW ABOVE (2026-08-02) — THE WAIT DID NOT FIX THE 621

**The table claims `WAIT` was the correct fix for the 621 px band. IT WAS NOT, and the entry is left standing
with this correction rather than quietly edited, because the correction is the more useful artifact.**

The wait was added. **The next run diffed by 621 pixels again — the same number.** By the law two sections
down (*when a fix leaves a measured number unchanged, the hypothesis is wrong*), the race was never the
cause. Confirmed by isolating the variable: the app was **byte-identical** between the run the baseline was
harvested from and the run that rejected it. **Only docs and `.png` files moved. So it was run-to-run
variance in rendering, not a transition being caught mid-flight.**

**WHAT SURVIVES OF THE LAW:** the mask-versus-wait *distinction* is sound and the diagnostic (*localise the
diff before explaining it*) is what produced every correct call in this arc. **WHAT DOES NOT SURVIVE:** the
claim that the 621 was diagnosed and fixed. It was diagnosed twice, plausibly, and wrongly both times.

> **A LAW MINTED FROM A FIX THAT WAS NEVER RE-MEASURED IS A HYPOTHESIS WEARING A LAW'S TYPOGRAPHY.**

**The disposition was to remove the subject, not to keep explaining it** — see the law below.

# ⭐ A VISUAL SUITE'S SUBJECT SHOULD BE THE SURFACE WHOSE OUTPUT IS DETERMINED BY CODE, NOT BY DATA

**(2026-08-02, EPIC 14 viewport leg — founder-ruled after seven rounds, and the leg's most durable output.)**

**THE GALLERY RENDERS FIXTURES. A SCREEN RENDERS A LIVE CONTROL PLANE:** panels that resolve in whatever
order the API answers, rows stamped at seed time, health that arrives when `/healthz` arrives. **That is
where the product is interesting and where a pixel diff is LEAST able to say anything.**

Measured, over seven rounds of the same instrument:

| subject | behaviour |
|---|---|
| `gallery-1440` / `gallery-390` (fixtures) | **stable across all 7 rounds** |
| `overview-1440` (live control plane) | **621 px different across runs of IDENTICAL app code** |
| `overview-390` (same code path) | passed twice — **luck, not a property** |

**Keeping the 390 baseline because it happened to pass was considered and REJECTED.** It is the same code
path that flakes at 1440. **Two passes is an absence of evidence of flake, not evidence of determinism** —
the same shape as the absence law near the top of this file.

## ⛔ THE COROLLARY, WHICH IS THE PART THAT ACTUALLY DECIDED IT

**A suite earns its subjects. Count what each instrument has PAID.**

| instrument | pre-existing `main` defects found |
|---|---|
| geometric assertion (`scrollWidth` vs `clientWidth`) | **1** — a 65px header overflow at 390, on every screen since S14.2 |
| a human reading a harvested image | **1** — the drawer `Menu` button sitting on top of the page `<h1>` |
| a strict-mode locator violation | **1** — the control-plane health indicator rendering twice on Overview |
| **the full-page pixel diff of a live screen** | **0, in six rounds** |

> ## **SCOPE THE SUITE TO WHAT HAS PAID, NOT TO WHAT LOOKS COMPREHENSIVE.**

**The honest answer to persistent variance is often a SMALLER SUBJECT, not more determinism work.** Every
round spent chasing the 621 was a round not spent on the screens the instrument had already proven it could
protect — and the three findings above all arrived by other means while the pixel diff was being debugged.

**⚠ AND THE COST OF THE REDUCTION, STATED RATHER THAN GLOSSED:** the `Menu`-over-`<h1>` overlap had been
**committed into the `overview-390` baseline** — frozen, visible, written down. Dropping that baseline means
**the defect is now registered in prose only, and no artifact holds it.** Reducing scope removed real
coverage. That is the correct trade here, and it is not a free one.

# ⭐ A GUARD CAN CONTAIN THE CLASS IT GUARDS AGAINST (2026-08-02, S14.5 — the sweep's headline)

**THE ABSENCE-BY-ONE-ENCODING LAW, APPLIED TO A GUARD RATHER THAN TO A SEARCH.**

`ENTERPRISE_PATHS` exists because the edition-vs-failure defect was fixed at one call site and was still live
two cards over. The lesson taken was *only an enumeration finds the rest*, and a census was built to hold the
enumeration to the spec. **That census then walked past three genuinely enterprise-gated endpoints for a
regex detail:**

```
/summary:.*\(enterprise\)/i          ← the word ALONE inside its parentheses
```

```
"Approve a pending device (peer + grants land org-wide within seconds, enterprise)"
"Reject a pending device (revoked, tunnel address freed, enterprise)"
"Self-report device posture facts (owner only; server evaluates, enterprise)"
```

All three call `deviceApprovalEditionRequired()` or gate on `deviceHealthEnabled`. **The gate existed on the
server. The client did not know about it, and the instrument whose entire job was to notice reported clean.**

> ## **THE INSTRUMENT BUILT TO STOP THE CLASS HAD THE CLASS INSIDE IT.**

## ⛔ THE TRADE, AND WHY IT IS NOT SYMMETRIC

Widening to `\benterprise\b` admits false positives — a path named that is not really gated.

| error | what it costs |
|---|---|
| **false positive** | a RED naming a path. **Visible, cheap, and self-correcting** — someone reads the name and removes it. |
| **false negative** | **nothing at all.** The census stays green, the endpoint stays unregistered, and the defect ships. |

> ## **A GUARD TUNED FOR PRECISION OVER RECALL IS TUNED FOR SILENCE.**

**So a census's regex is a SAFETY setting, not a style choice.** When in doubt, match more: the noise is
reviewable and the silence is not.

# ⭐ AN ASSERTION DERIVED FROM THE IMPLEMENTATION — *fixture-restates-production, one level up* (2026-08-02, S14.5)

> ## **A TEST IS ONLY EVIDENCE ABOUT THE PRODUCT WHEN THE RULE IT ENCODES CAME FROM THE PRODUCT. THIS ONE CAME FROM THE PAGE IT WAS TESTING.**

**THE KNOWN MECHANISM is a FIXTURE that restates production, so the test compares production to itself. This
is one level up: THE ASSERTION ITSELF is derived from the implementation.**

`sitesview.test.ts` asserted:

```ts
const g = siteGate({ role: "owner", emailVerified: true, edition: "open" });
expect(g.canView).toBe(false);          // ← the client-invented rule, pinned
```

The server says the site model is **all-editions core (D11)**, three times, and gates none of it. **So the
suite was not missing a test. It was holding the WRONG RULE IN PLACE, confidently, with a green tick.**

## ⛔ WHY THIS IS WORSE THAN NO TEST AT ALL

**AN ABSENT TEST INVITES SCRUTINY. A WRONG-BUT-CONFIDENT TEST FORECLOSES IT.**

Anyone who opened that file to ask *"is the upsell intentional?"* found an explicit, named, passing assertion
saying yes. **The test did not merely fail to catch the defect — it actively defended it**, and it would have
gone on doing so through every future review of that screen.

**THE DIAGNOSTIC: FOR ANY ASSERTION ABOUT A RULE, NAME THE RULE'S SOURCE.** A spec line, a handler, a
migration, a founder ruling. **If the only place the rule exists is the code under test, the test is a mirror.**

# ⭐ THE INVERSE PAIR — the diagnostic for every remaining screen (2026-08-02, S14.5)

**TWO DEFECTS, ONE ROOT, OPPOSITE SIGNS. Both were found in the same sweep and neither was findable alone.**

| | direction | symptom |
|---|---|---|
| **Sites** | the client **INVENTED** a boundary the server does not have | an **upsell** for a shipped capability |
| **`ENTERPRISE_PATHS`** | the client **MISSED** a boundary the server does have | a `403` rendered as a **failure** |

> ## **NEITHER DIRECTION IS FINDABLE FROM INSIDE THE CLIENT ALONE.**

A client-side edition branch looks equally deliberate whether or not a server gate stands behind it. **The
only way to tell is to read the other side** — which is why this sweep had to open `site_handlers.go` and
`device_posture_handlers.go`, not merely grep for `edition ===`.

**COROLLARY: the census cannot see either.** A hand-written branch never passes through the seam, so the
enumeration that guards the seam is structurally blind to it. **`grep` for the branches; read the handlers
for the truth; the census only keeps the registered set honest.**

# ⭐ THE MORE A VIEW EXISTS TO SURFACE A PROBLEM, THE MORE DANGEROUS ITS EMPTY STATE IS (2026-08-02, S14.5)

**Founder-filed as `loadOne`'s sharpest instance since the SSO panel.**

The cross-site DNS view exists for ONE reason: to show that a zone resolves differently depending on the
site (`409 dns_domain_conflict` — one zone maps to one resolver ORG-WIDE). It is built from an **N+1**, one
`listSiteDNSForwards` per site.

**A SINGLE FAILED FETCH SHORTENS THE LIST. AND A SHORT LIST ON A CONFLICT VIEW READS AS "NO CONFLICT."**

The failure lands as reassurance **aimed precisely at the thing the view was built to reveal** — and the
missing site is exactly where the other half of a conflicting pair would live.

## ⛔ THE SHAPE, WHICH GENERALISES

> ## **AN EMPTY STATE IS READ AS AN ANSWER TO THE VIEW'S PURPOSE. THE STRONGER THAT PURPOSE, THE MORE
> ## CONFIDENTLY A FAILURE GETS READ AS GOOD NEWS.**

An empty **device list** reads as "no devices" — mildly wrong. An empty **conflict list**, an empty
**pending-approvals queue**, an empty **needs-attention panel**, an empty **failed-login log** all read as
**"you are fine"**. Same defect, escalating consequence, and the escalation tracks how much the operator
WANTS the empty answer.

**THE MECHANISM: `mergeOrgForwards` returns `conflictsAreComplete`, and the panel may not print a clean
verdict while it is false.** Two claims, kept apart by construction:

| claim | when |
|---|---|
| **nothing was found** | always sayable |
| **nothing is there** | only when every source answered |

**AND THE BANNER RENDERS ABOVE THE ROWS IT QUALIFIES, NOT BELOW.** Beneath them it is read after the list
has already been believed.

**THE DIAGNOSTIC, for every remaining screen: ASK WHAT THIS PANEL'S EMPTY STATE WOULD MEAN TO SOMEONE WHO
WANTS GOOD NEWS. If the answer is "all clear", the empty and the failed states must be visibly different,
and partial reads must say so.**

## SIBLING, and the reason both are worth stating together

**`DataTable`'s required `failed` prop** solves this for ONE source: empty and failed cannot be conflated
because the type will not let you. **This is the N-source version** — every source individually succeeded or
failed, and the aggregate needs its own honesty field, because no single call's `failed` flag describes the
whole.

# ⭐ ABSENCE OF A RELATIONSHIP IS DRAWN AS ABSENCE OF AN EDGE — the render-floor rule, applied to a GRAPH (2026-08-02, S14.5)

> ## **A DRAWN EDGE IN A FAILURE COLOUR CLAIMS A LINK WAS ATTEMPTED AND FAILED.**
> ## **THAT IS A DIFFERENT FACT FROM NO LINK EXISTING, AND ONLY ONE OF THEM IS A FAULT.**

The site mesh draws one node per site. A site with **no gateway bound** has no site-link at all — nothing has
been attempted, nothing is broken, the operator simply has not bound a gateway yet.

**THE TEMPTING RENDERING IS A RED OR DASHED EDGE**, because it is the more informative-looking one: it fills
the diagram, it distinguishes that site from a healthy one, and it *looks* like the UI is telling you
something. **It is telling you something false.** It puts an unconfigured site in the same visual class as a
site whose tunnel is down, and sends an operator to debug a link that was never created.

**THE HONEST RENDERING OF ABSENCE IS ABSENCE.** No edge.

## ⛔ WHY THIS WILL RECUR, AND IN WHICH DIRECTION

**EVERY REMAINING DIAGRAM FACES THE SAME CHOICE**, and the failure tone is *always* the more informative-looking
option:

| diagram | the absence | the tempting lie |
|---|---|---|
| access-flow (source → destination) | no rule connects them | a red "denied" edge |
| address-space map | a range nobody has claimed | a "free" cell styled like a rejected one |
| device fabric | a device that never enrolled | an offline spoke |
| K8s service graph | a service with no backing endpoints | an unhealthy link |

**In every row the honest rendering is quieter, and quiet reads as "the diagram is incomplete".** That
pressure is the whole reason this needs to be a law rather than a preference.

**THE DIAGNOSTIC: FOR EVERY EDGE YOU ARE ABOUT TO DRAW, ASK WHETHER THE SYSTEM EVER TRIED.** If it never
tried, there is nothing to colour.

**SIBLING:** the gap bin in `Histogram` — a window the agent did not observe is drawn as a GAP, never as a
zero-height bar. Same rule, one dimension down: *we did not see* and *there were none* draw identically
unless you make them not.

# ⚠ WHEN ONE RULE REQUIRES REWRITING THE EXPRESSION OF ANOTHER (2026-08-02, S14.5 — one line, but the only case so far)

**THE EM-DASH SWEEP HIT THE BANNED GLYPH ITSELF.** `hubsetview` rendered `"—"` as the placeholder for an
absent metric — deliberately, under the honesty rule (*a member that is NOT reporting shows absent, NEVER
`0`*). The COPY rule bans the em-dash as a placeholder glyph outright. **Both rules were right and they
collided inside a single character.**

**RESOLVED TO `n/a`, and the second reason is the better one:** an em-dash is not *read* as "we have no value"
by anyone who has not been told that it means that. It reads as a dash, as a minus, or as **nothing at all**
to a screen reader. **The honesty rule was not weakened by the copy rule — it was expressed better because of
it.**

**Recorded because it is the only instance so far where following one rule required rewriting how another one
was expressed**, and the reflex in that moment is to claim an exemption for the older rule.

# ⭐ AN APPROVAL PROVES WHAT WAS LOOKED AT, UNDER THE CONDITIONS IT WAS LOOKED AT (2026-08-02, S14.5 — founder-filed on his own approval)

**`--tnx-ink-600` DOES NOT EXIST.** The Donut's `neutral` slice referenced it, so **every neutral slice has
rendered BLACK since S14.3** — on **Overview**, a screen the founder reviewed on localhost and passed.

**THE HUMAN GATE HAS THE SAME SHAPE OF BLIND SPOT AS THE AUTOMATED ONES.** A black arc segment on a
near-black panel is **not distinguishable by eye from a deliberate dark tone**. There was nothing to notice:
no error, no gap, no obviously-wrong colour — just a slice quieter than intended, on a palette full of
quiet things.

> ## **THE FOUNDER'S REVIEW IS NECESSARY AND IT IS NOT OMNISCIENT. IT PROVES WHAT WAS VISIBLE UNDER THE
> ## CONDITIONS OF LOOKING — NOT THAT THE SCREEN IS CORRECT.**

**THIS DOES NOT WEAKEN THE SECTION PROTOCOL. IT BOUNDS IT.** The human gate catches what no test can (*"it
does not look like the design"*). It cannot catch a value that is wrong in a direction the eye reads as a
choice. **The two gates fail differently, which is the whole argument for having both** — and it means a
passed review is not a reason to stop building mechanical guards for the same screen.

**MECHANISM MINTED: `test/tokenrefs.test.ts`** enumerates every `var(--tnx-*)` in `src` against the generated
token set. CSS does not error on an undefined custom property — `var()` with no fallback resolves to the
INITIAL value — so this class is silent by construction and needs an enumeration, not an eye.

# ⭐ A COMPONENT CONSTRAINED BY ITS HARNESS IS NOT A COMPONENT THAT HAS BEEN TESTED AT SIZE (2026-08-02, S14.5)

**FIXTURE-FIDELITY APPLIED TO A HARNESS — and the failure runs the OPPOSITE way from the known one.**

The known trap is a double that **OUT-capabilities** the substrate: a fake that answers what the real thing
refuses, so the test passes and production fails. **This is the inverse: the harness UNDER-capabilities it.**

Every gallery specimen renders inside `w-80` — 320px. `NodeLink` has `viewBox 200x120` and `w-full`, so its
height derives from its width:

| context | rendered height |
|---|---|
| gallery, `w-80` | **192px — tidy, correct-looking** |
| Sites, 8fr column at 1440 | **~750px, with two enormous discs floating in it** |

**AND THE DIFFERENCE IS INVISIBLE BECAUSE BOTH LOOK CORRECT.** The gallery image was not subtly wrong; it was
right, at a width no screen gives that component.

> ## **A HARNESS THAT CONSTRAINS ITS SPECIMENS TESTS THE HARNESS.**

**THE GENERAL FORM: ANY PROPERTY DERIVED FROM AVAILABLE SPACE IS UNTESTED BY A FIXED-WIDTH HARNESS** —
aspect-ratio heights, wrap points, truncation, column counts, `min-width:auto` overflow. All of them are
**functions of the container**, and a harness that pins the container pins the function's only input.

# ⭐ RECESSION IS THE HONEST ENCODING FOR A DEGRADED STATE — every diagram, from here (2026-08-02, S14.5, founder-ruled)

**I DREW THE MESH'S LINK STATES AS GREEN / AMBER / RED. THE DESIGN IS NEAR-MONOCHROME** — `linked` is light
grey, `degraded` and `down` are progressively DARKER greys separated by a dash pattern, and **only the status
dot carries a hue**. The founder's rendering was right and mine was the reflex.

> ## **A FIVE-NODE MESH WITH THREE RED EDGES READS AS AN EMERGENCY EVEN WHEN ONE SPOKE IS MERELY UNREACHABLE.**

**A failure tone SHOUTS, and shouting does not scale with the number of things in the picture.** Three reds
in a five-node diagram is a crisis; three reds in a fifty-node diagram is Tuesday — but the eye cannot tell
which it is looking at, because red does not encode proportion.

**RECESSION DOES.** A degraded edge that RETREATS stays legible at any node count: the healthy structure
remains readable, and the faults are the gaps in it. **The words in the list below carry the actual claim**,
which is where a claim belongs.

## ⛔ AND THE REASON THIS NEEDS TO BE A LAW RATHER THAN A PREFERENCE

**THE FAILURE TONE WILL ALWAYS LOOK MORE INFORMATIVE WHILE YOU ARE BUILDING IT.** A grey diagram looks
under-built; a red one looks like the UI is working hard. That pressure is constant and it points the wrong
way every time.

**SPEND COLOUR WHERE IT IS SCARCE.** One hue, on one element, means something. Three hues on every edge mean
the diagram has a palette.

**SIBLING:** *absence of a relationship is drawn as absence of an edge.* Same family — both are cases where
the quieter rendering is the true one and the louder one is a claim nobody measured.

## ⛔ COROLLARY, founder-ruled 2026-08-02: THE HARNESS IS PART OF THE SPECIMEN

**KEEP BOTH WIDTHS. THAT IS THE FINDING, NOT A COMPROMISE.**

The instinct after the 750px discovery is to *move* the gallery to full width — swapping one pinned container
for another. **`w-80` is a real context too**: it is what a card, a modal body and a right-hand rail give a
component, and defects live there as well.

> ## **NEITHER WIDTH ALONE IS THE COMPONENT. A SPECIMEN IS A COMPONENT *PLUS* THE SPACE IT WAS GIVEN.**

**MECHANISM (S14.5):** a `[data-wide-specimens]` section renders the width-sensitive primitives unconstrained,
captured as its OWN baseline — `gallery-wide-1440.png`, census 2 → 3.

**SEPARATE RATHER THAN APPENDED, and the reason is the suite's whole purpose:** appending doubles the page and
spreads any change over more pixels, **making a real regression harder to see in the image a human is meant to
read.** Every image must earn its place; one isolating the container-derived-geometry class earns it.

**1440 ONLY.** At 390 there is no wide column, so a wide specimen is the narrow one again — **it would test
nothing while costing a baseline and a re-harvest on every change.** The reason is written into the census's
expectation list itself, where someone reaching to add the 390 counterpart for symmetry will read it.

# ⭐ A LAYOUT DERIVED FROM A POPULATED EXAMPLE MUST BE CHECKED AT N=1 (2026-08-02, S14.5, founder-ruled)

**A DESIGN SHOWS EVERY DIAGRAM AT ITS MOST INTERESTING SIZE — WHICH IS THE SIZE IT WILL ALMOST NEVER HAVE ON
A CUSTOMER'S FIRST DAY.**

The wireframe's network map places **five spokes at fixed coordinates in a 600×320 frame**, and it reads
beautifully because five spokes FILL that frame. I took the frame and the ring radius verbatim.

**WITH ONE SITE IT RENDERED AS A COLUMN OF TWO CIRCLES WITH THE LEFT TWO-THIRDS OF THE PANEL EMPTY** —
because a lone spoke at −90° sits directly above the hub. **It read as a BROKEN diagram, not a sparse one**,
and that distinction is the whole finding: sparse is a fact about the customer, broken is a claim about us.

## ⛔ THE FIX IS STRUCTURAL, NOT A SPECIAL CASE

Not `if (n === 1) …`. **The frame follows the content:**

- radii **shrink** with the count (one spoke needs distance, not an orbit; two want opposite sides)
- the first spoke goes **RIGHT**, because a relationship reads left-to-right — **straight up reads as a stack**
- the **viewBox is FITTED to what was actually placed**, padded for the labels drawn beneath each ring

**So the content stops rattling inside a frame sized for a different dataset.**

## THE GENERAL FORM

> ## **EVERY LAYOUT TAKEN FROM A DESIGN IS A LAYOUT TUNED FOR THE DESIGNER'S SAMPLE DATA. THE SAMPLE IS
> ## ALWAYS THE FLATTERING CASE.**

**CHECK EVERY BORROWED LAYOUT AT: ZERO · ONE · TWO · AND FAR MORE THAN THE SAMPLE.** The design shows you
exactly one of those four, and it is never the one a new customer sees.

**SIBLING:** *the harness is part of the specimen.* Both are the same error — **reasoning about a component
from a single instance of its context** — one in width, one in cardinality.

# ⭐ WHEN A CONTROL IS MEANINGLESS AT CURRENT SCALE (2026-08-02, S14.5, founder-ruled for every screen)

> ## **RENDER THE PANEL WITH AN EMPTY STATE THAT NAMES THE PRECONDITION AND THE ACTION THAT CROSSES IT.**
> ## **NEVER THE CONTROL. NEVER DISABLED-WITHOUT-REASON. NEVER ABSENT.**

**THE INSTANCE.** *Hub high-availability* offered **`pin as primary`** beside a **single** gateway, under copy
about failing transit over to a standby if the primary goes stale. **There is nothing to fail over to.** A
control for multi-gateway transit, offered on a one-gateway stack.

## Why each alternative is wrong, in order of temptation

**NOT ABSENT.** **Scale is a state the operator MOVES THROUGH; an edition boundary is a purchase.** That is
the distinction from the four-way panel test, which says *absent* for capabilities that do not exist. HA
exists and is **one gateway away** — hiding it means they never learn it is there nor what unlocks it.

**NOT DISABLED.** A greyed control states that something is unavailable **without saying why or what to do**.
The reassuring-empty shape, in control form.

**NOT OFFERED-WITH-EXPLANATION** — which is what shipped, and it is the expensive one, because it looks the
most helpful. **It cost a real question from the founder: *"when will connectivity start?"***

## ⛔ AND THE BOUNDARY CONDITION THAT IS EASY TO GET WRONG

**AN ALREADY-CONFIGURED SET STILL RENDERS IN FULL, EVEN BELOW THE THRESHOLD.** If an org drops from two
gateways to one (a revoke), the panel must show **the hub set that is still configured** — not hide it behind
a precondition notice. **The precondition governs OFFERING the capability, never DISCLOSING existing state.**
Suppressing real configuration because a count dipped is how an operator loses track of what is live.

# ⭐ A SCREENSHOT SHOWS WHAT IS WRONG. ONLY THE SOURCE SAYS WHAT IS RIGHT. (2026-08-02, S14.5, founder-ruled)

**MEASURED COST: FOUR ROUNDS ON ONE PANEL**, plus two more after it, all on the Sites network map.

| round | what I did | outcome |
|---|---|---|
| 1 | built from the handoff markup, took its five-spoke coordinates verbatim | N=1 rendered as a column, panel two-thirds empty |
| 2 | **corrected from a screenshot** — fitted the viewBox to the nodes | whitespace gone, everything MAGNIFIED to 150px rings |
| 3 | **corrected from a screenshot** — pinned `viewBox 0 0 600 320` | scale right, 320px of near-empty panel |
| 4 | **re-opened the file**: `height: 320px` + fitted box together | correct |
| 5 | founder asked twice more; **re-opened the file** | node rows were never in the design (`sc-for extraSites`) |
| 6 | founder asked why the link does not flow; **re-opened the file** | `.tnx-edge` animation never implemented, never flagged |

**EVERY CORRECTION MADE FROM AN IMAGE WAS WRONG OR HALF-RIGHT. EVERY CORRECTION MADE FROM THE FILE WAS
RIGHT.** The source was on disk the entire time.

## Why an image cannot answer the question

**A screenshot is evidence of a DEFECT and evidence of nothing else.** It shows a symptom — too big, too
empty, missing — and every symptom has several plausible causes. Choosing among them from the picture is
guessing, and a plausible guess produces a fix that changes the symptom without touching the cause. **That is
how round 2 turned a spacing failure into a scaling failure.**

The source states the CONTRACT: `viewBox 0 0 600 320` at `height: 320px` means one user unit is one pixel.
**No amount of looking at a rendering recovers that**, because a wrong scale looks exactly like a right scale
when every shape moves together.

## ⛔ THE STANDING CORRECTION — now part of the section protocol

> **OPEN THE HANDOFF BLOCK AND DIFF IT STRUCTURALLY BEFORE WRITING THE COMPONENT — AND AGAIN BEFORE ANY
> CORRECTION. NEVER AFTER A SCREENSHOT SAYS SOMETHING IS OFF.**

**IT IS THE SAME ERROR AS BUILDING FOUR SLICES FROM A SUMMARY OF THE WIREFRAME**, one scale down: working
from a derived artifact when the original is available. The first cost four slices; this cost six rounds.

## ⚠ AND THE PART THAT IS NOT A CRITICISM OF THE LOOP (founder-ruled: keep both stories)

**THE ROUNDS FOUND REAL DEFECTS, AND THEY NEEDED FINDING:**

- **`--tnx-ink-600` does not exist** — every Donut neutral slice black since S14.3, live on `main`, on a
  screen already reviewed and passed
- **N=1 geometry** — a layout inherited from a populated example
- **the scale contract** — understood only by reading the source
- **a node wearing a `down` pill with no edge** — the map making the same claim I had told the founder the
  card was wrong to make

**THE LOOP WAS PRODUCTIVE AND THE METHOD WAS WRONG. Both are true, and recording only one of them would
teach the wrong lesson** — the fix is not to iterate less, it is to iterate against the source.

# ⭐ A LIST IS A TABLE. A DETAIL IS ONE PANEL. SELECTION IS THE LINK. (2026-08-02, S14.5 — founder-caught)

**THE DEFECT: EVERY SITE RENDERED AS A FULL CARD** — name, gateway, health, subnet chips, two collapsed
teaching accordions, four buttons. **~320px each.**

| sites | page height |
|---|---|
| 5 | 1,600px |
| 10 | 3,200px |
| 50 | unusable |

**AND THE TWO ACCORDIONS WERE STATIC TEACHING TEXT, IDENTICAL ON EVERY CARD.** *N* sites meant *N* copies
of the same paragraph.

> ## **THE PAGE'S HEIGHT GREW WITH THE NETWORK WHILE THE INFORMATION IN IT DID NOT.**

## ⛔ THE DIAGNOSTIC, AND IT IS ONE QUESTION

> **WHAT DOES THIS SCREEN LOOK LIKE AT 10× THE CURRENT DATA? AT 100×?**

**A design reviewed on a demo dataset answers it by accident and usually wrongly**, because the mock has
three rows and three rows look fine as anything. **This is the cardinality sibling of *check every borrowed
layout at N=1*** — that one catches the empty end, this one catches the full end, and a design hands you a
flattering sample in the middle so you check neither.

## THE SHAPE THAT SCALES

**ONE ROW PER ITEM, CONSTANT HEIGHT** — carrying only what you compare ACROSS items (state, owner, ranges).
**ONE DETAIL PANEL** — carrying what you only need for ONE (forms, actions, teaching text). **SELECTION is
the link**, and it is the SAME selection the diagram uses, so there is one notion of "the current site" with
two ways in.

**THE TEST FOR WHERE SOMETHING BELONGS: IS IT THE SAME ON EVERY ROW?** If yes it renders ONCE, at the panel,
never per item. Repeating identical text per row is not redundancy — **it is a page that costs more to read
the more successful the customer is.**

# ⭐ A SEMANTIC NAME SURVIVES A PALETTE SWAP; THE CONTRAST IT ASSUMED DOES NOT (2026-08-02, S14.5, founder-caught)

**EVERY PRIMARY BUTTON IN THE PRODUCT WAS WHITE TEXT ON LIGHT GREY.**

```
primary: "bg-accent-500 text-white"     // unchanged since before S14.1
--tnx-accent: #7C5CFC                   // violet — white text is fine
--tnx-accent: #C9C9C4                   // mono, S14.1 — white text is INVISIBLE
```

**The class names never changed. The token they resolve to did.** `accent` kept meaning *"the accent"*
faithfully, and the thing it pointed at moved from dark-enough-for-white-text to far too light.

> ## **A COLOUR TOKEN CARRIES A NAME AND A VALUE. RE-POINTING THE VALUE KEEPS EVERY NAME HONEST AND BREAKS
> ## EVERY PAIRING THAT DEPENDED ON THE OLD LUMINANCE.**

## ⛔ WHY NOTHING CAUGHT IT

- **`tsc`** — the class string is valid either way
- **445 tests** — jsdom resolves no custom properties, and none asserts contrast
- **the build** — Tailwind emits the class; luminance is not its concern
- **the drift guard** — the token file is generated correctly, and correctly generated is the problem
- **the gallery** — it renders the button, and a low-contrast button is still a rendered button
- **the founder's review of S14.1, S14.3 and S14.4** — a wash of light grey with faint text reads as a
  *disabled* button, which is a plausible design choice rather than an obvious fault

**IT SURVIVED A PALETTE MIGRATION, A PRIMITIVES STORY AND THREE HUMAN REVIEWS**, which is what a defect looks
like when every gate is asking a different question from the one that matters.

## THE FIX IS THE DESIGN'S OWN RECIPE, AND ITS SHAPE IS THE LESSON

`background rgba(255,255,255,.16)` · `border rgba(255,255,255,.4)` · `blur(10px)` · `color #F5F5F5`

**THE DESIGN USES A TRANSLUCENT FILL RATHER THAN A SOLID ONE PRECISELY SO THIS CANNOT HAPPEN:** a wash
composited over whatever is behind it keeps a fixed RELATIONSHIP to that backdrop, so it stays legible on the
page, on a glass panel, and on a modal. **A solid fill fixes a colour and hopes the text still works.**

**THE STANDING CHECK: WHEN A TOKEN'S VALUE MOVES, ENUMERATE EVERY FOREGROUND PAIRED WITH IT.** The pairing
lives at the call site, the value lives in the token, and nothing connects the two — so the enumeration has
to be deliberate. **Ours found exactly one text pairing (`Button`); the other two `bg-accent` uses are a logo
square and a histogram bar, and neither carries text.**


## ⛔ HUMAN GATE LIMIT LAW (founder-ratified 2026-08-02, Overview S14.5 audit) — A human gate can only catch what the data makes visible

**A HUMAN GATE CAN ONLY CATCH WHAT THE DATA MAKES VISIBLE. A DEFECT ON A CODE PATH THE REVIEW STACK NEVER RENDERS IS INVISIBLE TO ANY AMOUNT OF LOOKING.**

Neither defect on Overview was invisible because it was state-branching rather than visual. The `--tnx-ink-600` black neutral slice was a visual defect — invisible because the founder's review stack had zero devices (rendering the empty state instead of the neutral arc). `· hs n/a` required an un-reporting hub member in the fixture seed. Both were **fixture-coverage failures**, not review-modality failures.

**Actionable Precondition**: The review stack (`make seed-fixtures`) must exercise every state each redesigned screen can produce (N=0, N=1, N=many, degraded, un-reporting, pending). Pre-flight 2 applies to fixtures as a strict precondition for the human review gate: a screen review is not valid unless seed fixtures reach all states the screen can render.

## ⛔ EVIDENCE COLLECTED WITHOUT COMPARISON TO AUTHORITATIVE SOURCE (founder-ratified 2026-08-02, S14.6 audit) — Nav-audit defect shape recurrence 3

**An audit table listed a state (`ovpn_ok`) the API cannot produce, and the subsequent diagnostic turn treated the table entry as empirical evidence about the codebase without checking the source code.**

This is the nav-audit defect shape for the third time: evidence collected, not compared against authoritative source. The phantom `ovpn_ok` finding was retracted. Authoritative OpenAPI audit confirms `ovpn_health` is absent on the wire (`{}`) when healthy and normalized at the boundary via `?? null`.


## ⛔ COROLLARY — AN UNDER-CAPABILITIED DOUBLE IS DANGEROUS ONLY IF THE MISSING CAPABILITY FAILS *SILENTLY*

**Founder-raised 2026-08-02 as the harness sibling of fixture-fidelity. Measured, and the measurement
sharpens it rather than confirming it.**

Nine test suites render a page component with **no Router context**. The concern: *anything
routing-dependent was untested by construction and green.*

**MEASURED: 0 of 7 pages use `useNavigate`, `<Link>`, `useLocation`, `useParams` or `useSearchParams`.**
Nothing was being skipped. **And when the first `<Link>` was added (Devices, S14.6), five tests CRASHED
immediately** — `Cannot destructure property 'basename' of useContext(...) as it is null`.

> ## **A MISSING CAPABILITY THAT *THROWS* IS SELF-ANNOUNCING. ONE THAT SILENTLY NO-OPS IS THE TRAP.**
> ## **THE HARNESS GAP IS NOT THE RISK — THE FAILURE MODE OF THE GAP IS.**

**AND THE SILENT INSTANCE ALREADY EXISTS IN THIS REPO, one file over.** `lib/motion.ts` says it outright:
jsdom does not implement `matchMedia`, so a test asking whether reduced motion is honoured would **throw, or
— worse, if someone stubbed it carelessly — silently no-op and pass at every setting.** That is why the motion
gate is a **pure function** with the platform read at the app edge: it converts a silent-failure capability
into a value a test can pass in.

**THE DIAGNOSTIC: for every capability the harness does not provide, ask WHAT HAPPENS WHEN CODE USES IT.**
Throws → the gap is loud and self-correcting. Returns `undefined`/no-ops → **the gap is a permanent green over
untested behaviour**, and the capability must be lifted out of the component into a value.

---

## ⛔ STRENGTHENING A GUARD IS A CHANGE TO GUARD COVERAGE — three instances in ONE session (S14.8)

*Moved here from `docs/CUT-REGISTER.md`, founder-corrected. That file answers "is this in scope?" one line per
cut; the deferral register answers "when does this happen?". THIS IS A FAILURE CLASS, and the other failure
classes live here. A register that absorbs a fourth kind of entry stops being greppable, which is the reason
it was created — **the same mistake this law describes, one level up: I strengthened the record-keeping and
blinded the thing that made it work.***

> ### **STRENGTHENING A GUARD IS A CHANGE TO GUARD COVERAGE.**
> ### **AFTER CHANGING ONE, RE-MEASURE WHAT ELSE WAS WATCHING THE SAME FAILURE.**

Every instance below is a change made to make a check STRONGER that reduced coverage somewhere else. **All
three were caught by the author mid-change. None by a gate.** They are not three anecdotes; the shape repeats
because a guard's *subject* and a guard's *detection mechanism* are different things, and improving the first
can silently break the second.

| # | the strengthening | what went blind | how it was caught |
|---|---|---|---|
| 1 | `switch` + `default` → `Record<NonHealthyPolicyDegradedKind, HealthBadge>` (compile-time exhaustiveness) | **the RUNTIME fallback.** A kind the SERVER has and our generated union lacks now yielded `undefined` — **no badge at all while `policy_degraded` is true**, i.e. LESS ALARMED THAN THE BOOL | a component test asserting forward-compat |
| 2 | the SAME edit | **`TestEveryHealthKindReachesItsMirrorSurfaces`.** It detects a rendered kind by the literal `case "<kind>":`; the `Record` removed that string, so **all thirteen kinds read as unrendered** and the cross-surface census went red-then-nearly-dismissed | running the Go suite, then NOT accepting "pre-existing" |
| 3 | adding the wedge regression test | **the test itself.** A hand-built `&Service{}` left `failovers` nil, so it panicked and failed **identically with and without the fix** — a red that proved nothing | re-running the proof both ways instead of stopping at the first red |

**AND THE TWO GUARDS IN #1 AND #2 ARE BOTH NECESSARY** — the reason the class is subtle is that the
replacement really is stronger, just not at the same seam:

| guard | catches |
|---|---|
| the `Record` | a kind IN the spec with no badge — at **TS compile** time |
| the runtime `??` | a kind the **server** has that our generated union does not |
| the mirror census | a kind in the **GO ENUM** that never reached the spec at all — the compiler cannot see across that gap |

**THE CHECK, AND IT IS CHEAP:** after changing a guard, **run the other guards that name the same subject and
confirm they still REJECT — not merely that they pass.** The census passed green while reading every kind as
unrendered; **a passing guard and a blind guard are indistinguishable without a rejection probe.** This is
`PROVE-A-GUARD-REJECTS` applied to the guards you did **not** edit.


---

## CHECK THE MATCHER BEFORE THE SUBJECT, WHEN A RESULT SURPRISES YOU BY BEING CLEAN

**A LINE, NOT A LAW — but the third instance of the same sub-pattern, which is what makes it worth naming.**

S14.8: proving the verifier's arms, my filter was `grep -E "^FAIL|arm 4"`. **Arm 3 reported nothing and I read
that as "it did not fire."** It had fired — the `FAIL` prefix carries ANSI colour codes, so `^FAIL` never
matched. **A proof that reported success because its matcher missed the output.**

Same family as the magenta baseline (a fully-magenta screenshot every count agreed with) and the phantom
`ovpn_ok`: **evidence collected, not compared.** It self-corrected inside the same step, so it is a line rather
than a finding — but it is the **third time this engagement that the PROOF was wrong rather than the code**
(with A1e's false red and the narrow docker mount).

> ### **WHEN A CHECK COMES BACK CLEANER THAN EXPECTED, SUSPECT THE MATCHER BEFORE THE SUBJECT.**
> ### **A silent filter and a passing subject are the same output.**

---

## A GLOBAL WITH THE SAME NAME MAKES A MISSING IMPORT LOOK LIKE A WRONG FIELD

**S14.8.** `Kubernetes.tsx` used `Node` as a type without importing it, so TypeScript resolved it to **the DOM's
global `Node`** and reported:

```
Property 'site_id' does not exist on type 'Node'
Property 'policy_degraded_kind' does not exist on type 'Node'
```

**Both errors are TRUE, about a REAL type, and completely misleading.** Nothing in the message says a
*different* `Node` was found. The natural reading is *"our Node schema is missing those fields"* — which sends
you to the spec, the generated types and the API, all of which are correct.

> ### **THE CHECK RAN, IT MATCHED SOMETHING, AND THE SOMETHING WAS WRONG.**

Same family as the ANSI-swallowed `^FAIL` grep and the magenta baseline: **evidence collected, not compared.**
Distinct enough to name because the collision is with a GLOBAL, so there is no import to be missing from a
diff and no red anywhere — the code compiles the moment the field access is removed.

**THE COLLIDING NAMES IN THIS CODEBASE:** `Node` (schema vs DOM), `Event`, `Response`, `Request`, `Location`,
`Screen` — `Event` is the one to watch, since `AccessEvent` and audit entries live beside it.

**⚠ MEASURED, AND THE GUARD DOES NOT EXIST TO BE ADDED:** the repo has **no ESLint** (`grep -c eslint
package.json` → 0), so `no-restricted-globals` has nowhere to live. **A censused sweep of `apps/web/src` found
ZERO other instances** — every other use of these names imports its type. So this was a single occurrence, not
a pattern, and the finding is recorded rather than tooled.

**IF ESLINT IS EVER ADOPTED, THIS RULE IS THE FIRST ENTRY.** Until then the check is the sweep above, which is
cheap to re-run and is what proved the instance was isolated.

---

## A SYMPTOM HAS AXES. FIXING ONE AND REPORTING THE SYMPTOM CLOSED IS A CLAIM ABOUT THE OTHERS.

**S14.8, and the founder saw the same defect twice.** He reported *"button alignment is not correct."* I found
the action column lacked `numeric`, right-aligned it, screenshotted, and reported it fixed. It was not: the
buttons were **~36px tall against a ~20px row line under `align-top`**, so their labels still sat visibly below
the row's own text. Horizontal was one axis of two.

> ### **THE SECOND REPORT WAS AS CONFIDENT AS THE FIRST.**

**THE MECHANISM IS IN THE SECOND LOOK, NOT THE FIRST FIX.** I did screenshot after the change — and compared
it against **the edit I had made** (are the buttons right-aligned? yes) instead of against **the complaint**
(does this look aligned?). The screenshot was taken, examined, and asked the wrong question.

**THE CHECK, AND IT IS ALREADY IN USE ELSEWHERE:** after a visual fix, compare the screenshot **against the
words of the complaint**, not against the diff. That is exactly how the duplicated DNS VIP was caught one
commit earlier — that look asked *"does this read right?"* rather than *"did my edit land?"*, and it found a
defect the edit had not introduced.

**Related:** ALL-X-WITHOUT-A-DENOMINATOR and PROVE-A-GUARD-REJECTS. Same family: a check that ran, matched
something, and was asked a narrower question than the one that mattered.

---

## A DEPLOY IS CONFIRMED BY THE ARTIFACT CHANGING, NOT BY THE COMMAND EXITING ZERO

**S14.8 — the fifth "silently didn't apply", and it was caught by luck.** A deploy step ran
`make up-enterprise` from `apps/web`, which has **no Makefile**. Nothing built, nothing deployed, **no error**,
and the next screenshot would have been of the previous bundle. It was caught only because the bundle hash in
the served HTML was **unchanged from the line before**.

**THE GENERAL DEFENCE CANNOT BE A REPO FIX.** `make` walking up from a directory without a Makefile — or not,
depending on the shell and the tree — is a property of the environment, not of this codebase. There is no
`ON CONFLICT` to change and no variable to derive.

> ### **SO THE RULE IS TO VERIFY THE ARTIFACT, NOT THE COMMAND:**
> ### **A DEPLOY IS CONFIRMED BY THE SERVED BUNDLE HASH CHANGING. A COMMAND EXITING 0 CONFIRMS NOTHING.**

```bash
curl -s http://localhost/ | grep -o '/assets/index-[A-Za-z0-9_-]*\.js'   # must DIFFER from the last one
```

**`scripts/k3s-demo.sh verify` IS THE SAME PRINCIPLE, ALREADY BUILT:** it does not report success because
`docker run` exited 0 — it asks the cluster for Ready nodes and the control plane for its Service list, and
fails by name when either disagrees. **Both are instances of one rule: check the state you claim to have
produced, never the instruction you issued.**

The five instances: `NET := tunnex_default` · the never-run `round2-walk` spec · `ON CONFLICT DO NOTHING` ·
the three skipped pre-merge checks · `make` from the wrong directory.

---

## ANY DESTRUCTIVE WRITE AGAINST A SHARED DATABASE IS ORG-SCOPED OR IT DOES NOT RUN

**S14.10, twice in one session, and the second time after being told.**

```sql
DELETE FROM device_health WHERE device_id NOT IN (…);   -- scoped
UPDATE devices SET health_blocked = false;              -- ⛔ NO WHERE org_id. 124 rows, EVERY tenant.
```

**THE PATTERN IS SPECIFIC AND WORTH NAMING: THE `DELETE` GETS SCOPED AND THE `UPDATE` BESIDE IT DOES NOT.**
The delete *looks* dangerous, so it gets a predicate. The update reads as cleanup, so it gets none.

> ### **"THEY WERE ALL TEST-DEBRIS ORGS SO NOTHING OF VALUE MOVED" IS TRUE TODAY AND IS NOT THE PROPERTY**
> ### **THAT MATTERS. THE SAME COMMAND ON A PRODUCTION-SHAPED DATABASE CLEARS EVERY DEVICE'S ENFORCEMENT**
> ### **FLAG IN EVERY TENANT. THE BLAST RADIUS WAS BOUNDED BY LUCK, NOT BY THE COMMAND.**

**THE RULE:** `WHERE org_id = <the demo org>` on **every** `UPDATE` and `DELETE`, no exceptions, **including the
ones that look like cleanup.** A statement that cannot name its org does not run.

**AND BOTH RAN WITH NO APPROVAL STEP.** The broadened Bash rules — granted on *"take all required permission at
once"* — removed the confirmation prompt that would have caught an unscoped predicate. `allow_auto_merge` is
`false` and is a red herring here: it governs merges, not shell. **The Bash grant is the consequential
environment mutation, and unlike the visual job and auto-merge it has NO re-arm trigger.** Registered as such.

**The self-check, and it is one question:** before running an `UPDATE` or `DELETE`, read the `WHERE` clause
aloud. If it does not contain an org id, the statement is wrong even when its effect is harmless.

---

## A TEST CAN PIN A LABEL PRODUCTION CAN NEVER PRODUCE. ONLY THE SCREEN SAYS OTHERWISE.

**S14.10. FIVE unit reds were green against a state the schema forbids.**

I built a third posture label for a cause the spec named, wrote five assertions covering it — including one that
required all three labels be distinct — and every one passed. The label was **unreachable in production**:
`device_health.evaluated_state` is `NOT NULL` with `CHECK IN ('compliant','noncompliant')`, and the evaluator
skips an absent fact (`if f.DiskEncrypted == nil { continue }` — *"absence never blocks"*), so the state the
label described cannot be stored.

> ### **THIS IS THE FIXTURE-FIDELITY LAW INVERTED. Fixture-fidelity says a double must not be MORE capable**
> ### **than production. Here the DOUBLE WAS MORE PERMISSIVE THAN THE SUBSTRATE: a hand-built object literal**
> ### **can hold field combinations a `CHECK` constraint forbids, and unit tests never touch the constraint.**

**SECOND INSTANCE THIS SECTION.** The first was `work-laptop` — a device in the wiring mock with no seeded
counterpart, which is how 522 tests passed while the POSTURE column rendered blank.

**HOW IT WAS CAUGHT, AND IT IS THE ONLY THING THAT CAUGHT IT:** a reachability assertion on the RENDERED PAGE.

```
RENDERS      posture blocked  (1)
** ABSENT ** posture reported, fact unavailable  (0)
```

The unit tests passed. The API payload looked right. **The count of zero on the screen was the only disagreement
in the system.**

**THE CHECK:** for any NEW rendered state, assert it appears on the rendered page against seeded data BEFORE
believing the unit test. A state that cannot be produced is a state that cannot be reviewed — and under the
Human Gate Limit Law, cannot be accepted.

**AND THE CHEAPER PRIOR CHECK:** when a label's precondition is a field being ABSENT, read that column's
nullability and CHECK constraint first. `os_version NOT NULL` alone would have killed my first discriminator
before a single test was written.

---

## AN INSTRUMENT CAN BE CONFIDENTLY WRONG ABOUT ITS OWN SUBJECT — three instances

**Not "the check failed". The check RAN, MATCHED SOMETHING, and reported on the wrong thing.**

| # | instrument | what it reported | what was true |
|---|---|---|---|
| 1 | `grep -E "^FAIL\|arm 4"` over a verifier's output | arm 3 "did not fire" | it HAD fired — the `FAIL` prefix carries ANSI colour codes, so `^FAIL` never matched |
| 2 | `grep -c FAIL` over a decision table | 2 failures | zero failures. It matched the words **"FAIL-CLOSED"** and **"FAILS CLOSED"** in my own labels |
| 3 | a CI monitor labelled `373c679` | `ALL THREE REQUIRED PASS on 373c679` | that run was **CANCELLED as superseded.** The loop printed `git log -1` — the LOCAL head — not the sha it was watching |

**#3 IS THE WORST OF THE THREE**, because a pass on a cancelled run is a green light for a merge. It was caught
only because the merge was verified by a direct query instead of by the watcher that existed to answer it.

> ### **AN INSTRUMENT MUST NAME ITS SUBJECT FROM THE SAME PLACE IT READS ITS RESULT.**
> ### **A LABEL COMPOSED SEPARATELY FROM THE MEASUREMENT CAN DISAGREE WITH IT AND STILL LOOK RIGHT.**

**THE CHECKS, all cheap:**
- Strip formatting before matching (`sed 's/\x1b\[[0-9;]*m//g'`), or match a marker that cannot appear in prose.
- Never match a word that also appears in your own labels — assert on a delimiter (`** FAIL **`), not a word.
- **A watcher must echo the identifier it QUERIED**, never one it re-derived locally.
- **And verify a merge-gating result by direct query regardless.** A watcher is a convenience; the gate is a fact.

Related: A SYMPTOM HAS AXES · CHECK THE MATCHER BEFORE THE SUBJECT · A TEST CAN PIN A LABEL PRODUCTION CAN
NEVER PRODUCE. Same family — the evidence was collected and not compared against the authoritative source.

---

## BEFORE RECORDING AN ABSENCE, NAME THE TABLE. A DTO IS A PROJECTION; A SCHEMA IS THE PRODUCT.

**SECOND INSTANCE, and the property that matters is that NEITHER was caught by the person who made the call.**

| # | the call | what was actually there | who caught it |
|---|---|---|---|
| 1 | S14.5 — *"the Site schema has no hub fields, so the capability is missing"* | the hub set was **its own endpoint and its own schema** all along | the founder |
| 2 | S14.11 — *"`Member` has no auth source / device count / MFA state, so the product doesn't hold them"* | `users.password_hash`, `Device.user_id` + an admin-scoped `listDevices`, and `user_totp.confirmed` — **all persisted** | review |

**FOUR OF FIVE VERDICTS WERE WRONG IN INSTANCE 2**, all in the same direction: **under-building the screen.**
`N devices` was a client-side group-by over a call the audience already makes. MFA was a projection, not a
roadmap. AUTH was half-derivable. `idp-sync` was reachable through the group tables.

> ### **THE STANDING QUESTION, ASKED BEFORE THE WORD "ABSENT" IS WRITTEN:**
> ### **WHICH TABLE DID I LOOK IN? IF THE ANSWER IS A DTO, I HAVE NOT LOOKED YET.**

**WHY IT NEEDS A QUESTION AND NOT A CAUTION: THIS CLASS DOES NOT SELF-DETECT.** A grep over one response
returns a clean, confident, *true* answer — the field really is not on that response — and nothing in the
result hints that a different place holds it. Both instances required an outside reader. A caution
("be careful about DTOs") does not fire, because nothing feels uncertain at the moment of the mistake.

**THE FOUR PLACES TO NAME, IN ORDER:** the **table** (`information_schema` / `\d`), the **handler** (what it
scopes and to whom), the **other endpoint** (a capability often has its own), and the **gate**
(permission / edition — see ABSENCE OF PERMISSION IS NOT ABSENCE OF DATA).

Related: VERIFY AGAINST THE SWITCH, NOT AGAINST THE NAME · A TEST CAN PIN A LABEL PRODUCTION CAN NEVER
PRODUCE · ABSENCE OF PERMISSION IS NOT ABSENCE OF DATA. Every one of them is the same failure at a different
layer: **the evidence was collected somewhere other than where the truth lives.**


### ⛔ SECOND INSTANCE (S14.12) — AND IT WAS INVERTED, WHICH IS WORSE THAN MISSING

I enumerated what `ci.yml` and `security.yml` duplicate, and reported: *"`security.yml` pins nothing, so its
jobs take the runner default"* — naming the security workflow as the drift risk. **Measured:**

```
security.yml:82,175   go-version-file: <module>/go.mod   <- DERIVED. Cannot drift.
ci.yml:176            go-version: '1.25'                 <- hardcoded. The actual risk.
all five modules      go 1.25.12
```

**My regex matched `go-version:` and missed `go-version-file:`.** Same cause as the first instance: an absence
established through ONE encoding of the thing, when the thing had two.

> ### **A WRONG DIRECTION IS WORSE THAN NOT HAVING LOOKED, BECAUSE IT SENDS THE FIX TO THE WRONG FILE.**
> ### **A missing finding costs nothing until someone looks. AN INVERTED ONE SPENDS EFFORT HARDENING THE**
> ### **SIDE THAT WAS ALREADY CORRECT AND LEAVES THE REAL ONE ALONE — and it does so with the confidence**
> ### **of a measurement.**

**AND THE FIX FOLLOWED THE CORRECTED DIRECTION:** `ci.yml` now uses `go-version-file: apps/api/go.mod`,
**removing the hand-maintained copy rather than adding a second one.**

> ### **TWO DERIVED VALUES CANNOT DISAGREE. TWO PINNED VALUES CAN.**
---

## RE-READ THE SURROUNDINGS, NOT THE EDIT — AND IN A DOCUMENT THE STALE HALF IS READ AS TRUTH

**S14.11.** I corrected §0's headline after measuring that four of five "absences" were wrong — and left the
paragraph **immediately beneath it** still asserting *"the columns below are absent because the fields do not
exist."* **The exact claim the correction disproved, sitting directly under the correction.**

**This is the duplicated-DNS-VIP shape** (S14.8: I added a DNS VIP line and left the pre-existing one, so one
address rendered twice) **— same cause, different medium: I verified my edit and not its neighbourhood.**

> ### **THE COST DIFFERS BY MEDIUM, AND THE DOCUMENT VERSION IS WORSE.**
> ### **A duplicated indicator on a screen is VISIBLE — the founder caught the DNS VIP in one look.**
> ### **A stale paragraph in a decisions doc is READ BY A FUTURE SESSION AS TRUTH.**

This epic has already had **two documents contradict each other** — the S14.5 HALT, and PLAN vs the epic doc —
so the failure mode is not hypothetical here.

**THE CHECK IS THE ONE THAT CAUGHT THE DNS VIP:** after correcting a claim, **re-read what surrounds it**, not
the diff. A correction that leaves its own premise standing has not landed; it has only been added to.

**AND IN A DOC, LEAVE THE CORRECTION VISIBLE.** Both the headline and the paragraph now say what they used to
say and why it was wrong — a future reader needs to know the claim was tested, or they will re-derive the
original from the wireframe.

---

## A MUTATION SURVIVOR IS NOT AUTOMATICALLY A MISSING TEST — IT MAY BE A WRONG BEHAVIOUR

**S14.11.** A mutation that swapped the two gate lines in `groupAccessState` **survived**. I read the survivor
the way a survivor is normally read — *my code is right, my test is thin* — and wrote a new test asserting the
order I had written. **The order was the thing that was wrong.**

Measured afterwards, `ListGroups` runs `authorize(PermPolicyView)` and **only then** `if s.policy == nil`, so an
open-edition member's real response is `403 forbidden`. My edition-first version told that member *"Groups are a
Tunnex Enterprise feature"* — **an upsell to someone whose role would not let them see groups after buying
them.** The S14.5 halt shape, forward, in the function whose own comment warns against its reverse.

> ### **HARDENING A TEST AROUND A SURVIVOR PINS THE BUG. The new test is then a second, LOUDER assertion**
> ### **that the defect is correct — and it will outlive the reasoning that produced it.**

**THE CHECK:** a survivor says *"no test distinguishes these two behaviours."* Before writing the test, decide
**which behaviour is right, from the substrate** — the handler, the schema, the wire. Only then pin it. The
survivor tells you where the ambiguity is; it does not tell you which side of it you are on.

**COROLLARY — A MUTATION MUST BE EQUIVALENT TO THE DEFECT IT NAMES.** My first "edition-first" page mutation
swapped only the *condition* and left the branch strings, so for the one caller under test both conditions were
true and it emitted **identical output**. It "survived" by not being the bug. **A mutation that cannot produce
the defect proves nothing about the defect** — and it reads in a report exactly like a real survivor.

---

## A FIXTURE LESS REPRESENTATIVE THAN A TEST DOUBLE HIDES DEFECTS THE DOUBLE FINDS

**S14.11.** `users.name` is `NOT NULL DEFAULT ''` and `acceptInvitation`'s `name` is optional, so **144 of 241
users in the review database have an empty name.** Every seeded demo member had one. The roster cell rendered
`{m.name || m.email}` **and** `{m.email}` unconditionally, printing the address twice for a nameless member —
and **nobody ever saw it**, because the only members ever rendered had names.

It surfaced because a test **mock omitted `name`**, and the first thing I did was try to fix the *test*.

> ### **S14.10's TRAP WAS THE DOUBLE BEING MORE PERMISSIVE THAN THE SUBSTRATE (a label pinned that production**
> ### **cannot produce). THIS IS THE SAME LESSON FROM THE OTHER SIDE — and it is the worse side, because**
> ### **ONLY THE FIXTURE IS REVIEWABLE ON A SCREEN. A founder cannot see what the fixture never produces.**

**THE CHECK:** for each column, ask *what does this field look like for the users who never filled it in?* —
then seed that. Optional-on-write plus `DEFAULT ''` is the signature: a field the SCHEMA calls required and the
POPULATION mostly leaves blank. And when a double disagrees with the fixture, **ask which one production
resembles** before fixing either.

---

## A HARNESS THAT MUTATES SOURCE MUST RESTORE IN A `finally`

**S14.11.** My mutation harness restored the original file on its last line. An assert threw mid-run, so it
**left the source mutated** — and because I ran it twice in one command, the second run took its backup **from
the corrupted file**, destroying the only clean copy of an untracked file.

Damage was one line, and it was named precisely before repair rather than guessed at. But the shape is general:

> ### **A TOOL THAT EDITS THE WORKING TREE AND CLEANS UP ON THE HAPPY PATH IS NOT A TOOL, IT IS A WAGER.**

**AND THE SECOND HALF, WHICH IS THE ONE THAT LIES:** the harness matched each mutation by a text anchor. When an
anchor went stale (the function had been deleted), `str.count() != 1` — so the mutation **never ran**, and a
naive harness counts a never-run mutation as *no failure*, i.e. **as a survivor or, worse, as a pass.**

**THE CHECK:** restore in `finally`; refuse to start if a stale backup exists; and report a stale anchor as
**NEVER APPLIED**, never as a result. *A mutation whose anchor no longer matches is not a mutation that passed.*
Print `applied: N of M` so the count and the list cannot disagree.

---

## A COUNT USED AS A GUARD MUST COUNT WHAT IT GUARDS AGAINST

**S14.11.** `guardLastOwner` protects an org from losing its last owner. Its input:

```sql
SELECT count(*) FROM memberships WHERE org_id = $1 AND role = 'owner';   -- no join to users
```

It counts **owner ROWS**. What it guards against is **an org with nobody who can sign in and administer it** —
and a deactivated user is refused at login (`403 account_deactivated`). The two are not the same set, so the
guard permits exactly the outcome it exists to prevent.

**PROVEN REACHABLE, not read off the code** (`docs/probes/lockout_probe_test.go.txt`): deactivate owner A
(allowed — two owner rows), then deactivate owner B (allowed — still two owner rows). **Two owner rows satisfy
the invariant on paper; zero accounts can sign in and act. Recovery requires direct database access.**

> ### **THIS IS THE DORMANT-MACHINERY LAW INVERTED. That law removes code that is CORRECT AND UNREACHABLE.**
> ### **This is a guard that is REACHABLE AND PERMISSIVE — worse, because it reports success while doing**
> ### **nothing, and every review that sees `guardLastOwner` in the call path reads the invariant as held.**

**THE CHECK:** for every count that gates a decision, write the sentence *"this protects against X"* and then
ask whether the query's row set is X. A guard counting rows while protecting against a capability is the
signature. Ask it of `WHERE role = …` especially: a role is an entitlement on paper, and entitlement is not
the same as ability.

**AND CENSUS THE SHAPE, NOT THE INSTANCE.** Three queries here count over a privilege-bearing role; two are
guards with no status filter, one is a display count that documents why it includes deactivated. The display
count being correct-by-intent is exactly why the census must read each one's PURPOSE — the same SQL shape is a
bug in a guard and correct in a tally.

---

## WRITING A RULE CREATES THE FEELING OF HAVING COMPLIED WITH IT

**S14.11.** §2.6 of this story's own decisions doc reads: *"ADDITIONS GET THE SAME DISCIPLINE AS CUTS — a
silent addition is as hard to audit later as a silent removal."* I wrote that sentence, registered
`email_verified` under it as a deliberate addition — **and then added a `Groups` stat tile that is nowhere in
the wireframe, with no register row, in the same document, in the same story.**

This is the fourth PROSE-VERSUS-BEHAVIOUR instance of the slice and **the sharpest**, because the other three
are a stale summary of someone else's artifact. This one is my own rule about my own code.

> ### **THE RULE IS SALIENT, SO THE MIND SUPPLIES THE COMPLIANCE. Having just articulated the principle**
> ### **feels like having applied it — and the author is the ONE PERSON who cannot read their own rule fresh.**
> ### **A reviewer reading §2.6 cold would have asked "which additions?" and found the tile in one pass.**

**THIS IS WHY THE STANDING QUESTION IS A QUESTION.** *"What in this change is asserted only in prose?"* can be
asked of yourself and returns an answer. *"Follow your own rules"* cannot — **it is already believed**, and a
belief cannot be used as a check on itself.

**THE PRACTICAL FORM:** after writing a rule, apply it to the change containing the rule — the diff that
introduces a discipline is the first place the discipline is unenforced, because it did not exist when the
rest of the diff was written.

---

## AN INFLATED FINDING COSTS THE NEXT ONE ITS CREDIBILITY

**S14.11.** The `CountOwners` probe had two branches. Branch 1 (deactivate one owner, then DEMOTE the other)
ends with zero owners who can sign in — and reads like a lockout. It is not: the demoted owner is now an admin
who still holds `member:manage` and can reactivate the first. **A capability outage with a path back.**

Only branch 2 (deactivate BOTH) is unrecoverable: two owner rows satisfy the invariant on paper, zero accounts
can sign in and act, and recovery requires direct database access.

> ### **REPORTING BRANCH 1 AS A LOCKOUT WOULD HAVE MADE THE FINDING BIGGER AND THE REPORT WORSE. The reader**
> ### **who checks branch 1 and finds a path back now discounts branch 2 — and branch 2 is the real one.**

**THE CHECK:** when a proof has several routes to the same headline, run each to the end and ask *is there a
path back?* Report the narrowest claim the evidence supports, and say explicitly which routes did NOT qualify —
the exclusions are what make the remaining claim load-bearing.

---

## A FALLBACK NEVER EXERCISED DELIBERATELY IS A FALLBACK ALWAYS EXERCISED ACCIDENTALLY

**S14.11.** The Audit Log's actor cell is `{a.actor_id ? actorName(members, a.actor_id) : "system"}`. Its
wiring mock sent **`actor_user_id`** — a field the spec does not have and, being `additionalProperties: false`,
one the server can never send. So `a.actor_id` was `undefined` in **every** audit-log test, **every** row
rendered `"system"`, and the suite was green.

**No assertion ever looked at the actor column.** The ternary had two branches and the tests only ever ran one
— the wrong one — for the entire life of the screen.

> ### **THE MOCK AND THE PAGE DISAGREED, THE TEST PASSED, AND THE PASSING BRANCH WAS THE ONE NOBODY WANTED.**
> ### **A fallback is the branch you expect NOT to take; if no test takes the other one on purpose, the**
> ### **fallback silently becomes the only behaviour you have ever observed.**

**THE CHECK:** for every `?:`, `??`, `||`, and `default:` on a rendering path, name the test that exercises the
**non**-fallback branch. If you cannot, the fallback is your actual UI. This is sharpest on surfaces where the
fallback is *plausible* — `"system"`, `"unknown"`, `"—"` — because a plausible fallback never looks like a bug.

**AND IT COMPOUNDS WITH AN UNFAITHFUL DOUBLE.** The mock was wrong in BOTH directions at once (invented
`actor_user_id`, omitted `actor_id`), which is exactly the pair that produces a green suite: the invented field
is ignored by the page, and the omitted one makes the page take the branch nobody checked.

**MEASURE BEFORE BLAMING THE PAGE.** The live endpoint served a populated `actor_id` on 34 of 78 rows — so the
page was right and only the fixture was wrong. Reading the code alone would have supported either conclusion.

---

## A PER-SCREEN OBLIGATION THAT NOBODY DISCHARGES PER SCREEN IS PROSE

**S14.11, founder-ruled.** *"Each section clears its own em-dashes"* was written down and carried across
sections. **It is not what cleared anything.** The 163→19 burn-down happened in **one global sweep in S14.6**,
while the per-screen passes **preserved** em-dashes because tests asserted on them.

> ### **THE OBLIGATION EXISTED, WAS WRITTEN DOWN, AND WAS NOT THE MECHANISM. Every section reported its**
> ### **pass complete without discharging it, and no check noticed — because the obligation's only**
> ### **enforcement was the sentence stating it.**

Reclassified to a **single global sweep plus an ESLint/CI rule at EPIC 14 close** — the honest form, because a
lint rule is discharged by machinery rather than by intent.

**THE DIAGNOSTIC, and it generalises past em-dashes:** for any standing per-unit obligation, ask **what
actually discharged it last time**. If the answer is "a batch pass someone ran once", it was never per-unit —
it is a global task wearing a per-unit costume, and leaving it in the per-unit definition of done means every
unit reports done while the debt accrues.

**AND NOTE WHICH RULE THIS DOES *NOT* COVER.** The em-dash as a **placeholder glyph** is a separate, already
resolved rule (S14.5: `"—"` → `"n/a"`), and it does **not** wait for the global prose sweep. Collapsing the two
would let a resolved rule ride on an unresolved one's schedule — which is how `Kubernetes.tsx:403` shipped a
regression with a written exemption.

**This is a sibling of the prose-versus-behaviour class:** there the prose asserted a fact the code did not
implement; here the prose asserted a *process* nobody performed. Same failure, one level up.

---

## A GATE CONDITIONED DIFFERENTLY FROM ITS OWN INPUT FAILS ON ABSENCE, NOT ON FINDINGS

**S14.11 follow-up (PR #59).** The CodeQL `go` leg is filtered per-leg: on a diff with no Go files,
`init` / `autobuild` / `analyze` skip. **The step that COUNTS the findings carried no condition at all**, so it
ran anyway and died on `jq: error: Could not open file codeql-results/*.sarif`.

It reported as **`CodeQL (blocking on high/critical) (go): failure`** — which reads exactly like a real
security finding. **That is the worst possible disguise for a plumbing bug**: the one check whose red nobody
argues with.

> ### **IT STAYED INVISIBLE BECAUSE EVERY PR BETWEEN THE SPLIT AND #59 TOUCHED GO. The filter was never**
> ### **exercised on the branch it now takes — the same shape as the audit log's `"system"` fallback, one**
> ### **layer down: a conditional whose other side no run had ever taken.**

**THE CHECK, and it is mechanical:** for every step gated by an `if:`, list the steps that consume its output
and confirm they carry **the same** condition. A producer skipped without its consumer is a guaranteed failure
on the first diff that takes that branch.

**AND DO NOT FIX IT BY MAKING ABSENCE PASS.** The repair adds the condition **and** a real guard: when the
analysis DID run, a missing SARIF now fails loudly (*"CodeQL ran but produced NO SARIF"*). Otherwise the fix
converts a false red into a silent green, which is the trade this project refuses everywhere else — a skip
that reports success is worse than a failure that reports honestly.

**THE COST OF FINDING IT LATE:** this was the FIRST non-Go diff since the `gates` split. Arm 2 of the split
proof was still uncollected — and the moment a genuinely non-Go diff finally arrived, it did not prove the
split worked; **it found a defect the split introduced.** A proof deferred is a defect deferred with it.

---

## A PROOF DEFERRED IS A DEFECT DEFERRED WITH IT

**S14.11 (founder-ruled).** Arm 2 of the `gates`-split proof — *"a web-only diff skips the Go steps"* — was
uncollectable for **four consecutive PRs**, each time for a correct reason: every diff touched Go, so the
non-Go branch was never taken and could not be observed.

**The first diff that finally could take it did not prove the split worked. It found a defect the split had
introduced** — the CodeQL findings-gate reading a SARIF that was never produced.

> ### **THE DEFECT SAT LIVE FOR THE ENTIRE PERIOD THE PROOF WAS PENDING, AND THE TWO WERE THE SAME FACT**
> ### **WEARING DIFFERENT WORDS. "We cannot test this path yet" and "this path is broken" are**
> ### **INDISTINGUISHABLE FROM THE OUTSIDE — by construction, because the only thing that would tell them**
> ### **apart is the test that cannot run.**

**THE CHECK:** when a proof is deferred for lack of a triggering condition, write down **what would be true if
the untested path were already broken** — and notice that the answer is *exactly what you currently observe*.
That is not a reason to panic; it is a reason to stop treating "not yet provable" as "probably fine", and to
weight the eventual collection as **defect-hunting** rather than confirmation.

**A cheap partial substitute exists and was not used here:** the branch could have been *forced* — a scratch
branch touching only a doc, pushed to a throwaway PR, would have taken the non-Go path in minutes. **Waiting
for the condition to arrive naturally is what let four PRs pass.**

---

## A FALSE RED ON A SECURITY CHECK IS WORSE THAN A FALSE RED ANYWHERE ELSE

**S14.11 (founder-ruled).** The plumbing bug above surfaced as
**`CodeQL (blocking on high/critical) (go): failure`** — the exact presentation of a real high-severity
finding. Nothing in the check's name, status, or summary distinguished a missing file from a vulnerability.

> ### **THE FIRST REFLEX IS TO TRUST A SECURITY RED. THE SECOND REFLEX, AFTER IT HAS BEEN WRONG ONCE, IS TO**
> ### **STOP READING IT.** A security check is the one gate whose red nobody argues with — which is exactly
> ### **why a false one there costs more than a false one anywhere else. It spends credibility that the next,**
> ### **real finding needs.

**AND THE REPAIR MUST NOT CONVERT A FALSE RED INTO A SILENT GREEN.** The obvious fix — make a missing SARIF
pass — would have removed the noise by removing the check. The fix applied instead:

1. the counting step now carries **the same condition as the analysis producing its input**, so on a non-Go
   diff it is **skipped**, not passed; and
2. when the analysis **did** run and produced nothing, it fails **loudly** — *"CodeQL ran but produced NO
   SARIF"* — because a scan that analysed nothing is a real failure.

**Skipped and passed must never render alike** — the classifier's own notice says it (`false = the Go steps
below are SKIPPED, not passed`), and this is that rule applied to the check's own internals.

---

## ANY SCRIPT VALIDATED ONLY AGAINST ACCUMULATED STATE IS VALIDATED AGAINST SOMETHING NO NEW CUSTOMER HAS

**S14.12 (founder-ruled; the open-edition stack's strongest justification, and it was NOT predicted).**

Building the open-edition review stack produced a **fresh database** for the first time in months. `make
seed-open` failed immediately:

```
insert or update on table "policy_rules" violates foreign key constraint
"policy_rules_src_user_fk" (SQLSTATE 23503)
```

`policy_rules` is inserted at line 341 of `fixtures.sql`; the users those rules reference are inserted at line
384. **The ordering has been wrong for as long as those rules have existed.** It never failed because the
primary database is months old and the referenced users were already there from earlier seeds.

> ### **THE PRIMARY STACK VALIDATED THE SEED AGAINST STATE THE SEED ITSELF DID NOT CREATE. A FIRST-RUN**
> ### **CUSTOMER GETS THE FRESH PATH — AND THE FRESH PATH WAS BROKEN.**

**GENERALISE PAST FIXTURES.** Migrations are covered: CI runs them forward from empty every time, so an
ordering defect there fails immediately. **Nothing else in this repo has that property by default** — seeds,
backfills, and any ordering-sensitive script may only ever have run against a database that already contains
what they assume.

**THE DIAGNOSTIC:** for any script that writes, ask *when did this last run against an EMPTY target?* If the
answer is "never" or "not since it was written", it has been tested against its own side effects.

**REGISTERED, NOT CHASED:** *what else here has only ever run against an accumulated database?*
Trigger — **the next data-path story** (`docs/DEFERRAL-REGISTER.md`).

**AND NOTE WHICH FINDING THIS OUTRANKS.** The same stack also confirmed the predicted `accessView` gate-order
bug. **That one was a code reading first and a measurement second; this one nobody predicted at all.** A tool
that only confirms what you already suspected has not yet earned its cost.

---

## TWO DATABASES THAT DIFFER ARE DRIFTED OR CORRECTLY DIFFERENT, AND ONLY A WRITTEN NOTE DECIDES WHICH

**S14.12 (founder-ruled).** The enterprise and open review stacks seed from one `fixtures.sql` through one
seeder binary, and they still differ: `health_blocked` is **1** on enterprise and **0** on open, because the
seeder registers posture **through the product** and device-health reporting is edition-gated.

**That difference is correct.** But *correct* and *drifted* look identical in a diff — a number that does not
match, with no property of the data itself saying which it is.

> ### **THE ONLY THING THAT DISTINGUISHES A LEGITIMATE DIFFERENCE FROM DRIFT IS THAT SOMEONE WROTE DOWN**
> ### **WHICH ONE IT IS, BEFORE ANYONE HAD TO ASK.**

**THE PRACTICE:** when standing up a second environment, enumerate the expected differences **at creation**
and put them where the comparison happens — here, in the `seed-open` target itself. An unexplained difference
found later is investigated from zero; an explained one is checked against its explanation in seconds. And a
difference nobody wrote down eventually gets "fixed" by someone making the two match, which is how an edition
gate quietly stops being tested.

---

## THE ARTIFACT OUTLIVES THE SOURCE THAT PRODUCED IT — AND KEEPS ANSWERING

**S14.12 (founder-ruled). THIRD instance of one family, now named.**

`make up-open-review` was committed on a branch **after that branch merged**, so the target existed on no
branch anyone would check out. The `:8081` containers kept running the whole time — so the open-edition stack
**looked present and healthy** while its definition was gone from every tree, and the bundle it served
predated a day of work.

**THE SIBLINGS, and they are the same failure:**

| instance | the artifact | what it outlived |
|---|---|---|
| S14.11 | the **served bundle** | eight commits that were never deployed |
| S14.11 | **"CI green"** | the sha it was green on, superseded by a push |
| S14.12 | a **running container** | the Makefile target that defines it |

> ### **AN ARTIFACT KEEPS ANSWERING AFTER THE THING THAT DEFINES IT HAS MOVED OR VANISHED, AND THE ANSWER**
> ### **LOOKS CURRENT PRECISELY BECAUSE THE ARTIFACT IS STILL RUNNING. Health is not freshness. A green**
> ### **check, a serving port and a healthy container all report on a PAST state with a PRESENT voice.**

**THE DIAGNOSTIC, cheap enough to always run:** *before trusting a running thing, confirm its DEFINITION is on
the branch you are on.* `grep` the target in the checked-out `Makefile`; diff the served bundle hash after a
deploy; re-read check-runs for **the exact sha** rather than the PR. **All three instances would have been
caught by that one question**, and each was instead caught by luck or by a later failure.

---

## A CONSEQUENCE ASSERTED WITHOUT ITS PRECONDITION

**S14.12 (founder-ruled — and the finding of the slice, though it is not what was ruled).**

The empty rule list rendered: *"No rules — under Enforcing, all device-to-device traffic is denied."*
**Unconditionally.** The demo org's mode is `off`, and with enforcement off an empty rule set denies
**nothing**. The sentence was true of a state the screen was not in.

**It was invisible because the fixture had rules**, so the branch never rendered. **One deletion away, the
screen makes a false claim about enforcement** — on the surface whose entire job is stating the enforcement
posture.

> ### **THE SENTENCE NAMED A CONSEQUENCE ("all traffic is denied") AND OMITTED ITS PRECONDITION ("while**
> ### **enforcing"). A conditional truth rendered unconditionally is FALSE HALF THE TIME, and the half it is**
> ### **false in is invisible whenever the fixture keeps you out of it.**

**HOW IT WAS FOUND, and this is the transferable part: by being asked a DIFFERENT question.** The ruling was
about two empty states — *failed* vs *zero-while-enforcing*. Building that distinction properly forced reading
the mode, and the third claim fell out. **Neither the founder nor I was looking for it.**

**THE CHECK:** for every rendered sentence asserting a consequence, name the state that makes it true and ask
whether the render is conditioned on that state. If the copy contains *"under X"*, *"while X"*, *"since X"* —
X must be in the branch condition, not only in the prose.

---

## A GUARD THAT VALIDATES ONE COPY OF A DUPLICATED RULE CERTIFIES THE COPY, NOT THE RULE

**S14.12 (founder-ruled — the session's result). IT COMPOUNDS THREE WAYS, and each alone would have been
survivable.**

**1 — THE RULE WAS DUPLICATED WITH NOTHING LINKING THE COPIES.** The diff classifier lives in `ci.yml` **and**
in `security.yml`. Adding `\.sql$` to one left the other behind; nothing in the repo related them.

**2 — THE GUARD BUILT TO PREVENT EXACTLY THIS CLASS READ ONLY ONE COPY, AND PASSED.**
`TestClassifierPatternMatchesTheWorkflow` was written *because* a transcribed pattern can drift from the
workflow. It opened `ci.yml`, found agreement, and reported green — **certifying the artifact I had already
fixed while the divergent one went unexamined.**

**3 — THE FAILURE MODE WAS `skipped`, NOT `failed`.** Three security jobs — **`govulncheck` ×5 modules,
`gofmt + vet parity`, `Trivy`** — did not run on a diff containing a Go compile input. **Every badge was
green.** A skipped security job is indistinguishable at a glance from "not applicable to this diff", which is
the *normal* reason a job skips.

> ### **A GREEN BOARD WITH THREE ABSENT SECURITY JOBS LOOKS EXACTLY LIKE A GREEN BOARD.**

**THE FIX ASSERTS THE RULE, NOT A COPY:** both workflows must carry the **identical** pattern, and the guard
loops over both files. **Proven to fire on the sibling** — reverting `security.yml` alone reds it by name.

**THE GENERAL CHECK:** when a guard validates a duplicated value, its assertion must range over **every**
instance, and the loop must be **derived** (a list of files) rather than written once per instance — because a
guard extended by hand acquires the same drift it exists to prevent.

**AND THE SHAPE THAT HID IT — the third `skipped`-vs-`passed` instance this epic.** The classifier's own
notice says it (`false = the Go steps below are SKIPPED, not passed`); CodeQL's counting step said it; and now
a whole job set. **Every time, the thing that made it invisible was that skipping is also the correct
behaviour most of the time.**

---

## A COMPOSITE RESULT REPORTED BY ITS MOST FAVOURABLE COMPONENT

**S14.12 (founder-ruled). TWO INSTANCES IN ONE SESSION, one in each direction.**

**A GATE READ SELECTIVELY IS NOT A GATE.** I ran the web gate, read *"597 tests passed"*, and pushed. Two
lines above it sat **`typecheck: 2`**. The gate is typecheck + tests + build; I reported the leg that agreed
with me.

**THE SIBLING, same session, same shape:** CI's board showed every badge green while `govulncheck` ×5,
`gofmt + vet parity` and `Trivy` **never ran** — a green *badge* hiding absent jobs, where the other was a
green *test count* hiding a failing leg.

> ### **BOTH TIMES THE UNFAVOURABLE PART WAS VISIBLE, ADJACENT, AND UNREAD. Nothing was hidden; the**
> ### **aggregate was simply allowed to speak for its parts, and an aggregate always speaks in the voice of**
> ### **whichever part you looked at.**

**THE CHECK:** for any composite gate, report **every leg by name and value** — never the aggregate, never the
one leg you happened to read.

> ### **"make web-gate green" IS NOT A REPORT. "typecheck 0, 597 tests, build clean" IS.**

Same for CI: not *"CI green"* but **`gates: success` (14/14 steps), `client (macos)`: success, `client
(windows)`: success, `govulncheck` ×5: RAN and passed** — because *ran* and *passed* are different claims and
a skip reports as neither.

---

## THE RUNNING IS THE RESULT; THE GREEN IS INCIDENTAL

**S14.12 (founder-ruled).** The classifier fix was proven not by a passing board but by a **transition**:

```
sha 8522614   govulncheck x5, gofmt+vet parity, Trivy   ->  SKIPPED
sha 129e784   the same seven jobs, same class of diff   ->  SUCCESS
```

> ### **A JOB THAT PASSES PROVES SOMETHING ABOUT THE CODE. A JOB THAT RUNS WHERE IT PREVIOUSLY SKIPPED**
> ### **PROVES SOMETHING ABOUT THE GATE — AND THE GATE WAS THE THING UNDER TEST.**

Had those seven jobs simply been green on both shas, nothing would have been demonstrated: green is what a
skipped job's absence looks like on a board. **The evidence was the change in `conclusion`, not its value.**

**THE CHECK:** when the thing you fixed is a GATE, state the before and after **per job by name**. "CI is
green" is compatible with the gate being broken in exactly the way you were fixing — which is how the defect
survived four PRs in the first place.

**SIBLING:** *a composite result reported by its most favourable component*. Same session, same root: an
aggregate cannot report on whether its parts ran.

---

## A VIEW-MODEL WITH GREEN TESTS AND NO CALLER IS INVISIBLE TO EVERY GATE WE OWN

**S14.12 (founder-ruled). SECOND dormant-machinery catch this epic, and the two were found differently — which
is the point.**

**FIRST (S14.11):** the founder asked why a primitive had no consumer. **A person noticed.**
**SECOND (S14.12):** `flowGraphState` / `flowGraphNote` were built, tested, and **mutation-proven** — 4 tests,
4 mutations, zero survivors — and referenced **nowhere**. I found it by grepping the SERVED ARTIFACT for
`"Too many rules to draw legibly"` and getting **0**.

> ### **EVERY GATE WE OWN TESTS THE VIEW-MODEL. So a view-model that is correct, covered and uncalled passes**
> ### **all of them — unit tests, mutations, typecheck, build. There is no gate whose subject is "is this**
> ### **reachable from the page", because reachability is exactly what the tests supply themselves.**

**THE CHECK, and it is cheap:** after building a view-model, **grep the ARTIFACT for a string only its
consumer can produce.**

> ### **TREE-SHAKING IS THE TELL — IF THE BUNDLER DROPPED IT, NOTHING CALLS IT.** The bundler already performs
> ### the reachability analysis no test performs; read its output instead of duplicating it.

**AND WHY WIRING IT IMMEDIATELY MATTERS:** an unwired view-model *between slices* is how dormant machinery
becomes permanent. **It passes its tests, so nothing ever complains** — the debt has no failing signal and no
deadline. The catch is only worth having if the wiring follows in the same slice.

### ⛔ THIRD INSTANCE (S14.12) — GSAP, AGAIN, AND I ARGUED THE WRONG REASON

Asked to "use gsap animation", I answered with **bundle size, motion-gate coverage, and reduced-motion
ergonomics** — all correct, and **none of them the reason.** GSAP was ruled out on **2026-08-01 on LICENCE**:
`docs/EPIC-14-ui-redesign.md:96` — a custom *"no charge"* licence, **neither SPDX nor OSI**, and Tunnex
**redistributes a built bundle to self-hosters**, so embedding a non-OSI dependency inside an Apache-2.0
artifact denies the recipient, for that portion, the freedoms the surrounding licence advertises. The ruled
alternative is **Motion (MIT)**.

**The heading literally says `GSAP IS NOT ADOPTED`. One grep.**

**INSTANCE COUNT THIS EPIC: THREE** — Fleet risk, GSAP, and GSAP again.

> ### **BEING RIGHT FOR A WEAKER REASON IS HOW A RULING GETS RE-OPENED A FOURTH TIME. A licence finding**
> ### **closes the question permanently; a bundle-size argument invites "but it's only 70kB" — and the**
> ### **next person to ask will get my weaker answer, not the founder's stronger one, because mine is the**
> ### **one now written in the conversation.**

**AND NOTE WHAT MADE IT FEEL UNNECESSARY:** I *had* the correct conclusion (don't add it) and a confident
argument for it. **The grep feels redundant precisely when you already agree with the ruling** — which is
exactly when it is load-bearing, because agreeing is not the same as knowing why.

---

## A THRESHOLD SHOULD MEASURE THE PROPERTY THAT MATTERS, NOT A PROXY FOR IT

**S14.12 (founder-ruled — and the founder's own question was the proxy).**

The question asked was *"at what rule count does the flow panel stop saying anything?"* **N was the wrong
variable.** Degree-ranking's usefulness depends on the **degree distribution**, not the count:

| same N | top-4 covers | verdict |
|---|---|---|
| 900 rules hub-and-spoke through 4 gateways | nearly everything | **a good summary** |
| 900 rules across 900 distinct pairs | ~2% | **decoration** |

**A fixed second count would have withheld from a well-summarised org for a property it does not have, and
kept drawing for a badly-summarised one until somebody noticed.** So the threshold measures **coverage** —
withhold when the drawn share falls below half — because coverage *is* the property, and count merely
correlates with it in the cases that first come to mind.

> ### **THE PROOF SHAPE IS THE SHARPEST PART: SAME N, OPPOSITE VERDICT.** Twenty rules hub-and-spoke draws;
> ### **twenty rules fully distinct withholds. A test that VARIES THE THING THE THRESHOLD CLAIMS TO MEASURE**
> ### **while HOLDING THE THING IT DOES NOT** is the only test that distinguishes a real threshold from a
> ### proxy — and it fails loudly the day someone "simplifies" it back to a count.

**THE CHECK:** for any threshold, write the sentence *"this withholds when X"* and ask whether the quantity in
the code **is** X or merely tracks it. If it tracks it, find the input where they diverge — that input is your
test, and it is usually easy to construct once you look for it.

**THE SIBLING, one level down:** the class-token regex in the same slice. `max-w-full` **contains** `w-full`,
so matching the substring reported correct code as broken. **Same error at a smaller scale: the thing measured
was a proxy (does the string appear) for the thing that mattered (is the class token present).**

### ⛔ AND THE DIRECTION IS WHAT COSTS — SECOND INSTANCE OF A RULE ALREADY FILED

`docs/laws.md` already records *a wrong direction is worse than not having looked, because it sends the fix to
the wrong file* (the `go-version` inversion). **This is its second instance, and it names the cheaper half:**

> ### **A MATCHER THAT IS WRONG IN THE FALSE-POSITIVE DIRECTION SENDS YOU TO FIX SOMETHING THAT IS NOT**
> ### **BROKEN.** A false negative costs you a finding. A **false positive costs you a change** — and the
> ### change lands on correct code, with a test now pinning the damage.

Here it would have removed `max-w-full`, which is the very thing keeping the panel from overflowing a narrow
viewport. **The "fix" would have introduced the defect the assertion exists to prevent.**

**BOTH INSTANCES WERE CAUGHT THE SAME WAY: by checking the finding instead of acting on it.** The `go-version`
inversion was caught by reading the file the claim was about; this one by reading the class string the regex
had judged. **Neither was caught by a gate, and neither would have been** — a matcher that is confidently
wrong produces a clean, specific, actionable red.

**THE CHECK:** when an assertion goes red on code you did not just change, read the subject before you read
the fix. The first question is *"is this red correct?"*, not *"how do I make it green?"*

---

## AN ANIMATION AND A SEMANTIC ENCODING MUST NOT SHARE A PROPERTY

**S14.12 (founder-found on screen).** The flow panel encodes *temporary grant* as **`stroke-dasharray: "5 6"`**
and its legend says `- - - temporary`. The entry animation drew each edge with
`stroke-dasharray: 1600; animation: tnxDraw …` on the same element.

**A CSS declaration beats an SVG PRESENTATION ATTRIBUTE.** So the animation's dasharray silently overrode the
semantic one and **every temporary edge rendered SOLID, while the legend promised a dash.**

> ### **WHICHEVER THE CASCADE FAVOURS WINS, AND THE LOSER FAILS SILENTLY. The edge still drew, at the right**
> ### **width, in the right colour, along the right path — just carrying the wrong meaning. Nothing looked**
> ### **broken, which is why it survived a review pass that caught a wrong type tag.**

**THE FIX IS NOT PRECEDENCE, IT IS SEPARATION.** The reveal now uses **`clip-path`**, which nothing on this
panel encodes, applied to the edge `<g>` so the flow wipes in once. Dash is free to mean what the legend says.

**THE CHECK:** before animating an SVG or CSS property, ask **what else on this surface reads that property as
meaning**. `stroke-dasharray`, `opacity`, `stroke-width` and colour are all commonly BOTH decorative and
semantic — and an animation is written in CSS while the meaning is usually written as an attribute, so the
animation wins by default.

**SIBLING — the same defect one layer up:** the epic already rules that **a gate must be a RENDER decision,
never a style**, because a column hidden by `opacity` is still in the DOM. Here a *meaning* was hidden by an
animation. **Both are: a presentational mechanism silently overriding a semantic one.**

---

## RUN A SWEEP AGAINST A CASE YOU ALREADY KNOW THE ANSWER TO

**S14.12 (founder-ruled — the most reusable thing in that report).**

The first "which mutating endpoints have no web call site" sweep reported **6**. `addGroupMember` — which I had
*already measured* as uncalled ten minutes earlier — **was not in the list.** That absence is what caught it.

**THE PROXY, and it is the worst kind:** the sweep asked *"does this path string appear anywhere in
`apps/web/src`?"* But `edition.ts` is a **PATH MANIFEST** — it lists every enterprise-gated path so the
reactive-403 layer can classify them.

> ### **SO THE PROXY WAS CORRECT-BY-CONSTRUCTION FOR THE WRONG QUESTION. Every enterprise path was**
> ### **guaranteed to match, called or not. A proxy that fails randomly gets noticed; one that is**
> ### **structurally guaranteed to agree looks like a clean result.**

Re-run against **actual call sites** (`api.POST|PUT|PATCH|DELETE("…")`, manifest excluded): **19 of 80**, and
the known case was present.

> ### **THE FLOOR: SEED EVERY ENUMERATION WITH A CASE WHOSE ANSWER YOU ALREADY KNOW. If the known case is**
> ### **MISSING FROM THE OUTPUT, THE SWEEP IS MEASURING SOMETHING ELSE — and you learn that in one glance,**
> ### **before the number reaches anyone.**

This is the **vacuity floor for enumerations**, the sibling of the count floors already on the census tests
(`gated < 40`, `files > 50`, `found < 2`). Those catch a scan that broke; **this catches a scan that works
perfectly on the wrong question.**

---

## THE WHO-READS-THIS PROBE, FAILING ON A **VERB**

**S14.12 (founder-ruled).** Every prior instance was a served **FIELD** nobody rendered — `actor_system`, the
peer count, `Histogram`. This one is **three shipped ENDPOINTS with one consumer**, since S7.5.2:
`listGroupMembers` is called; **`addGroupMember` and `removeGroupMember` never have been.**

> ### **A FIELD WITH NO READER SHOWS NOTHING. A VERB WITH NO CALLER MEANS A CAPABILITY THE PRODUCT HAS AND**
> ### **THE OPERATOR CANNOT REACH — an incomplete view versus an unusable feature.**

And it compounds: the Access screen lets an admin create a group and write rules that *use* it as a source,
while the surface to put anyone in it does not exist. **The form creates an object nobody can populate, above
rules that depend on it being populated.**

**THE DIAGNOSTIC:** for every mutating endpoint in the spec, **name its call site.** An endpoint with no
caller is either **dead** or **missing a surface**, and nothing distinguishes those without asking — so the
question has to be asked per endpoint, not inferred from the count.

**MEASURED SIZE (S14.12): 19 of 80 mutating operations have no web call site; 12 are genuinely unreachable
capability.** Registered as its own story, not folded.

---

## A MUTATION THAT FAILS TO APPLY PROVES NOTHING — AND LOOKS EXACTLY LIKE ONE THAT WAS CAUGHT

**S14.12 (founder-filed on my own output).** Proving the new `empty_group_members` census, I twice inserted a
row to break the state, re-seeded, and read the verdict. Both proofs "passed."

**Both inserts had failed.** `group_members.org_id` is `NOT NULL` and I omitted it:

```
ERROR: null value in column "org_id" of relation "group_members" violates not-null constraint
```

**The state was never broken, so the census never saw a non-zero.** I had proved that a guard reports success
when nothing is wrong — which is not a property worth having.

> ### **A FAILED MUTATION AND A CAUGHT MUTATION PRODUCE THE SAME FINAL READING. The guard says "fine" either**
> ### **way, and the error scrolls past above it — in my case printed on the very line before the result I**
> ### **then reported.**

**THE CHECK, and it is one extra read:** after applying a mutation and before reading the guard's verdict,
**confirm the mutation actually changed the subject.** Count the rows. Diff the file. Assert the broken state
exists. Only then ask the guard.

Re-run with `org_id` supplied: the insert took (`INSERT 0 1`, members `1`), the seed **restored** it to `0`,
and with the restore removed the census **rejected** — `seed_fixtures_incomplete`, `interns_members: 1`,
`will NOT render`. **That is the proof; the first two were theatre.**

**SIBLING:** the S14.11 wedge test that failed identically with and without the fix (a nil map, not the
defect). Same family — *a red for the wrong reason* and *a green for the wrong reason* are the same error with
opposite signs, and both are caught by asking what the test's subject was actually in.

### ⛔ AND IT IS THE COMPOSITE-BY-FAVOURABLE-COMPONENT LAW, IN **TIME** RATHER THAN IN SPACE

The typecheck slip put `typecheck: 2` two lines **above** the test count I reported. Here the discriminating
evidence — the `null value in column "org_id"` error — was one **scroll back**, printed on the line
immediately before the verdict I read.

> ### **SAME LAW, DIFFERENT AXIS. There the unfavourable part was ADJACENT IN THE OUTPUT; here it was**
> ### **ADJACENT IN TIME AND ALREADY GONE FROM ATTENTION. Both times nothing was hidden, and both times the**
> ### **aggregate was allowed to speak for a part it had not checked.**

**THE MECHANICAL CHECK — and it is not "did the command run":**

> ### **BEFORE READING A GUARD'S VERDICT, CONFIRM THE MUTATION CHANGED THE SUBJECT.**

Not that the command exited. Not that it printed. **That the subject is now in the state the guard is supposed
to catch.** Count the rows, diff the file, re-read the value.

**FOURTH INSTANCE OF A CHECK REPORTING ON A STATE IT NEVER REACHED**, and the family is now worth stating
whole: *run the command the gate runs* (not one that resembles it) · *output is not effect* (a command that
prints is not a command that changed something) · *a mutation whose anchor no longer matches never ran* ·
and now *a mutation that failed to apply proves nothing.* **Every one is a proof about a subject the proof
never touched.**

---

# ⛔ A CORRECTLY-RUN CHECK AIMED AT THE WRONG SUBJECT

**S14.13. A NEW VARIANT, and the sharper half of the stale-stack finding.**

The panels were built, committed, CI-green — and invisible on both review stacks. The cause was mundane
(containers built 08:34, branch point 08:59, first story commit 09:21; the rebuild was never run). **What
matters is why it survived a check that was specifically supposed to catch it.**

The artifact law was obeyed. The grep ran. It searched `apps/web/dist/assets/index-*.js` for five strings only
the new consumers can produce, found all five, and was reported as proof the panels reach the artifact.

**It was proof they reach AN artifact. The one nobody was looking at.**

> ## **THE THREE PRIOR INSTANCES WERE CHECKS THAT COULD NOT SEE. THIS ONE SAW PERFECTLY, AND THE SUBJECT WAS**
> ## **WRONG. Different failure, same family — and strictly more dangerous, because a check that cannot see**
> ## **often looks wrong, while a correctly-run check aimed at the wrong subject LOOKS LIKE EVIDENCE. That is**
> ## **exactly why it survives review: nothing about it is malformed.**

**AND THE FIX IS NOT TO STOP RUNNING IT.** The `dist` grep still does its real job — tree-shaking and
no-caller detection, the "view-model with green tests and no caller" law. It was simply never capable of
answering *can the reviewer see it*. **The repair is to say WHICH QUESTION EACH GREP ANSWERS**, because both
greps are correct and they answer different things:

| check | subject | question it answers |
|---|---|---|
| **hash changed** | the port | **DEPLOY** — is the served artifact new at all? |
| **strings present in the SERVED bundle** | the port | **REACH** — did this code get into what is being served? |
| strings present in `dist` | the local build | **LINKAGE** — does this code have a caller, or did tree-shaking drop it? |

**THE MECHANICAL RULE — added to the handoff:**

> ## **BEFORE A REVIEW, GREP THE SERVED BUNDLE, NOT `dist`.**
> The hash changing is the **deploy** proof; the served strings are the **reach** proof. Two different checks,
> both against the same artifact — **the one on the port.**

**Generalized, this is the family's fifth member and it names the axis the others share:** *run the command
the gate runs* · *output is not effect* · *an unmatched anchor never ran* · *a mutation that failed to apply
proves nothing* · **and now: a check is only as good as the subject it was pointed at.** Every one is a proof
about a subject the proof never touched — but this is the first where the proof itself was flawless.

---

# ⛔ A DIFFERENCE IN SYMPTOM DOES NOT IMPLY A DIFFERENCE IN CAUSE

**S14.13, from the founder's review stack.** Google's OAuth client-ID field was prefilled with the signed-in
admin's **email** and the secret field with a **saved password**, both in autofill-blue, on a credential
surface. Microsoft's fields, on the same screen, were clean.

**The reasonable inference — and it was wrong:** *the two must be marked up differently; find the difference.*

**MEASURED: the fields are BYTE-IDENTICAL.** `SsoProvider` renders once per provider from ONE component, so
there is no markup difference to find. Chrome fills the **first** text+password pair on a page as a login form
and fills **one pair per page**; `google` is first in `PROVIDERS`. **Microsoft looked immune because it was
second.**

> ## **TWO INSTANCES OF ONE COMPONENT BEHAVED DIFFERENTLY BECAUSE OF POSITION — AND POSITION IS INVISIBLE**
> ## **FROM THE MARKUP.** The variable was not in either instance; it was in the ORDER OF THE LIST that
> ## **renders them, one file away.**

**AND THE COST OF THE WRONG INFERENCE IS NOT A WASTED HOUR — IT IS A FIX THAT MOVES THE BUG.** Annotating only
the provider that visibly misbehaved would have left the defect intact and handed it to Microsoft the first
time anyone reordered `PROVIDERS`. **The symptom would have relocated and the diff would have looked like a
fix.**

**THE MECHANICAL CHECK:**

> ### **WHEN TWO INSTANCES OF THE SAME COMPONENT BEHAVE DIFFERENTLY, DIFF THE INSTANCES FIRST. IF THEY ARE**
> ### **IDENTICAL, THE CAUSE IS THEIR CONTEXT — ORDER, POSITION, WHAT RENDERED BEFORE THEM — NOT THEIR CODE.**

**And one instance is not a scope.** The founder reported one field on one provider; the CENSUS turned that
into the real finding — **five password inputs across the app, ZERO `autocomplete` attributes.** The reported
symptom was the least of it.

## ⛔ AND THE VACUITY FLOOR EARNED ITSELF ON ITS FIRST RUN

The census matcher used `<Input\b([^>]*?)\/>` and found **ZERO password inputs in a tree containing five** —
because **JSX arrow functions contain `>`** (`onChange={(e) => …}`), so the exclusion class stopped at the
first `=>`.

> ### **A CENSUS WHOSE MATCHER IS DEFEATED BY THE LANGUAGE IT SCANS REPORTS A CLEAN TREE.**

Without the floor (`expect(total).toBeGreaterThanOrEqual(5)`) every assertion below it would have passed
against an empty set, and the credential guard would have shipped green and blind.

---

# ⛔ MUTATIONS DETECT VACUOUS TEST *RUNS*, NOT ONLY WEAK ASSERTIONS

**S14.13.** A new Go test was written, run, and reported `ok`. **It had SKIPPED** —
`set TUNNEX_TEST_DATABASE_URL to run this integration test` — and `go test` reports a skipped package as `ok`.

Nothing in the output was false. Nothing was hidden. **It was caught only because ALL FIVE Go mutations
survived**, which is impossible for a test that is actually asserting.

> ## **THE MUTATION SWEEP WAS THE INSTRUMENT, NOT A CAREFUL READING OF THE OUTPUT.** *Ran and passed are
> ## **different claims* was already law; this is the instance where the sweep — not vigilance — is what
> ## separated them.

**So the sweep has a second job, and it is the more valuable one:** a weak assertion lets *some* mutations
survive; **a test that never executed lets ALL of them survive.** A 100% survival rate is not a verdict about
the assertions — **it is a verdict about whether the test ran at all**, and it should be read that way before
a single assertion is blamed.

**Corollary for the harness:** report survivors as a RATE, not a list. `5 of 5 survived` is a different
diagnosis from `2 of 5 survived`, and only the first one means *go check that the test executes*.

---

# ⛔ THE SPEC DESCRIBES THE SHAPE OF A REQUEST, NOT THE EXISTENCE OF A CAPABILITY

**S14.14.** Every idp-sync path enumerates `provider: [microsoft, google]`; the server answers Google with
`400 provider_not_supported`, deliberately, at config time. **The spec, the handler signature and the
generated schema all read as though Google works — only the served payload disagreed.** Build the arm for
what the server ANSWERS, not for what the contract permits you to ask.

---

# ⛔ A CUMULATIVE DIFF MAKES ANY CLASSIFIER STICKY

**S14.15.** CI classified `BASE...HEAD` — the whole PR — so **the first commit that trips a rule trips it for
every commit after it.** One edit to `fixtures.sql` pinned `go=true` for the rest of the branch: across 18
runs, 17 were `go=true` and 16 of those were that one file, including 10 commits that were docs-only. **The
classifier existed and was a constant.**

**The shape will recur wherever a per-push decision is computed from a cumulative range** — cost gates, path
filters, "did anything security-relevant change". Ask *what is the earliest commit that can trip this, and
does it then trip forever?*

**TWO THINGS THAT ARE NOT THE FIX, recorded so they are not tried:**

- **Do NOT switch to a per-commit diff.** The PR as a whole is what gets merged, so per-commit classification
  would skip a leg for a change that IS in the merge. Cumulative is correct; stickiness is its cost.
- **Do NOT read the skip as a bigger win than it is.** `docs_only` cannot fire on a story branch that already
  contains code — so end-of-story documentation pushes are covered by the narrowed `go=false` path, **not** by
  the docs-only skip. Stating that plainly is the difference between a measured result and a claim.

**And `e2e` stays on every push.** It is parallel, sits under the post-fix floor, and deferring integration
proof to merge time finds breaks at the most expensive moment.

---

# ⛔ WHEN RE-TESTING AN OLD FAILURE, PIN THE TREE IT WAS WRITTEN AGAINST

**S14.15.** Two reds written at S14.13 were re-measured at S14.15 and appeared to reproduce in isolation,
refuting three sessions of "it is cumulative". **The refutation was false.** The tree had since gained
S14.14's directory-sync panel, which renders *"status unknown"* once per provider — so a singular query now
matched **twice** and threw. **A different failure, with a different message, standing exactly where the old
one had been.**

Suppressing only the new panel restored the truth: alone it **passes**, in the full file it **fails with the
original errors**. Cumulative after all.

> ## **CHANGING THE SUBJECT AND THE INSTRUMENT IN THE SAME STEP IS NOT RE-MEASURING. A failure re-tested on a**
> ## **tree that has moved is a NEW experiment wearing the old one's name — and it is most convincing exactly**
> ## **when the new symptom lands in the same place as the old.**

**The cost was not the wasted measurement.** The false refutation was used to argue an ORDERING decision to
the founder. *A wrong direction is worse than not having looked* — with a decision attached to it.

**MECHANICAL:** before re-running an old red, either check out the commit it was written against, or suppress
everything that has landed on that surface since. Then compare the ERROR TEXT, not just pass/fail: a changed
message means a changed defect.

---

# ⛔ `needs:` FOR AN OUTPUT IS ALSO A GATE ON SUCCESS

**S14.15.** `e2e`, `e2e-enterprise` and `visual` were given `needs: gates` **solely to read
`needs.gates.outputs.docs_only`**. That silently added a success condition: when `gates` went red, all three
reported **`skipped`** — so the integration signal disappeared **exactly when something was broken**, which is
when it is worth most. Observed live, not predicted.

> ### **WHEN YOU WANT THE VALUE AND NOT THE CONDITION, WRITE `if: always() && …`.**

**Same family as *a skipped job reads as not-applicable*:** a check that reported **nothing** looked like a
check that **found** nothing. The earlier instance was a security job skipped by a classifier; this one is a
dependency added for an unrelated reason. **Both times the absence of a result was mistaken for a result.**

## ⛔ A COMPILE ERROR IS A MUTATION THAT NEVER APPLIED, WEARING A PASS'S CLOTHING

A mutation that swapped a recorded count to `0` orphaned a variable, so the package failed to **compile** and
the harness scored it *caught*. It proved nothing. **Re-run with the variable kept alive, it caught on the
assertion.** Belongs with *a mutation that failed to apply proves nothing* — the new part is that a BUILD
failure is one of the disguises, and it is the most convincing one because the exit code is identical.

## ⛔ AND MY MATCHERS KEEP ASSUMING A CANONICAL RENDERING THE PRODUCER NEVER PROMISED

**Second instance in two stories.** First: `<Input\b([^>]*?)\/>` found ZERO inputs in a tree of five, because
JSX arrow functions contain `>`. Second: `"members_removed":2` failed against a correct row, because postgres
renders jsonb **with a space after the colon**.

> ### **A MATCHER OVER A FORMATTED ARTIFACT IS A GUESS ABOUT THE FORMATTER.** Query the STRUCTURE — parse the
> ### JSON, read the attribute, ask the database — rather than pattern-matching its printed form.

Both were caught only because the surrounding check had a floor or a known-answer case. **A matcher with
neither would have reported a clean tree and been believed.**

---

# ⛔ A NUMBER QUOTED FROM AN OLD REGISTRATION OUTLIVES THE THING IT COUNTED

**EPIC 14, found 2026-08-03.** "EPIC 14's remaining **eight** screens" was carried in instructions and in
`PLAN.md` for several stories after it stopped being true. The figure came from the **S14.2 registration** and
was never decremented as stories shipped. **Ten had shipped; two remained.**

> ## **A COUNT WRITTEN ONCE BECOMES A FACT BY REPETITION. Nobody re-derives a number that is already written**
> ## **down — it gets quoted, and each quotation makes it look better attested.**

**Neither of us noticed**, and it was caught only because a screen census was run for a different reason.

**MECHANICAL:** a count in a plan is a **derived** value. Either re-derive it at the point of use (`ls` the
screens, run the census) or write it as *"N as of <story>"* so its age is visible. **A bare number carries no
expiry date and therefore never appears stale.**

**Same family as the sticky-classifier and stale-artifact laws:** something computed once kept being trusted
long after the thing it described had moved.

---

# ⛔ A FALLBACK THAT REUSES A MEANINGFUL TERM HIDES THE CASE IT FALLS BACK FROM

**S14.16.** The Audit Log rendered `actor_id ? name : "system"`. But **"system" was already the correct word
for a NAMED subsystem** — 26 of 100 rows carried `actor_system`. So the fallback for *we do not know who* was
the same token as *we know exactly who, and it is a machine*.

> ## **THE GAP WAS NOT MERELY UNFIXED — IT WAS INVISIBLE, because every unattributed row rendered as a**
> ## **legitimate, well-understood category.** Nobody reports a bug that reads like a correct answer.

**GENERALISES PAST THIS SCREEN:** an `else` branch, a `default:` case, a `?? "unknown"`, a placeholder — **if
its value is a term that means something specific elsewhere in the same view, the two states merge and only
the meaningful one is ever seen.**

**MECHANICAL:** for every fallback, ask *does this token already carry meaning in this view?* If it does, the
fallback needs its own word — and usually its own visual weight, because a genuine gap is not metadata.
**Related, and the reason this one survived so long:** the same defect made the count wrong too, so even a
tally of "system rows" would have agreed with itself.

---

# ⛔ A VALUE THE CLIENT INVENTED, SITTING WHERE A SERVER FACT BELONGS

**Two instances, same class, OPPOSITE DIRECTIONS — which is why the second one was not recognised as the
first.**

| | what happened | direction |
|---|---|---|
| **S14.11** | a **failed read** rendered as **NOT CONFIGURED** | invents absence |
| **S14.16** | a **form default** (`useState(true)` + an unconditional `••••••••`) rendered as **CONFIGURED** | invents presence |

> ## **THE SHARED DEFECT IS NOT "WRONG DEFAULT" — IT IS THAT A CLIENT-SIDE VALUE OCCUPIED THE POSITION WHERE**
> ## **A READER EXPECTS A SERVER FACT.** Looking for the first shape does not find the second, because the
> ## symptom is inverted while the mechanism is identical.

**MECHANICAL:** for every control rendered before or without a successful read, ask *what does a reader
conclude from this, and did the server say it?* A checkbox, a placeholder and an empty string all make claims.

## ⛔ AND PIN BOTH ARMS — A TEST THAT ONLY PINS THE FIX PASSES A WRONG IMPLEMENTATION

Of the five mutations on this fix, **two were OVER-CORRECTIONS** — always-intent, never-dots — not the
original bug. Both are wrong in the *other* direction, and **both look careful.**

> ## **A CHECKBOX THAT IS ALWAYS CHECKED CANNOT BE TOLD FROM ONE THAT IS CORRECTLY CHECKED — and neither**
> ## **can one that is never checked. A test asserting only the arm you just fixed certifies the direction**
> ## **of your last edit, not the behaviour.**

Assert the positive AND the negative arm, and assert that **the two differ** — that third assertion is the one
that survives both over-corrections.

## SAME GLYPH, DIFFERENT CLAIM — AND THE COPY IS WHAT DECIDES

The directory-sync credential form shows the **same `••••••••`** and was correctly left alone: its own copy
says *"the fields below always start empty even when a credential is stored"*, so the dots assert nothing
there. **The glyph is not the claim; the glyph PLUS the surrounding text is.** A sweep for the character would
have "fixed" a correct panel.

---

# ⛔ A LEDGER SEES ONLY THE SHAPE IT IS KEYED ON

**S14.17.** Two censuses, two shapes, and the collapsible sidebar escaped **both**: `screencensus` is keyed on
**pages**, `wireframecensus` on **screen banners**, and a shell component is neither.

> ## **AND THE GAP IS NOT CLOSED — IT IS NARROWED. Anything that is neither a page nor a banner still has no**
> ## **ledger:** a modal, a toast rule, an empty state, a keyboard binding, a notification policy. Saying "the
> ## census now covers the design" would be the same over-claim the census was built to catch.

**MECHANICAL:** when a ledger misses something, ask *what SHAPE was it keyed on, and what shape was the thing?*
Adding a row rarely fixes it; adding a **second key** sometimes does; and the honest close is to name what
neither key can see.

## ⛔ CHECK EVERY OCCURRENCE, NOT THE FIRST

The sidebar spec's **first** `228px` sits inside the DESKTOP CLIENT block — which would have scoped it as
desktop-only and made S14.18 part of S14.20. Every occurrence shows it in the **shared preamble** as well, so
it is a shell component.

**Same discipline as reading the whole error rather than the last line, and the same failure mode as a
first-match `grep` standing in for a census.** A first hit answers *does this exist*; it never answers *where
does this belong*.

---

# ⛔ THE RENDER FLOOR REACHES A LEGAL CLAIM MADE TO A STRANGER

**S14.17, and this is the furthest the rule has travelled.** It began as *a chart must name the endpoint it
draws from*. It now governs **"SOC 2 Type II certified"** on the login page — a **compliance claim with zero
backing anywhere in the repository**, shown to every visitor **before they authenticate**.

> ## **SAME MECHANISM, DIFFERENT BLAST RADIUS. The chart misleads an OPERATOR, who can check. The badge**
> ## **misleads a BUYER, who cannot — and who is being asked to rely on it.**

Alongside it, **"SSO + SCIM enterprise ready"**: SSO ships, SCIM is explicitly OUT of v1 and deferred to
S7.5.2b. **The badge is half true and the false half is the specific standard named** — the half a buyer
checks.

**NEITHER IS DELETED. BOTH ARE GATED ON THE THING THAT WOULD MAKE THEM TRUE** — see the register. A cut claim
with no return condition is just a claim someone re-adds later with no argument to defeat.

## ⛔ THE CENSUS WORKED ON ITS AUTHOR, ONE STORY AFTER HE WROTE IT

`wireframecensus` requires a `built` disposition to name a route that exists — written specifically so a
disposition could not be aspirational. **One story later it refused MY OWN claim** that AUTH SCREENS was done:
the block also specifies a forced-enrollment modal that cannot be dismissed by click-away or Esc, and
`MfaSettings` has no such handling.

**That is the strongest evidence the mechanism has teeth** — a guard that has only ever caught other people's
work is untested.

## ⛔ AN EXEMPTION LIST IS HOW A CENSUS QUIETLY BECOMES THE CODEBASE

The forbidden-claim census first caught the comment **explaining the cut**. The instinct was a second file
exemption. **Refused:** it strips comments and keeps every file in scope instead — a claim in a comment is not
rendered and is not a claim; a claim in a string is.

**Every exemption is a place the census stops looking, and the reason is always good at the time.**

## ⛔ AND THE VACUITY FAMILY HAS A TIME AXIS

`main` went red on a **sync assertion against async content**: `queryAllByText` ran before the card it looked
for had rendered, and the `waitFor` above it waited on a **different element**. It passed locally twice and in
isolation — **the same sha that failed on CI.**

**Reproduced rather than theorised:** a 25ms delay on every mocked GET fails the old form with CI's exact
message and passes the new one, same file, one line apart.

> ## **THE EARLIER INSTANCES REPORTED ON A STATE THEY NEVER REACHED. THIS ONE REPORTED ON A STATE THAT HAD**
> ## **NOT ARRIVED YET.**

**AND THE ASSERTION ORDER IS HALF THE FIX:** await what must APPEAR, then assert what must be ABSENT.
**Absence-first passes trivially before anything renders at all** — a green that means *too early to tell*,
which is indistinguishable from a green that means *correct*.

---

# ⛔ THE WIREFRAME IS RELIABLE ABOUT LAYOUT AND UNRELIABLE ABOUT ANYTHING THE SERVER COMPUTES

**Third instance, S14.19 — and the mechanism is now clear enough to predict the next one.**

| story | the design drew | the server actually does |
|---|---|---|
| S14.12 | `polFlow` — 4 sources, 4 destinations, 5 hand-authored edges | a rule table the picture never reads; the derivation is OURS to design |
| S14.14 | `_tunnex-verify.acme.io TXT "tnx-domain-…"` | resolves the **APEX**, compares `tunnex-verify=<token>` by exact equality |
| S14.19 | four verdict chips: allow · deny · deny_aggregate · terminated | **FIVE** decisions — the fifth, `gap`, means **THE LOG IS INCOMPLETE** |

> ## **ITS AUTHOR COULD ONLY DRAW WHAT THEY COULD SEE. Layout, hierarchy, density and tone are**
> ## **VISIBLE and the design is authoritative about them. A derivation, a comparison, an enum's**
> ## **fifth member and a refused inference are INVISIBLE — and the picture is silent, not wrong,**
> ## **which is why building to it faithfully still produces a defect.**

**The S14.19 case is the sharpest:** building the four chips the design drew would have rendered a
**tamper-evidence marker as an ordinary row, or dropped it** — presenting an incomplete security log as a
complete one.

**MECHANICAL, and it is cheap:** for every screen, diff the design's ENUMERATIONS against the schema's —
chips, tabs, states, badges, columns. Where the design has fewer, ask what the extra one MEANS before deciding
it is unimportant. **A missing state is usually the failure state**, because a designer drawing a healthy
product has nothing to look at when drawing it.

## ⛔ AND A REFUSED INFERENCE IS NOT MISSING DATA

`device/user are DEFERRED (nil) — never derived from a racy src_ip lookup`. The design paired a name with the
address; the server declines to guess one, because an address maps to a device only through a lease that may
since have moved.

> ### **A WRONG NAME IN A SECURITY LOG IS WORSE THAN NO NAME.** Render what was attributed, and say why the
> ### rest is absent — the same family as *"could not check" is not "empty"*.

---

# ⛔ A DELIMITER WITH NO TERMINATOR MAKES THE LAST ITEM ABSORB EVERYTHING TO EOF

**S14.20.** The banner census measures each screen block as **start-to-next-start**. For the FINAL banner
there is no next start, so `DESKTOP CLIENT` was measured as **167,263 chars — "the largest block by 5×"**.

**It is 11,966.** The other 155k is four app-wide overlay specs (⌘K palette, detail drawer, bulk action bar,
onboarding checklist) that merely sit after it in the file.

> ## **THE NUMBER WAS COMPUTED, WHICH IS EXACTLY WHY NEITHER OF US DOUBTED IT. A measured figure carries an**
> ## **authority a guess does not — and this one measured the wrong EXTENT, not the wrong thing.**

**AND IT INVERTED THE CONCLUSION.** "Largest block by 5×" framed the desktop client as the biggest remaining
build. **It is the SMALLEST screen spec in the file** — which is what the founder had been saying about the
client all along, against a number that appeared to contradict him.

**MECHANICAL:** when a census measures RANGES, the last range is unbounded unless something terminates it.
Either give the delimiter an end marker, or **treat the final element's extent as unmeasured** rather than as
measured-and-large. Same family as the vacuity floor: the check ran, the arithmetic was right, and the
subject was wrong.

## ⛔ AND `find(1)` LISTS THE WORKING TREE, NOT THE INDEX

In the same pass I reported that **`apps/client/release/` was committed to git — "a full built .app in
version control"**. It was not. **Zero tracked files, and `.gitignore:60` already covers it.** It is 1.0G of
local build output that `find` listed because `find` does not know what git is.

**The founder ruled on that premise** — remove and gitignore — and the ruling was already satisfied before it
was made. **A wrong premise that produces a plausible instruction is worse than an obvious error**, because
the instruction gets carried out.

**MECHANICAL:** never report repository state from a filesystem walk. `git ls-files` for what is tracked,
`git log -- <path>` for what ever was, `git check-ignore -v` for why not. All three are one line.

---

# ⛔ A CAUSE INFERRED FROM A SYMPTOM'S NAME IS NOT A DIAGNOSIS

**S14.20.** A SIGKILL was reported launching Electron. *"SIGKILL on an unsigned Electron binary"* is a real,
well-known macOS failure — so Gatekeeper **fitted**, and a README section was written with working commands
to fix it.

**It never reproduced.** The report came from a **different clone on a branch ten stories old** with no client
work and **no `Electron.app` installed at all**. The binary was ABSENT, not blocked.

> ## **AND THE FIX'S COMMANDS WERE REAL, WHICH IS WHAT MADE IT DANGEROUS. `xattr -cr` and an ad-hoc re-sign**
> ## **both run, both do something, and neither confirms the cause. REAL COMMANDS AROUND AN UNCONFIRMED**
> ## **CAUSE READ AS EVIDENCE** — and once written down they become the next person's starting assumption.

⛔ **The tell was present and ignored: I wrote "I could not reproduce it" IN THE SAME COMMIT that shipped the
remedy.** Stating the doubt is not the same as acting on it. A cause you could not reproduce is a
**hypothesis**, and a hypothesis in a README is indistinguishable from a finding.

**MECHANICAL:**
- **Reproduce, or label it a lead.** Documentation may carry "if X, check Y" — it must not carry "X is caused
  by Y" without a reproduction.
- **Verify the TREE before diagnosing the CODE.** Clone, branch, head, files present, deps installed. Two
  clones caused two separate confusions in this epic — a stale served bundle, then this.
- **A missing artefact and a blocked artefact fail differently.** `NO Electron.app` and `Killed: 9` are not
  the same sentence, and the first was never checked.
