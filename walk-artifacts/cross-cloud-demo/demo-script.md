# Cross-cloud site-to-site demo — AWS Sydney ↔ Azure West US (founder demo)

**The real thing:** two gateways in two clouds, two different clouds' private subnets, joined by a Tunnex site-to-site tunnel through the existing control plane — a host in AWS's VPC reaches a host in Azure's VNet, under enforcing Zero Trust. **Runs AFTER the S8.3 merge, on `main`.** Pawan at both cloud consoles; I drive leg-by-leg.

## Cast
| role | host | public IP | private | notes |
|---|---|---|---|---|
| **Control plane** | existing box | `40.65.63.141` | — | the CP + web (enterprise); enroll both gateways against its PUBLIC API |
| **Gateway A (hub)** | `tunnex-aws-vm` (AWS, Sydney) | `15.134.231.13` | `172.31.24.206` | VPC `172.31.0.0/16` · **hub** (public UDP endpoint) |
| **Gateway B (spoke)** | `tunnex-azure-vm` (Azure, West US) | `172.184.125.139` | `<AZURE_PRIV_IP>` | VNet `<AZURE_VNET_PREFIX>` — **fill from Pawan's first paste** |

**Hub designation is deliberate:** the hub = the endpoint-bearing gateway (`electSiteHub` picks the one with a public UDP endpoint). We make **Gateway A the hub** by opening UDP 51820 to it and advertising its endpoint; the Azure spoke dials the hub. (If BOTH have public UDP open, the lower node-id wins — so designate ONE on purpose to avoid ambiguity.)

**Architecture note (honesty, LOG-EVERY-STEP):** both agents install on the cloud VMs directly against the CP's PUBLIC API — the SAME enrollment path as any customer gateway (no docker-compose stack cloud-side, just the `tunnex-node` container). **The container→host-LAN routing question applies HERE too:** the VPC/VNet subnet lives behind the host's NIC, NOT inside the container, so the gateway container must route+NAT to the host subnet. Every such command is **docs/S8.5 input** — S8.5 compiles routed ranges → forward rules automatically; today it's manual.

---

## Pre-flight (both consoles — paste outputs; STOP on any surprise)
**Cloud-fabric (each step LOGGED as S8.5/deploy input):**
1. **AWS security group:** allow **UDP 51820** inbound to `15.134.231.13` (from the Azure public IP, or 0.0.0.0/0 for the demo). Allow ICMP for the ping proof.
2. **AWS source/dest check OFF** on the AWS instance ENI (a gateway forwards traffic not addressed to itself — AWS drops it otherwise). Log it.
3. **Azure IP-forwarding ON** on the Azure VM NIC (same reason, Azure side). Log it.
4. **Azure NSG:** allow ICMP + the return path; the spoke dials out so no inbound UDP needed on Azure.
5. Confirm reachability: from Azure `ping -c1 15.134.231.13` (hub public) succeeds.

**Agent install (both VMs, against the CP public API):**
```
# per VM: run the tunnex-node container, TUNNEX_API_URL=https://40.65.63.141 (public CP),
# TUNNEX_AGENT_SERVERNAME=<CP cert SAN>, enroll with a join token issued from the UI.
```
Pre-flight PASS = both agents enrolled + reporting; hub shows a public endpoint, spoke does not.

---

## Setup (product path — Sites UI, owner)
1. **Register two sites**, bind Gateway A → `site-aws`, Gateway B → `site-azure`.
2. **Advertise + approve** each cloud's private subnet:
   - `site-aws`: `172.31.0.0/16` (or a scoped `/24` around `172.31.24.206`).
   - `site-azure`: `<AZURE_VNET_PREFIX>` (from Pawan's paste).
   - **Disjointness handled in-script:** AWS `172.31.x` vs Azure VNet must be disjoint (they are, different clouds). If either overlaps the device pool (`10.99.x`) or each other, scope to a `/24`. Approve → `site.subnet_approved` each.
3. **Hub confirm:** the Sites page shows Gateway A with the **HUB** badge (backend-elected), Gateway B a spoke.
4. **Gateway container→host routing/NAT (the hand-fiddle — LOG EACH):** on each VM, ensure the gateway container forwards + masquerades to its host subnet (`net.ipv4.ip_forward=1`; masquerade out the host NIC for the peer subnet; a route to the host LAN). **Every command = S8.5 input.**

## Proof legs
1. **Routed-but-dropped (ordering matters — do this BEFORE the grant):** enforcing, NO site→site grant → from an AWS host `ping <azure-host-in-VNet>` → the packet is WG-carried to the hub and **DROPPED** at the forward chain (routing ≠ permission). Capture the hub `default_drop` counter.
2. **Grant → cross-cloud ping:** Access → rule `src_kind=site (site-aws) → dst_kind=site (site-azure)` (and/or the reverse) → the SAME ping now **replies**. **pcap BOTH sides** (tcpdump on AWS egress + Azure ingress) showing the ICMP crossing the WG tunnel — first cross-CLOUD packet.
3. **`site_link_down` live:** stop the hub (or the spoke link) → after the staleness window the Sites card flips to **`site_link_down`** / `site_hub_down`; restart → clears. (Keepalive keeps a healthy idle link warm — the S8.3 Slice-0 rider, so the badge means a real dead link.)
4. **Un-NAT'd invariant:** confirm the site-to-site traffic keeps its real LAN source (masquerade scoped `oifname != wg0`) — the transit accept matches the real VPC/VNet source, not a NAT'd address.

## Verdict + S8.5 input
- Record: did AWS Sydney reach Azure West US through the tunnel (ping + pcap both sides)? drop-then-grant flipped on exactly the grant? `site_link_down` live?
- **The manual-step list** (SG/source-dest-check/IP-forwarding/container-NAT/routes) IS S8.5's scope evidence — S8.5 automates the routed-range → forward-rule compilation so a customer never runs these by hand.

---
*Fill `<AZURE_PRIV_IP>` + `<AZURE_VNET_PREFIX>` from Pawan's first paste at pre-flight. Screenshots/pcaps → this dir. Runs post-merge on main.*
