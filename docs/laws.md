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

## DORMANT-MACHINERY — INSTANCE 2 (EPIC 13, WF-S13-6, founder-ruled 2026-07-31): the epic that minted the law shipped a case of it

**S8.4 minted the law; EPIC 13 is its second instance, and a more dangerous shape.** In S8.4 the dormant code was
a *sweep* for residue that could not exist yet. Here the dormant code is **the feature itself** — gateway
recovery — in the case the epic was opened to fix.

**The caller/trigger analysis, stated the way the law now requires:**

| | |
|---|---|
| **the machinery** | `attemptRekey` — proof-of-possession recovery |
| **its only caller** | `main()`, `apps/node/cmd/agent/main.go:77`, via `identity.Decide` on credentials read at boot |
| **the trigger it must serve** | a certificate expiring at **runtime**, under a process that is already up |
| **can caller and trigger co-occur?** | **NO.** `main()` runs once, before the trigger exists. Nothing re-enters it |

Recovery is therefore **reachable only by restarting the process** — and the epic's headline claim is that a
gateway comes back *by itself*.

**The epic WROTE THE BUG DOWN AS A SAFETY PROPERTY.** `docs/S13.1-decisions.md:1277`, from Batch B:

> *"There is no path from the in-loop clock check to starting a recovery, so a clock that jumps the other way
> cannot…"*

That sentence is **correct about NTP** — a backward clock jump must not trigger a recovery — and it is **also an
exact description of the defect**. The same absent edge that makes a clock jump safe makes runtime expiry
unrecoverable. It was reasoned about, written down, and shipped, because it was only ever examined from the
direction where it is a virtue.

**What this adds to the law:** when you record that *no path exists* from X to Y as a safety property, **state
what else that missing path was carrying.** An absent edge is a guarantee in one direction and a gap in the
other, and the note that proves the guarantee is the natural place to notice the gap. See also
`tunnex-unit-tests-prove-behaviour-not-reachability` — name the trigger, then check the caller can co-occur with
it.

## THE VACUITY DETECTOR (founder-ratified 2026-08-01) — read this BEFORE the five mechanisms below

**WHEN A CHECK REPORTS THE SAME VALUE FOR EVERY INPUT, VERIFY ONE INPUT INDEPENDENTLY.** A check that cannot
distinguish its cases reports agreement — and agreement reads as success.

The five mechanisms below name distinct ways a green result can mean nothing. **This is the PROCEDURE that
catches all five**, and it is cheaper than diagnosing which one you are in:

- a **half-fold** — every row reports "closed"
- a **tautological guard** — every input satisfies the assertion
- a **fixture that restates production** — every run agrees with itself
- **true-by-structure** — every mutation of the fix leaves it green
- **absence-means-permit** — every unset value reads as allowed

**How to apply it, in one line: name a case whose answer you already know by other means, and check the check
against that case.** If the check cannot tell that case apart from the others, it is measuring nothing.

**TWO INSTANCES ON ONE DAY, both inside an audit that existed to detect vacuity:**

1. **`git show` failing into `2>/dev/null`** so `grep -c` counted an empty stream. It returned `0` for every
   commit — **including one whose true value was independently known to be `1`.** The uniform result across a
   case with a different real answer was the entire tell; the tally itself looked like a clean audit.
2. **zsh's `:a` history modifier ate a path** — `"$c:apps/..."` expands to `${c:a}` + `"pps/..."`. Caught ONLY by
   a deliberate **injected-duplicate probe**: feed the check a case that must produce a different answer and
   require it to. The probe proved the counting method sound while the path construction was broken.

3. **`ok … [no tests to run]`** — a green with NOTHING EXECUTED, byte-indistinguishable in a scrollback from a
   green with everything passing. Produced by running a `//go:build enterprise` test file without the tag.

**CAN A GATE PRODUCE THAT SHAPE? Checked, and the answer is a qualified YES — which makes it a gate hole, not
just a local footgun.**

- If **every** file in a package is excluded by a build tag, `go test` reports **`FAIL … [setup failed]`**. Loud,
  and safe.
- But if **some** files are excluded and others are not, the package runs the survivors and prints **`ok`**. The
  excluded tests are invisible, and nothing in the output distinguishes "these tests passed" from "these tests
  were never compiled."

**Live instance in this repo:** `apps/api/internal/devices/restore.go` is **untagged** — it ships in BOTH
editions — while its only test file, `restore_integration_test.go`, is `//go:build enterprise`. So
`make test-editions`' open pass compiles the restore path, tests none of it, and reports `ok` for the package.
Registered as a finding in `docs/S13.1-pass3-triage.md`.

**Instance 2 is the form to copy.** Do not merely inspect a check; **feed it a case it must fail on.** A check
that has never once produced a different answer has not been shown to be capable of one.

## TRUE-BY-STRUCTURE — the FOURTH way a green check means nothing (EPIC 13, 2026-08-01)

**The assertion is guaranteed by code the fix never touched, so no mutation of the fix can break it.** The red is
about a real property, the property genuinely holds, and the test says nothing whatever about the change it was
written for.

**Four mechanisms now, and they are distinct:**

| mechanism | what fails |
|---|---|
| **half-fold** | the remedy addresses the defect's NEIGHBOURHOOD, not the defect |
| **tautological guard** | the expectation DERIVES from the artifact under test |
| **fixture restates production** | the DOUBLE stands in for the thing being tested |
| **TRUE-BY-STRUCTURE** | the assertion is held up by code the fix never touched |

**THE DIAGNOSTIC: if you cannot describe an INPUT that would make the assertion false, it is not a test of the
fix.** Not "can I imagine the code being wrong" — name the input. If the only way to falsify the assertion is to
edit a different function than the one under test, the red belongs to that other function, or to nothing.

**THE INSTANCE.** A red asserted that sustained throttling never reaches the join token. It PASSED with
`refusals++` injected directly into the throttle branch — because the exhaustion check lives inside the
`ErrRekeyRefused` case and the throttle branch returns before reaching it. **A throttle cannot spend the token
regardless of any fix**, so the assertion was unfalsifiable by construction.

**THE PROCEDURAL CAUSE, AND THE RULE IT MAKES: ONE MUTATION AT A TIME. Not a preference — the rule.** This
surfaced only because the two mutations were run SEPARATELY. Combined, the package would have shown ONE `FAIL`
and one failing test name, and that reads as success for both reds — the passing one hidden behind the failing
one. A combined mutation run can prove *at least one* red works; it can never prove that each does.

### FIFTH MECHANISM — SAMPLED-SLOWER-THAN-THE-EVENT (EPIC 13 box-walk, 2026-08-01)

**The observation window is coarser than the state it observes, so the check reports the steady state and
never the transition.** The property may well hold; the check could not have seen it either way.

| mechanism | what fails |
|---|---|
| **SAMPLED-SLOWER-THAN-THE-EVENT** | the observer's interval exceeds the state's lifetime |

**THE INSTANCE.** §B's B2 asserts that `nodes.cert_delivered` flips `f` → `t` across a re-key. The walk polled
it **twelve times at ~7-second intervals and read `t` every time**, including 3 seconds after the recovery. The
window is bounded by the code: `RekeyNode` clears the marker in the same statement that rotates the serial
(`nodes.sql:319-326`), and `nodes.sql:49` sets it back on the agent's first authenticated call — here
`06:22:17.246` → `06:22:19.262`, **about two seconds.** A 7-second poll against a 2-second window **cannot
fail.** Twelve green samples, zero information.

**It passes the TRUE-BY-STRUCTURE diagnostic and fails anyway**, which is why it is a separate mechanism: an
input that falsifies the assertion is easy to name (a re-key that never re-delivers). The defect is not in the
assertion — **it is in the sampling rate**, and no amount of reasoning about the assertion surfaces it.

**THE DIAGNOSTIC: state the event's LIFETIME and the observer's INTERVAL as two numbers, and compare them.** If
the interval is not comfortably smaller than the lifetime, the check is decorative. For a transition bounded by
two code paths, read the lifetime out of the code rather than estimating it.

**Recorded as NOT OBSERVED, never as passed.** The distinction is the whole point: a walk that logs "B2 green"
on twelve blind samples has manufactured evidence.

### SIXTH MECHANISM — ASSERTS-A-DIFFERENT-EVENT-THAN-IT-WAITS-ON (EPIC 13, 2026-08-01). THE MIRROR OF THE OTHER FIVE.

**The check waits for event A and asserts property B, where B happens strictly after A and nothing synchronises
them. It fails for a reason unrelated to its subject.**

| mechanism | what fails |
|---|---|
| **ASSERTS-A-DIFFERENT-EVENT-THAN-IT-WAITS-ON** | the wait and the assertion are about different moments |

**THIS ONE INVERTS THE FAMILY.** The other five all answer *"could this check have failed for the RIGHT
reason?"* — they are green when they should be silent. This one is the mirror: it goes **red for the WRONG
reason.** And the consequence is symmetric, which is the part worth internalising: **a check that can fail
spuriously is exactly as uninformative as one that cannot fail at all.** In both cases the colour carries no
information about the subject.

**THE INSTANCE.** `TestExpiryWhileRUNNINGRecoversWithoutARestart` — `identityWatchLoop`'s acceptance red, and
EPIC 13's **first merge precondition**. It waited on `issued`, a counter incremented inside the fake control
plane's HANDLER (*"the CP produced a response"*), then called `cancel()`, then asserted the AGENT'S DISK
(*"the recovery was promoted"*). The disk write happens strictly after the response. A fast machine won the
race; a contended CI runner lost it — and did, on the very first CI run this branch ever received.

**WHY IT MATTERS MORE ON A GATE.** On merge day a red here is indistinguishable from a real regression, and a
green proves only that the runner was fast. **A flaky acceptance test cannot carry a merge precondition** — it
converts the gate into a coin toss whose outcome gets argued about rather than trusted.

**THE DIAGNOSTIC: name the event the test WAITS for and the property it ASSERTS, in that order, and check they
are the same moment.** If the assertion is downstream of the wait, the test is racing its own subject. **The fix
is never "wait longer" — it is to wait for the asserted property itself.**

**PROVEN BY TWO MUTATIONS, because "wait longer" and "wait for the right thing" are indistinguishable from a
single green:**

| mutation | expected | observed |
|---|---|---|
| promotion silently skipped (`saveCredsFn` → no-op returning nil) | FAIL | **FAIL at 15.11s**, `issued=749`, disk still expired |
| write delayed 3s then genuine | PASS | **PASS at 3.18s** |

The first proves the longer wait still bites on a real non-recovery; the second is the case the old wait lost.
**One mutation would have proven neither.**

## DOES THE REMEDY ADDRESS THE DEFECT, OR ITS NEIGHBOURHOOD? (founder-ratified 2026-08-01, EPIC 13, three instances in one epic)

**A fold is not closed because an edit landed near the defect. Ask of every remedy: does this make the NAMED
DEFECT impossible — or does it fix something ADJACENT to it?** Adjacent fixes are made in good faith, pass their
reds, survive marker sweeps, and read as complete.

**An earlier version of this law said multi-claim fold rows were the risky shape. That was wrong** — it was
inferred from two instances and refuted by the third. **Claim count is a SYMPTOM, not the mechanism.**

| instance | the defect | what the remedy did instead | claims in the row |
|---|---|---|---|
| claims 2/4/13/20 | nothing ENTERS the recovery loop at runtime | re-read the premise **inside** the loop — correct for 4/13/20, silent on 2 | 4 |
| claims 9/10/14 | the throttled branch has no exit or escalation | honoured `Retry-After` — the interval, not the exit | 3 |
| **#11** | an interrupted promotion leaves a mismatched pair | made the seam **injectable** and the failure **survivable** — the pair itself neither prevented nor detected | **1** |
| (Batch A, found earlier by this same question) | re-key must refuse a node the CP cannot verify is GONE | authorized any caller **proving the current key** — a live-node takeover | 1 |

**Two of four are single-claim rows.** The mechanism is that a defect has a NEIGHBOURHOOD — its consequences, its
testability, its survivability, its adjacent parameters — and every one of those is a satisfying thing to fix.
The fix is real, the red passes, the row gets ticked, and the defect is untouched.

**What binds now:** state the remedy as a sentence that makes the defect impossible, and check that sentence
against the defect's own words. *"The premise is re-read each pass"* does not contain *"something enters the
loop."* *"The seam is injectable"* does not contain *"the pair matches."* If the remedy's sentence and the
defect's sentence are about different subjects, the fold is open however good the code is.

**The corollary that cost this epic three instances:** a marker sweep asks *did the edit land?* — a question all
three passed. It cannot ask this one. **Sweeps verify presence; only reading the defect beside the remedy
verifies closure.**

## ONE-TRUTH — 6th instance (EPIC 13, 2026-08-01): TWO CLIENTS, ONE SERVER TRUTH, NEITHER CONSUMING IT

**The server computes the correct predicate. Both clients independently reimplement a WRONG one and neither
consumes the server's.** This is not a UI gap and the fix is not a dropdown.

| layer | the predicate |
|---|---|
| **API — the truth** | a gateway is usable iff `endpoint != "" && wg_public_key != ""` (`devices/service.go:72`), enforced with `409 node_not_ready`. `Create` accepts `in.NodeID`, so choosing has ALWAYS been supported |
| **web** | `nodepick.ts:30` — `selectableNodes(nodes)[0]`, filtering on `status == "active"` |
| **CLI** | `device.go:43-52` — iterate, take the first `status == "active"`, `break`. **No flag exposes the choice** |

**Two independent reimplementations of the same wrong test.** `status='active'` is a liveness claim; usability is
`endpoint AND key`. A gateway can be active and unusable — `azure-gw` was, for six days — and both clients
routed every device creation to it, producing `node_not_ready`, an error naming the OPERATOR'S agent
configuration for a defect in client selection.

**THE FIX IS TO EXPOSE THE API'S PREDICATE AND CONSUME IT**, not to add a picker to one surface. A picker over the
same wrong list still offers a dead gateway; consuming the server's predicate fixes both clients and the default.

**WHY NO TEST ON EITHER SIDE COULD CATCH IT.** Each client is INTERNALLY CONSISTENT — the web's tests pass against
the web's rule, the CLI's against the CLI's, and the server's against the server's. **The defect exists only in
the relationship between them**, which is precisely what a duplication hides. Note the surface: `apps/cli` had NO
CI job at all until S11 slice 1, and this is that same surface producing a second defect — but **this one is not
a coverage gap.** More tests on either client would have confirmed the wrong rule faster.

**Generalised:** when a server enforces a predicate and clients must anticipate it, the predicate is a SHARED
TRUTH and belongs in the contract (a field, or the generated types), never re-derived per client. Two
re-derivations agreeing by luck is not a property; two re-derivations agreeing WRONGLY is what shipped.

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

**Corollary — CHECK THE REMEDY, NOT ONLY THE CLAIM (founder-ratified 2026-07-30, S13.1 Slice 6).** A census of
user-facing strings must ask what each one PRESCRIBES as well as what it asserts. Slice 6's census graded three
`needs_reexport` consumers on whether they named a *cause*, and passed the badge label `re-export needed` as
"cause-neutral ✅" — while the widening made it visible to MANAGED devices, for which there is no export path at
all. The tooltip was caught because it named the wrong cause; the label was missed because it named no cause and
nobody asked whether it named a possible ACTION. **A label can lie through the remedy it prescribes, and a
census that only grades claims will pass it.** Every censused string gets both questions: is what it says still
true, and is what it tells the user to do still possible.

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

### INSTANCE COUNT — the law is NOT BINDING (founder-ratified 2026-07-31, EPIC 13 review pass 1)

**Six prior instances, and then THREE MORE IN A SINGLE REVIEW PASS — on the story that was supposed to satisfy
this law.** Counted here rather than given a new law, because a second law would be a way of not noticing that
the first one is not working.

The class is narrower than the law's general form and worth naming precisely: **a guard whose expectation is
derived from the artifact under test** (first papered at S7.5.5 as the tautological-guard finding). Pass 1's
three:

| # | guard | what it derives from the artifact | what it therefore cannot catch |
|---|---|---|---|
| 3 | `rekey_integration_test.go:195-198` | hand-applies the `UPDATE` that pushes `cert_not_after` back into the past | that a lost re-key commit *advances* the very column the gone-gate reads, so real fingerprint recovery is refused for a full 48h TTL. The test fabricates the state that makes it pass |
| 19 | `rekeyquery_test.go:61` | asserts the presence of a substring of the query it is guarding | a `WHERE` clause that re-keys **every active node** still passes |
| 20 | `migrationcompat_test.go:33-41` | a line-level regex over migration text, with no notion of which tables the previous version had | that it fires on a `RENAME` inside a table created in the **same release** — forcing an expand/contract shim onto a version that cannot exist, which was then documented as protecting it |

**Three in one pass means the law is being read and not applied.** #20 is the sharpest: the guard's verdict was
taken as authority and a compatibility shim was built to satisfy it, without anyone asking whether the table
existed one version ago. *The law was invoked to justify the work that the law would have prevented.*

### FURTHER INSTANCES (2026-07-31, EPIC 13 fold)

**Instance 6 — and the procedure is now MECHANIZED rather than remembered.** A third mutation reported `ok`
because a Python syntax error inside the heredoc meant the patch never applied. Three false proofs in one session,
all the same shape: **the outcome was verified and the APPLICATION was not.**

`scripts/mutate.sh` now asserts, before any test runs: the anchor exists (and exactly once), the file actually
changed on disk, and the result still compiles. Anchor and replacement are read from FILES, never argv, so no
shell escaping can corrupt them. A mutation that matches nothing now exits with a message saying so, instead of a
green test.

**The generalisation, since it cost three instances to learn:** when a check's setup can silently fail, the check
verifies nothing and reports success. Verify the setup, not just the result.

**A false proof through mangled tooling — instance 4 of this class.** A mutation round was run through a shell
function whose `\&` escaping silently corrupted every patch: two mutations produced BUILD failures (already known
to be indistinguishable from a pass) and one reported **`ok`** — a mutation that never applied, read as "the fix
is unnecessary". The rule that now binds: **mutation rounds are applied through a heredoc with an anchor
assertion** (`assert old in s`) so a patch that does not match fails loudly instead of passing quietly. Re-running
the same round correctly produced four clean FAILs.

**Fixture fidelity — two more instances, both caught by the reds themselves.** A cascade helper that recorded
LESS than the production sweep it mirrored, so a restored device looked like a pre-migration row and the red
failed for the wrong reason. And a fixture using fixed certificate serials against a globally unique column: it
passed once and then failed on a constraint forever — **a fixture whose first green is its only green**, caught by
`make test-editions` rather than by the direct run that wrote it. Both are the fixture-fidelity law: a fixture
that cannot express the production state tests a different system.

**What binds, from now on, when a guard is written or trusted:** state, in one sentence beside it, **what the
guard reads and where that value comes from.** If the answer is "from the thing it is checking", it is not a
guard — it is a restatement. Applies equally to trusting an EXISTING guard's verdict: #20 was a failure to ask
that question of a guard someone else wrote.


---

## A DETERMINATION OF "GONE" MUST PROVE THE CREDENTIAL CANNOT WORK

*Minted: EPIC 13 / S13.1, from a ruling that was wrong as literally written — and whose counterexample was in the
walk that produced it.*

Recovery mechanisms need to decide when an existing credential may be **replaced**. The condition ruled for
WF-S11-11 listed three determinations of "unusable": **expired**, **unreadable**, and **name mismatch**. The first
two are proofs the credential *cannot function*. The third is a proof that *configuration disagrees* — which is
not the same thing, and treating it as equivalent is destructive.

**The counterexample is in the walk that produced the ruling.** The enrolment command was pasted on the wrong
host: `azure-gw`, holding `azure-gw`'s **valid** certificate, with `TUNNEX_NODE_NAME=aws-gw-1`. Under a
mismatch-authorizes rule the agent would have abandoned a live gateway's identity and enrolled that host as a
different node — a working gateway made to look dead while a second node took its name. That is precisely the
S8.2c WF-2 disaster the stored-identity preference exists to prevent, **reproduced by the guard meant to help**.

**THE LAW:** a determination that the original is *gone* must rest on evidence the credential **cannot work** —
expired, revoked, cryptographically unusable. Evidence that configuration merely **disagrees** — a mismatched
name, an unexpected host, a surprising label — is a **loud ERROR and never an authorization**. When the two kinds
of evidence conflict, the fail-toward-the-existing-identity clause governs.

The same shape governs the CP side (S13.1 D3): `revoked` or `cert_not_after < now()` may authorize a re-key;
`last_seen_at` stale may not. Silence is not proof that something cannot work — it is only proof that we have not
heard from it.

---

## MUTATION-TESTING COROLLARY — A MUTATION MUST COMPILE

*Minted: EPIC 13 / S13.1. COULD THIS CHECK HAVE FAILED?, applied to the thing checking the checks.*

Mutation testing is a habit in this repo now: break the fix, watch the guard fail, restore. The trap is one level
up again.

A mutation that replaced `if expired {` with `if false {` **orphaned a variable and failed to build**. The harness
grepped for test-failure patterns, matched nothing, and printed nothing — **identical output to a mutation that
passed silently**. For a moment the core fix of an entire slice appeared to be unguarded.

**THE COROLLARY:** a mutation must **compile**. A build failure is not a passing test and it is not a failing one;
it is a mutation that never ran. So:

- Prefer mutations that keep every symbol used — `expired := false` rather than `if false {`.
- **THE DISCRIMINATOR — when a build failure IS the rejection.** A build failure is a **valid** rejection when the
  guard *is* the type signature: adding a `lastHandshakeFailed bool` to a decision function that must not see the
  network fails with `not enough arguments in call to Decide`, and that is precisely the guard working — the
  compiler is enforcing the constraint, and the author is sent back to the reasoning. It is an **invalid** mutation
  when the guard is *behavioural* and the build failure merely prevented the behaviour from running: neutralising
  `if expired {` orphaned a variable, so nothing executed and the output was indistinguishable from a pass. Ask
  which kind of guard you are testing before reading the outcome.
- Have the harness distinguish **build failure**, **test failure**, and **pass** as three outcomes, never two.
  Grepping for `FAIL:` alone conflates the first with the third.
- The pass you must see is the *named assertion message*, not merely the absence of output. Absence of output is
  the failure mode.

---

## A CENSUS MUST ASSERT THE PROPERTY, NEVER A COINCIDENCE OF THE CURRENT TEXT

*Minted: EPIC 13 / S13.1 Slice 4. A corollary of COULD THIS CHECK HAVE FAILED?, pointed at the guards' matching.*

Censuses in this repo read source text — Dockerfiles, `.sql` files, enum blocks, renderers. That is their strength
and their trap: it is easy to match something that is *true of the text today* rather than the *property being
guarded*.

Three instances, all this epic:

- A guard asserted `strings.Contains(queries, "cert_not_after)")` — the closing paren of an INSERT column list. It
  was pinning the column's **position at the end of the list**. Appending a new column after it was a completely
  legitimate change and it **broke the guard**, which teaches the next author to weaken the guard rather than trust
  it.
- A kind census matched `[a-z_]+` and silently dropped `k8s_endpoints_unavailable`, because the name contains a
  digit. The pattern encoded an assumption about naming that the names did not honour.
- A shipping census guessed binary names from package names, and `./cmd/server` builds `tunnex-api`. It both
  false-passed and false-failed until it parsed the `-o` flag instead.

**THE LAW:** assert the property, scoped. **Position, ordering, adjacency and formatting are coincidences**;
presence *within a named scope* is the property. Concretely:

- Extract the region first (this query, this const block, this build stage), then assert **within** it — so a match
  elsewhere in the file cannot vouch for it.
- Match identifiers, not punctuation. `cert_not_after` inside `CreateNode`, not `cert_not_after)` anywhere.
- Derive the expected set from the source of truth (`AllKinds()`, the `-o` flag) rather than restating it.
- When a guard fails on a change you believe is correct, the first question is whether the guard is pinned to a
  coincidence — not whether the change is wrong.

### Companion: BENIGN vs INERT — what a non-rejecting mutation actually means

A mutation that produces no failure has two very different explanations, and conflating them is how a guard is
wrongly trusted or wrongly deleted:

- **BENIGN** — the property is genuinely still enforced, by another path. Neutralising an empty-key check left the
  case refused by a later parse failure: **defence in depth**, so the mutation revealed redundancy, not absence.
- **INERT** — nothing enforces the property, and the guard never did. A red asserting
  `degradedKind(CertExpired: false)` is not the cert-expired kind passed with its own fix removed.

Tell them apart by asking *what refused, and why* — then mutate **that** instead. If nothing refuses, the guard is
inert and the property is unprotected.

---

## EXPIRY IS AN ABSENCE OF ACTION; REVOCATION IS THE PRESENCE OF A DECISION

*Minted: EPIC 13 / S13.1, from a ruling that was wrong and an attack chain that proved it.*

A recovery mechanism authenticated by **proof of possession** asks "do you still hold the key?" It cannot ask "are
you the person who should hold it." That distinction decides what such a proof may overturn.

**A cryptographic proof may overturn an absence of action. It must never overturn a decision.**

The attack that established this: an attacker steals a gateway's state volume — its private key. The operator
notices and **revokes** the gateway, which is the product's answer to a stolen credential. The attacker then proves
possession of the stolen key and, under a gate that accepted `revoked` as authorizing, receives a fresh certificate
for that node — active, same identity, same policy. **Revocation defeated by the exact credential it was invoked
against.**

`revoked` had been listed as the *strongest* authorizing evidence, on the reasoning that it is the strongest
evidence the node is **gone**. It is. That was the wrong question: strength-of-evidence-that-it-is-gone is not
validity-of-authorization-to-**return**.

**THE LAW:**

- **Expiry, lapse, timeout, absence of a heartbeat** — nobody decided anything. A proof of possession may recover
  from these, because no intent is being overridden.
- **Revocation, suspension, deliberate disablement, an explicit deny** — a human decided. Only another human act may
  undo it. A credential must never be able to reverse the decision made *about that credential*.

Undoing a decision requires an act of the same kind: an operator-minted token, an authenticated administrative call,
a signed approval. Never a proof that the holder is still the holder — that is precisely what was doubted.

**And prefer construction to convention when enforcing it.** The statement that performs recovery does not
*carefully avoid* resurrecting a revoked row; it does not reference `status` or `revoked_at` at all, so no future
call path can reintroduce it. The gate that authorizes takes no liveness parameter, so staleness cannot be passed in
by mistake. A rule that cannot be expressed is stronger than a rule that is merely followed.

### Corollary — WHEN A RED'S ASSERTION INVERTS, SAY WHICH BEHAVIOUR WAS WRONG

A test whose expectation reverses is recording a **decision**, not applying a fix. Quietly editing it is how the
reasoning is lost and the decision gets re-litigated by someone who only sees the current line.

So: the commit states which behaviour was wrong and why, and the test carries the reasoning — for a security
inversion, **the attack chain itself**, not just the rule. A future reader who finds `revoked → refuse` with no
explanation will eventually decide it is an inconvenience worth relaxing.

---

## A FORWARD REFERENCE NAMES AN INTENTION, NEVER A CAPABILITY

*Minted: EPIC 13 / S13.1. A comment citing a story is not a citation of code.*

`auth/service.go:177` read:

```go
// (Per-caller email throttling is a separate concern — S11.3 rate limiting.)
```

Accurate when written, and it reads like a pointer to a mechanism. It is a pointer to a **plan**. S11.3 was scoped,
listed as UNBUILT in EPIC 11's own verify pass, and never shipped — Slice 1 delivered the security-CI tier instead.
Two epics later that comment was read as evidence the throttle existed, and a ruling was made on it: *"rate-limit it
(S11.3 shipped the machinery)."* The machinery did not exist.

This is the same shape as EPIC 11's advisory CI job that never ran, and as a runbook naming a binary the image did
not contain: **an artifact that reads like evidence of a thing rather than evidence of a plan for the thing.**

**THE LAW:** a comment, doc, or ticket that cites a story name is naming an intention. Before relying on it:

- **Grep for the code, not the citation.** "Where is this implemented" is a different question from "where is this
  mentioned", and the second is much easier to answer accidentally.
- **Write forward references so they cannot be misread** — *"there is no rate limiting today; S11.3 would add it"*
  rather than *"S11.3 rate limiting"*. The tense is the whole difference.
- **When a plan item is descoped, sweep its forward references.** A citation outliving its story is how a plan
  becomes a phantom capability that someone later builds a ruling on.

Corollary of ARTIFACT-EXISTS ≠ ARTIFACT-WORKS, one step earlier: here the artifact does not exist at all, and the
*reference* is what exists.

---

## A GUARD MUST BE EXERCISED THROUGH THE STACK IT RUNS IN

*Minted: EPIC 13 / S13.1, from a review finding on a guard written in the same slice as the law it violated.*

A test that calls the function directly tests **the function**. It does not test **the protection** — because the
protection is the function *plus everything the request passes through before reaching it*.

The instance: a per-endpoint throttle read `r.RemoteAddr` and deliberately ignored `X-Forwarded-For`, on the
reasoning that a header the caller controls is not an identity. Three tests asserted exactly that, including one
that rotated a forged `X-Forwarded-For` across four requests and proved the budget still bound. All three passed.

**And the throttle was defeated in production**, because `middleware.RealIP` was registered *above* it and had
already overwritten `r.RemoteAddr` with the client-supplied header value. The tests built bare `httptest` requests
and never ran that middleware, so they proved a property of the function that the deployed path did not have. The
guard was inert and was reported as proven.

**THE LAW:** when a guard's correctness depends on its position in a pipeline — middleware order, interceptor
chains, decorator stacks, hook ordering, SQL executed through a wrapper — the test must either run the real
pipeline or assert the position itself. Concretely:

- **Assert the position.** `TestThrottleIsRegisteredBeforeRealIP` reads the router and fails if the registration
  order changes. Blunt, and it catches the actual defect where a unit test cannot.
- **Or exercise the composed handler**, not the leaf — build the router and send a request through it.
- **Ask what runs before this.** The question that would have found this in seconds is not "does my function
  ignore the header" but "**is `RemoteAddr` still the peer address by the time my function reads it?**"
- **Suspect any guard whose input is mutated upstream.** Anything that rewrites request fields — proxy middleware,
  body decoders, auth context injectors, path rewriters — turns "I read X" into "I read whatever the chain left in
  X".

This is COULD THIS CHECK HAVE FAILED? narrowed to a specific mechanism: the check *could* have failed on a wrong
function, and *could not* have failed on a wrong pipeline — which was the way it was actually wrong.

## A FIXTURE THAT RESTATES PRODUCTION TESTS THE RESTATEMENT (founder-ratified 2026-07-31, WF-S13-3)

**Fixture-fidelity, in the direction nobody watches for.** The known form is a fixture that records LESS than
production, so a red fails for the wrong reason — annoying, and self-announcing. This is the mirror: a fixture
that records MORE, so a red **passes** for the wrong reason. Nothing draws attention to a pass.

**The instance.** EPIC 13's fold for finding #8 added `revoked_prev_status` to the restore's READ side and, via a
bare `s.replace` whose anchor missed by one space, never added it to the production sweep. The same fold "fixed"
the test fixture to set the column by hand. So the red asserted against a fixture **simulating a production
change that did not exist** — and passed. So did a mutation round. Four gates, a review pass and a mutation round
all missed it; the box-walk found it in one query.

**THE RULE:** a fixture must **CALL** the production path it depends on, never restate it. Where restating is
unavoidable, the red is not evidence about production and must say so in its own comment.

**THE COROLLARY, and it amends a claim made earlier in the same epic:** *per-fix reds substitute for a review pass
ONLY where the fixture calls production.* Where a fixture restates it, the red proves the restatement and the
review remains owed. That claim was endorsed on the strength of a mutation round catching two vacuous guards —
and WF-S13-3 is the case it does not cover, because the mutation round passed too.

**Mechanically enforced, in the half that was missing:** `scripts/prove-fix.sh` requires the red to **FAIL BEFORE
the edit**, then proves the anchor matched exactly once, the file changed, it compiles, and the red passes after.
Assertion 1 is the one this incident needed — the fixture's simulation made the red green *before* the edit, and
that gate would have stopped it.

---

## A UNIT TEST PROVES BEHAVIOUR, NEVER REACHABILITY (founder-ratified 2026-07-30, S13.1)

**For every mechanism: name the caller, and prove the trigger can CO-OCCUR with the gate.**

EPIC 13 shipped `RestoreCascadeRevokedDevices` with reds that all passed. It has one caller (`Rekey`); devices are
cascade-revoked in one place (`Revoke`); and `Rekey` refuses a revoked node (D3). So the only trigger that creates
the work puts the node into the one state that can never reach the code that does it — **correct code wired to a
trigger it cannot fire from.** The reds proved the restore does the right thing *when called*. Nothing proved it is
ever called, and nothing in the build or the story-end review asked.

It surfaced while a WALK LEG was being drafted, because writing a leg forces the sequence to be stated end to end —
which is a reason to draft walk legs early, not only before a walk.

**This is the [who-reads-this probe](#) one layer up.** That probe catches a PRODUCER with no consumer (a channel
field nothing reads); this catches a CONSUMER with no reachable producer. Same defect class, opposite end, and both
land on the dormant-machinery law.

**The check, and it is cheap:** grep the callers of the new function AND the callers of whatever produces the state
it consumes, then ask whether both sets of preconditions can be true **at the same time**. Cheapest at design time,
still cheap while drafting a walk leg, expensive after it ships as code that never runs.

---

## ABSENCE MUST BE THE CLOSED STATE (founder-ratified 2026-07-31, S13.1 D3)

**A column whose ABSENT value means PERMIT is a fail-open waiting for the first writer that does not know about
it. Choose the encoding so absence is the CLOSED state.**

This is a by-construction rule, not a check. A check asks every future writer to remember; an encoding cannot be
forgotten, because the database supplies the safe value to anyone who says nothing.

**Three instances, all inside one ruling, which is why it is a law and not a note:**

1. **Existing rows.** `nodes.cert_delivered_at` shipped as a nullable timestamp where NULL meant *undelivered* —
   the state that OPENS the re-key redelivery carve-out. A new nullable column reads NULL for the entire fleet, so
   deploying it would have opened the carve-out for every node in the field on day one: **a fail-open introduced
   by the fix for a fail-open.** Caught before merge and closed by a backfill (0063).
2. **New rows.** The backfill fixed the rows that existed and did nothing for the ones created afterwards.
   `CreateNode` names six columns and not the marker, so **every freshly enrolled node** read undelivered while
   holding a valid certificate — on every replica, not only an older one mid-roll. The backfill answered
   "what about the fleet?" and nobody asked "what about tomorrow's fleet?" (0064).
3. **The encoding that looks right and is not.** `NOT NULL DEFAULT now()` was the obvious repair and cannot work:
   re-key must still express *never delivered*, and NOT NULL forbids the value that meant it. The shape that
   satisfies both halves is a **boolean `NOT NULL DEFAULT true`** — absence lands CLOSED, and only the one
   statement that legitimately opens the state says so explicitly.

**The test that distinguishes a real application of this law from a restatement:** write the INSERT that an
unaware writer produces — naming exactly the columns today's code names, and nothing else — and assert the
resulting row is in the refusing state. If that test cannot fail, the encoding is not doing the work.

**Related:** this is the schema-level sibling of *a determination of "gone" must prove the credential cannot work*
and of the fail-closed direction in KILL-SWITCH-NO-UNBOUNDED-I/O. Same instinct, moved from code into DDL, where
it cannot be refactored away.

### SIBLING — A RECOVERY MECHANISM MUST NOT DESTROY ITS OWN INPUT (founder-ratified 2026-08-01, WF-S13-8)

**A recovery path must not delete the record it needs, on the path where recovery is what failed. Discard the
input only after the recovery it feeds has demonstrably succeeded.**

ABSENCE MUST BE THE CLOSED STATE governs the value an unaware writer *supplies*. This governs the value a
recovery path *destroys*. Both fail the same way — the safe state has to survive somebody not thinking about it —
and both are by-construction, not checks.

**The instance.** `restoreDNS` (`apps/helper/backend_darwin.go:672`) puts every macOS network service's DNS back
from `/var/run/tunnex/dns.json` after a full tunnel hijacked it. Each restore is `_ = run("networksetup", …)` —
the error discarded — and `os.Remove(dnsBackupPath)` then runs **unconditionally**, outside any success test. So
a single failed restore strands that service on the tunnel resolver **and deletes the only record of what it
should have been.** The startup `CleanStale` retry that exists for exactly this case becomes a permanent no-op,
because its input is gone. **Total failure (no DNS), self-concealing (the tunnel is visibly down, so nobody
inspects resolver settings), and self-destroying.**

**The shape, stated so it is recognisable away from DNS:** *cleanup that runs unconditionally after a
best-effort apply.* The apply swallows its errors, the cleanup does not read them, and the retry downstream is
starved. Every teardown that persists state to undo itself has this shape available to it — the kill-switch pf
token, the route belief, any owned-marker sweep.

**The test that distinguishes application from restatement:** make the underlying command fail for ONE subject,
then assert the record survives AND the next retry still repairs it. A test that only checks the happy path
cannot fail in the direction this law protects.

**Sibling of** ABSENCE MUST BE THE CLOSED STATE (above) and of the KEEP-LAST direction in the reconcile model:
when the new value cannot be established, keep the old one — do not land on empty.
