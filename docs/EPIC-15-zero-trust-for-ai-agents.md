# EPIC 15 — Zero Trust for AI agents

**REGISTRATION ONLY. No design, no schema, no commit-one.** Sequenced **after EPIC 14, before the beta
bundle**. Written while the thinking is fresh; everything below is measured against the live schema and the
RBAC table, not asserted.

---

# ⛔ 0. THE BOUNDARY, FIRST — BEFORE THE PITCH, NOT AFTER IT

> ## **UNDER PROMPT INJECTION, AUTHENTICATION IS INTACT AND AUTHORIZATION IS INTACT. ONLY *INTENT* IS**
> ## **CORRUPTED. ZERO TRUST BOUNDS THE BLAST RADIUS OF A CORRECTLY-AUTHENTICATED PRINCIPAL. IT DOES NOT**
> ## **DETECT INJECTION.**

This sits in the opening rather than the caveats **because it is the sentence under the most pressure to
soften.** Every honest version of this epic says the same thing, and every marketing version is tempted to
imply the opposite — that a boundary *catches* the manipulated request rather than *limiting what it can
reach*.

⛔ **ANY COPY CLAIMING DETECTION IS A RENDER-FLOOR VIOLATION AT PRODUCT SCALE.** The render floor says a
surface must not state more than the system knows. A landing page claiming we detect prompt injection is that
same defect with a larger blast radius than any screen: it is a promise the product cannot keep, made to
people who cannot check it. **Treat it as the same class of error as a UI that renders a number the server
never computed.**

---

# 1. THE PROBLEM, STATED WITHOUT EMBELLISHMENT

MCP servers today are deployed either on **localhost** — safe because unreachable — or on the **public
internet behind a bearer token and nothing else**. There is no network boundary and no device identity
between those two positions.

An AI agent is a **non-human principal that runs unattended, at machine rate**, and that can be
**prompt-injected into asking for the wrong thing while remaining correctly authenticated**.

**Reported scale:** 1,800+ MCP servers exposed without authentication, and the Cloud Security Alliance
published an **Agentic Trust Framework (Feb 2026)** arguing agents need identity governance as rigorous as
human users'. See §0 for what this epic can and cannot claim about that.

---

# 2. INVENTORY — what already covers this, and how closely

**MEASURED against the live schema and `rbac.go`.**

| existing capability | serves an agent unchanged? | how close, honestly |
|---|---|---|
| **Machine credentials** (`machine_credentials`: name, fixed `role='operator'`, `token_hash`, `fingerprint`) | **CLOSEST — but no** | A first-class **non-user org principal** already exists, audits as `operator:<name>`, and is mintable/revocable. This is most of an agent identity. **See the defect below.** |
| **Audit with a system actor** (`actor_system`, `InsertSystemAuditLog`) | **YES** | Non-human attribution is already first-class and the metadata carries a CAUSE. S14.15 added two writers to it. Reusable as-is. |
| **Temporary grants** (`policy_rules.expires_at`, S7.5.4) | **YES** | Expiry on a rule already exists and already sweeps. An agent grant that dies in 30 minutes needs no new mechanism. |
| **Zero Trust rules** (`src_kind`, `dst_kind`, default-deny, deterministic compiler) | **STRUCTURALLY yes** | The model is right. The **vocabulary** is not: see §3. |
| **Gateway enrollment + device identity** | **YES** | Identity↔credential binding, full-sweep revocation, reconcile loop — all transport-agnostic. |
| **Posture** (`device_health_checks`, enterprise) | **PARTLY** | The mechanism fits; the **vocabulary does not** (§3). And it is explicitly *client-reported, not attestation* — an agent inherits that weakness, and inherits it worse. |

## ⛔ THE DEFECT IN THE CLOSEST THING WE HAVE

`machine_credentials.role` is **fixed at `operator`** at mint (D3), and the `operator` role holds
**`PermPolicyManage`**.

> ## **SO THE EXISTING NON-HUMAN PRINCIPAL CAN WRITE ITS OWN ACCESS RULES. For a GitOps operator that was**
> ## **the point. For an agent that can be talked into a request, it is the whole threat model inverted —**
> ## **a compromised agent grants itself what it was denied.**

**An agent principal must be a DIFFERENT role, not a reused one** — and the repo's own convention says so:
*permissions are named per feature; never reuse an existing perm for a new capability*. This is the single
largest "already covered?" correction in the inventory, and it was found by reading the grant table rather
than by assuming the closest thing fit.

---

# 3. WHAT IS GENUINELY NEW — the founder's three candidates, argued

## 3a · MCP server as a first-class RESOURCE TYPE — **AGREED, and the precedent already exists**

`dst_kind` is `CHECK (dst_kind IN ('resource','group','site','k8s_service'))`. **`k8s_service` is the proof**:
a *named*, non-CIDR destination type was already added to this model once and the compiler absorbed it.
`resources` are network-addressed (`cidr` + protocol + port range); `k8s_service` is the named one. An
`mcp_server` destination is **the same shape as a precedent that shipped**, not a speculative extension.

**So a rule reading `agent:X -> mcp:github-readonly` with an expiry is a vocabulary addition to a proven
model.** Lowest-risk of the three.

**COST: SMALL–MEDIUM.** One `dst_kind` value + a table + spec paths + compiler arm + a UI panel. No new
enforcement plane. **SEQUENCE: FIRST — founder-ruled, and for this reason: it is the one item whose precedent
already shipped.**

## 3b · AGENT as a third device type — **AGREED on the type, ARGUE with the posture**

`devices.transport` is `CHECK (transport IN ('wireguard','openvpn'))`; a third value is a CHECK change plus a
provisioning path. Mechanically small.

**The posture vocabulary is the substantive part:** `disk_encryption` means nothing for an agent.

**COST: MEDIUM.** A CHECK value is trivial; the provisioning path, the credential binding and the posture
vocabulary are not. **SEQUENCE: SECOND.**

⛔ **AND THE FOUNDER'S OWN CANDIDATE LIST GETS CUT HERE, ON HIS RULING.** Existing posture is
**client-reported, not attestation** — already labelled that way on the screen that ships it. A laptop
misreporting its disk encryption is a user lying about their own machine. **An agent self-reporting WHICH
MODEL IT IS, is the principal we are worried about being manipulated, being asked to describe itself.**

> ## **KEEP ONLY WHAT BINDS TO A REAL CREDENTIAL: which human account launched it · which host · which**
> ## **enrollment. DROP model self-reporting, or label it EXACTLY as existing posture is —**
> ## ***client-reported, not attestation*** — **and never let a rule depend on it.**

The three that survive are all bindable to something we issued and can revoke. *Which model* is bindable to
nothing, which is precisely why it is attractive to put on a screen and useless as a control.

## 3c · PER-TOOL GRANULARITY — **THE PART NOBODY ELSE IS DOING, AND THE PART THAT BREAKS THE ARCHITECTURE**

`read_file` vs `delete_repo` inside one MCP server.

**MEASURED CONSTRAINT:** every enforcement mechanism in this product operates at **L3/L4** — WireGuard peer
allow-lists, `ip` rules, nftables, the DOCKER-USER accept, resources as `cidr`+`protocol`+`port`. **A network
rule cannot see inside an MCP call.** Granting the server grants every tool on it.

> ## **SO TOOL-LEVEL RULES ARE NOT AN EXTENSION OF OUR ENFORCEMENT PLANE. THEY ARE A SECOND ONE — an L7**
> ## **proxy that terminates, parses and re-emits MCP.** That is a new data-path component with its own
> ## availability, latency, versioning and failure modes, sitting in the path of every agent call.

**AND THE ANSWER TO *"are we terminating MCP traffic, or enforcing at the network layer only?"* IS THE WHOLE
QUESTION — THOSE ARE DIFFERENT PRODUCTS.** Network-only is what we are today and what every existing mechanism
supports. Terminating MCP makes us a protocol proxy, with a parser in the path of every agent call.

**COST: LARGE, and structurally different from the other two.** A new data-path component with its own
availability, latency, versioning and failure modes — plus ongoing protocol churn (§4).
⛔ **AND TELEPORT SHIPPED THIS IN DEC 2025** (§5), so it is a catch-up item, not a differentiator. The first
draft of this paper called it *"the part nobody else is doing"*; that was wrong and unchecked.

**SEQUENCE: LAST — founder-ruled, and the reason is mine: ship a real boundary before putting a parsing proxy
in the path of every agent call.** If it is never built, the epic still delivers a working boundary.

---

# 4. THE COSTS, PLAINLY

- **This is an epic, not a feature.** New principal type, new device type, new destination type, new posture
  vocabulary, UI on both sides, and — if 3c is in — a new data-path component. **Frontend and backend both.**
- **It delays beta by its own length.** That is the decision, stated as a cost rather than buried.
- ⛔ **THE MCP PROTOCOL IS MOVING FAST. Anything coupled to its wire format may be stale in six months.**

**What is protocol-INDEPENDENT — the durable core:**
the agent **principal** and its role · **grants with expiry** · **audit attribution** · **device identity and
revocation** · **default-deny between a named source and a named destination**. None of that mentions MCP.
If MCP is replaced wholesale, this survives with a renamed destination type.

**What is protocol-COUPLED — and will rot:**
**per-tool enforcement (3c)**, because it must parse the protocol; any **tool discovery/enumeration**; any
posture field describing an MCP capability set.

**The sequencing follows from that split**, and it is the paper's main recommendation: **build the
protocol-independent core first and let it stand alone. Add the coupled layer last, knowing it is the part
that will need re-doing.**

---

# 5. ⛔ THE CATEGORY IS NOT EMPTY — MEASURED FACT, AND IT REPLACES WHAT THIS PAPER FIRST SAID

**This section is a CORRECTION.** The first draft treated primacy as *unproven and therefore deferred*, and
built a register row listing what would have to be true to earn it. **That framing was wrong, and generously
wrong in our own favour: the claim is not unproven, it is CHECKABLE AND FALSE.**

| who | what they already ship | when |
|---|---|---|
| **Versa Networks** | markets **"Industry's first Zero Trust MCP Server"** | ~May 2026 |
| ⛔ **Teleport** | **protocol-level MCP access control down to INDIVIDUAL TOOL INVOCATIONS**, deny-new-tools-by-default, JIT access for high-risk tools | Dec 2025 |
| **Octelium** | FOSS, self-hostable **architectural twin** — WireGuard + QUIC, unified identity for humans, workloads **and AI agents**, per-request ABAC, L7-aware policy, names MCP and A2A explicitly | shipping |
| Pomerium · AccuKnox · TrueFoundry | also shipping in the space | — |

> ## **SO WE DO NOT MAKE A PRIMACY CLAIM. NOT "not yet" — NOT AT ALL. Someone else already made it, and ours**
> ## **would be checkable and false the day it was published.**

⛔ **AND TELEPORT IS ALREADY WHERE §3c GOES.** Per-tool granularity is not our unclaimed differentiator; it is
**the thing a competitor shipped eight months before this paper was written.** The first draft called it *"the
part nobody else is doing"* — repeating the founder's framing without checking it. **That is the Tier-3 defect
this epic has spent fourteen stories cutting from other people's copy, committed in our own paper, about our
own product.**

## THE ACTUAL REASON TO BUILD, STATED PLAINLY

Not to claim the space — the space is occupied. **Two reasons, and they are enough:**

1. **Beta is more compelling with agent support than without it.** A ZT product that cannot describe an agent
   principal in 2026 looks unfinished, regardless of who was first.
2. **Our existing model already carries most of the shape** — §2 measures how much. The marginal cost of the
   protocol-independent core is low *because it is mostly vocabulary on mechanisms that already ship*.

**Neither reason requires us to be first, and neither is weakened by Teleport being ahead on §3c.**

## WHAT SURVIVES OF THE POSITIONING RULE

The three conditions from the first draft **stand as the standing test for any comparative claim we ever
make** — it ships and holds on a live wire · a real prior-art survey with dates and citations, not a memory ·
**someone outside the company says it.**

⛔ **The correction is that condition 2 was RUN, and it FAILED.** That is what the condition is for. **A rule
that has never rejected anything is not a rule** — this one just rejected our own headline, which is the only
evidence that it works.

---

# 6. STATUS

**REGISTERED. NOT STARTED.** No design, no schema, no commit-one. EPIC 14 continues unchanged.
