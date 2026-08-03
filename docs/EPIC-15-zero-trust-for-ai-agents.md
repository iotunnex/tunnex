# EPIC 15 — Zero Trust for AI agents

**REGISTRATION ONLY. No design, no schema, no commit-one.** Sequenced **after EPIC 14, before the beta
bundle**. Written while the thinking is fresh; everything below is measured against the live schema and the
RBAC table, not asserted.

---

# 0. THE PROBLEM, STATED WITHOUT EMBELLISHMENT

MCP servers today are deployed either on **localhost** — safe because unreachable — or on the **public
internet behind a bearer token and nothing else**. There is no network boundary and no device identity
between those two positions.

An AI agent is a **non-human principal that runs unattended, at machine rate**, and that can be
**prompt-injected into asking for the wrong thing while remaining correctly authenticated**.

⛔ **AND THAT LAST CLAUSE BOUNDS WHAT THIS EPIC CAN HONESTLY CLAIM.** Under prompt injection, authentication
is intact and authorization is intact — **only INTENT is corrupted**. Zero Trust does not detect injection and
must never be sold as though it does.

> ## **WHAT IT DOES IS BOUND THE BLAST RADIUS OF A CORRECTLY-AUTHENTICATED PRINCIPAL THAT HAS BEEN TALKED**
> ## **INTO THE WRONG REQUEST. That is a real and unmet need. It is not "we stop prompt injection."**

---

# 1. INVENTORY — what already covers this, and how closely

**MEASURED against the live schema and `rbac.go`.**

| existing capability | serves an agent unchanged? | how close, honestly |
|---|---|---|
| **Machine credentials** (`machine_credentials`: name, fixed `role='operator'`, `token_hash`, `fingerprint`) | **CLOSEST — but no** | A first-class **non-user org principal** already exists, audits as `operator:<name>`, and is mintable/revocable. This is most of an agent identity. **See the defect below.** |
| **Audit with a system actor** (`actor_system`, `InsertSystemAuditLog`) | **YES** | Non-human attribution is already first-class and the metadata carries a CAUSE. S14.15 added two writers to it. Reusable as-is. |
| **Temporary grants** (`policy_rules.expires_at`, S7.5.4) | **YES** | Expiry on a rule already exists and already sweeps. An agent grant that dies in 30 minutes needs no new mechanism. |
| **Zero Trust rules** (`src_kind`, `dst_kind`, default-deny, deterministic compiler) | **STRUCTURALLY yes** | The model is right. The **vocabulary** is not: see §2. |
| **Gateway enrollment + device identity** | **YES** | Identity↔credential binding, full-sweep revocation, reconcile loop — all transport-agnostic. |
| **Posture** (`device_health_checks`, enterprise) | **PARTLY** | The mechanism fits; the **vocabulary does not** (§2). And it is explicitly *client-reported, not attestation* — an agent inherits that weakness, and inherits it worse. |

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

# 2. WHAT IS GENUINELY NEW — the founder's three candidates, argued

## 2a · MCP server as a first-class RESOURCE TYPE — **AGREED, and the precedent already exists**

`dst_kind` is `CHECK (dst_kind IN ('resource','group','site','k8s_service'))`. **`k8s_service` is the proof**:
a *named*, non-CIDR destination type was already added to this model once and the compiler absorbed it.
`resources` are network-addressed (`cidr` + protocol + port range); `k8s_service` is the named one. An
`mcp_server` destination is **the same shape as a precedent that shipped**, not a speculative extension.

**So a rule reading `agent:X -> mcp:github-readonly` with an expiry is a vocabulary addition to a proven
model.** Lowest-risk of the three.

## 2b · AGENT as a third device type — **AGREED on the type, ARGUE with the posture**

`devices.transport` is `CHECK (transport IN ('wireguard','openvpn'))`; a third value is a CHECK change plus a
provisioning path. Mechanically small.

**The posture vocabulary is the substantive part, and the founder's framing is right:** `disk_encryption`
means nothing for an agent. *Which model · which host · which human account launched it* is the useful triple.

⛔ **BUT IT INHERITS A WEAKNESS AND MAKES IT WORSE.** Existing posture is **client-reported, not attestation**
— already recorded on that screen. A laptop misreporting its disk encryption is a user lying about their own
machine. **An agent self-reporting which model it is, is a principal we are worried about being manipulated,
being asked to describe itself.** The honest position: *which human launched it* is bindable to a real
credential and is worth something; *which model* is a self-report and must be labelled as one.

## 2c · PER-TOOL GRANULARITY — **THE PART NOBODY ELSE IS DOING, AND THE PART THAT BREAKS THE ARCHITECTURE**

`read_file` vs `delete_repo` inside one MCP server.

**MEASURED CONSTRAINT:** every enforcement mechanism in this product operates at **L3/L4** — WireGuard peer
allow-lists, `ip` rules, nftables, the DOCKER-USER accept, resources as `cidr`+`protocol`+`port`. **A network
rule cannot see inside an MCP call.** Granting the server grants every tool on it.

> ## **SO TOOL-LEVEL RULES ARE NOT AN EXTENSION OF OUR ENFORCEMENT PLANE. THEY ARE A SECOND ONE — an L7**
> ## **proxy that terminates, parses and re-emits MCP.** That is a new data-path component with its own
> ## availability, latency, versioning and failure modes, sitting in the path of every agent call.

**Feasible? Yes. Cheap? No — and it is the opposite of the rest of the epic**, where every other piece reuses
something proven. **It should be its own decide-item, and it should be the LAST slice**, so the epic ships a
real boundary before it takes on a proxy. **This is the differentiator and the risk, and they are the same
item.**

---

# 3. THE COSTS, PLAINLY

- **This is an epic, not a feature.** New principal type, new device type, new destination type, new posture
  vocabulary, UI on both sides, and — if 2c is in — a new data-path component. **Frontend and backend both.**
- **It delays beta by its own length.** That is the decision, stated as a cost rather than buried.
- ⛔ **THE MCP PROTOCOL IS MOVING FAST. Anything coupled to its wire format may be stale in six months.**

**What is protocol-INDEPENDENT — the durable core:**
the agent **principal** and its role · **grants with expiry** · **audit attribution** · **device identity and
revocation** · **default-deny between a named source and a named destination**. None of that mentions MCP.
If MCP is replaced wholesale, this survives with a renamed destination type.

**What is protocol-COUPLED — and will rot:**
**per-tool enforcement (2c)**, because it must parse the protocol; any **tool discovery/enumeration**; any
posture field describing an MCP capability set.

**The sequencing follows from that split**, and it is the paper's main recommendation: **build the
protocol-independent core first and let it stand alone. Add the coupled layer last, knowing it is the part
that will need re-doing.**

---

# 4. ⛔ POSITIONING — OUR OWN RULE, TURNED ON OURSELVES

The founder wants **"the world's first Zero Trust for AI agents."**

**That is exactly the unbuilt Tier-3 claim this epic has spent fourteen stories cutting from other people's
copy.** A claim we cannot substantiate is not improved by being about us.

> ## **THE EPIC TITLE AND THIS PAPER DESCRIBE CAPABILITY, NOT PRIMACY.**

**REGISTER ROW — the primacy claim, and what would have to be true to make it:**

| what would have to be true | how it would be established |
|---|---|
| The capability **ships and holds** — an agent principal, named MCP destinations, expiring grants, attributed audit, working on a live wire | our own box-walk, the same standard as every other epic |
| **No prior art** does this — a real survey of Tailscale, Cloudflare Access, Teleport, Pomerium, and whatever the MCP ecosystem has shipped by then | a written survey with dates and citations, not a memory |
| **Someone outside the company says it** | an analyst, a customer, or a named practitioner — **not us** |

**The rule: if we ship it and it holds, we earn the sentence. We do not open with it.** Until all three rows
are true, the claim does not appear in a title, a landing page, or a README.

---

# 5. STATUS

**REGISTERED. NOT STARTED.** No design, no schema, no commit-one. EPIC 14 continues unchanged.
