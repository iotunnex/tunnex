# Cross-cloud site-to-site demo — RECORD (live, founder-present, Pawan drove)

**Topology:** AWS `tunnex-aws-vm` Sydney (pub `15.134.231.13`, priv `172.31.24.206`, VPC `172.31.0.0/16`) = **hub** · Azure `tunnex-azure-vm` West US (pub `172.184.125.139`, priv `10.0.0.5`) = spoke · CP/dev-VM `Tunnex-dev-vm` (priv `10.0.0.4`, same Azure VNet `10.0.0.0/24`) = control plane + a **behind-gateway host**. Agents enrolled against the **public CP** (`40.65.63.141`), fresh `cross-cloud` org, mesh (Off) mode. Everything UI-first except the inherent terminal steps (agent run, ping/tcpdump, cloud consoles).

## HEADLINE — cross-cloud site-to-site tunnel PROVEN
```
# from the AWS host, into the Azure VNet, through the Tunnex tunnel:
ping -I 172.31.24.206 -c3 10.0.0.5
3 packets transmitted, 3 received, 0% packet loss   rtt avg 138 ms
```
**AWS Sydney → Azure West US, private-to-private, un-NAT'd, 138ms** (real cross-Pacific latency — genuinely crossing clouds). The two gateways handshook over the public internet (`wg show`: fresh handshake both sides, keepalive 25s, peers exchanged with the correct site allowed-ips), routes propagated (`10.0.0.0/24 dev wg0 metric 8021` on AWS, `172.31.0.0/16 dev wg0` on Azure).

## Two S8.3 review-folds proven INCIDENTALLY on the live wire
- **#1 `peersEqual` guard (S8.3 review):** the hub LEARNED the spoke's roamed endpoint (`172.184.125.139:38048`) while its desired endpoint for that peer is empty — the exact perpetual-churn scenario — and the tunnel stayed stable (steady counters, no re-sync storm). The three-clause guard holds cross-cloud.
- **S8.3 Slice-4 polish:** the Access **summary line "Policy not enforced — open mesh"** rendered live on the cross-cloud org (the `rulesSummary` off-state).

## Findings (evidence-backed) — feed S8.2c
| # | finding | evidence | class |
|---|---|---|---|
| 1 | UI join-token emits a **compose** command, not a bare `docker run` → paste-mismatch broke the command **twice** | the two `docker: invalid reference format` errors | deploy-shape (S8.2c/#1-2) |
| 2 | bare run defaults `backend=mem` (no real WG) → needs explicit `TUNNEX_WG_BACKEND=wgctrl` | `agent_backend_selected backend:"mem"` then `wgctrl` | deploy-shape |
| 3 | **bare run needs `--network host`** to reach real host LANs — bridge traps wg0, container only has the overlay `10.99.0.1` | 100% loss in bridge; routes on host after `--network host` | deploy-shape (the big one) |
| 4 | **no remote-gateway-against-public-CP artifact** — compose assumes API+gateway co-located (`api:8080`/`api:8443`) | `deploy/tunnex.yml` service names | deploy-shape |
| 4b | **`ApplyRoutes` programs the site route with NO `src` hint** → gateway-host-originated traffic mis-sources (overlay addr) AND a manual `src` fix is **clobbered every reconcile tick** | `ip route get 10.0.0.5 → src 10.99.0.1` after a manual `src` replace | **data-plane / agent** |
| 5 | **gateway forward chain is ASYMMETRIC** — accepts `iifname wg0 …` (tunnel→LAN) but has **no `iifname != wg0 oifname wg0`** (LAN→tunnel) → a **genuinely separate behind-gateway host cannot INITIATE to a remote site** (dropped by `policy drop`) | gw-azure nft: only `iifname "wg0" …` accepts; CP→AWS 100% loss even in mesh | **data-plane / possible S8.2 defect** — severity gated on (b) |
| GAP-1 | no in-session org creation for an existing owner (`/create-org` is `RequireNoOrg`-gated) | worked around by fresh signup | UI |
| GAP-2 | **no `site → site` in the Access Add-rule builder** — Source={group,user}, Dest={group,resource}; S8.3 *displays* site rules but can't *create* them | the modal dropdowns (screenshot) | UI (headline) |
| GAP-3 | Add-rule button stays disabled after adding a group until a page refresh | live | UI (pre-existing) |

## (b) — Enforcing + site→site grant characterization (finding #5 severity)
GAP-2 workaround: the grant was inserted **directly into `policy_rules`** (both directions, `site-azure↔site-aws`) — **DEMO-ONLY: this bypasses the API's disjointness validation + audit; never a documented path.**
**Result:** _(FILL from the re-ping + gw-azure nft)_
- **replies** → the compiled enforcing rule opens LAN→tunnel → #5 is a **mesh-mode-only gap**.
- **still drops** → the compiled forward rule doesn't match forwarded LAN-source traffic → #5 is a **data-plane defect in the S8.2 compiler output** (S8.2c's first slice, regardless of all else).

## Verdict
Cross-cloud site-to-site is **real and proven** (138ms, un-NAT'd, Sydney↔West US). Getting there required **6 manual gateway touches + 3 UI gaps** — none of which a customer should ever hit. **That friction is the entire justification for S8.2c** (see the Zero-Touch Gateway Law, `docs/laws.md`). The demo's value is the proof AND the gap inventory.
