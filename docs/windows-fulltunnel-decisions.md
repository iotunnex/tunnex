# Windows full-tunnel — decisions (guard + parity + kill-switch persistence)

Full-tunnel is proven on macOS (S3.7 + the two `backend_darwin.go` fixes + S6.8). Windows is
NOT ready, but is currently **connectable-but-unsafe** on main: S6.5a shipped the Windows `.exe`,
the S6.4 full-tunnel toggle is platform-agnostic, and the only server-side refusal
(`gateway_no_egress`, `devices/service.go:155`) gates on the GATEWAY's capability, not the client
OS — so a Windows client + an egress-capable Linux gateway is NOT refused. Combined with the two
Windows defects below, a Windows user who ticks "route all traffic" today gets a tunnel that
(a) can't resolve names and (b) leaks cleartext if the process dies.

This doc covers three pieces, decision-first. **Nothing lifts the Windows full-tunnel refusal
until BOTH build stories land AND the `kill -9` pcap passes live on a real Windows box.**

---

## 0. GUARD FIRST (standalone, urgent) — refuse Windows full-tunnel in code

**Ship before any of the build work.** Today Windows full-tunnel is reachable + broken on main.

- **Hard guard (the gate): `backend_windows.go` `Up()` refuses `cfg.FullTunnel`** with a typed
  `ProtocolError{Code: "full_tunnel_unsupported"}` BEFORE creating the adapter or arming WFP —
  so nothing is half-built and the root helper is the un-bypassable arbiter. Split tunnel is
  unaffected. Platform-scoped by the `//go:build windows` file, so macOS is untouched.
- **Negative test (windows-latest CI): `backend_windows_test.go`** asserts `Up(fullConfig())`
  returns `full_tunnel_unsupported` and that NOTHING was armed/created (no adapter, WFP not
  enabled). This is the assertion that turns "unproven" into "refused."
- **Soft add (UX, can ride the parity story): the client grays the full-tunnel toggle on Windows
  with a "coming soon on Windows" note** so a user isn't refused only at connect time. The hard
  guard is the helper; the UI is courtesy.
- **Lift condition (single, explicit):** delete the `Up()` refusal ONLY when Story A + Story B
  below are merged AND the Story-B `kill -9` pcap passes live. Until then it stays.

The `gateway_no_egress` capability guard stays as-is (it is correct and orthogonal — gateway
capability vs client platform).

---

## Story A — Windows full-tunnel PARITY (traffic correctness)

Make full-tunnel actually carry traffic + resolve names on Windows. `apps/helper/backend_windows.go`
only; mirrors the two macOS fixes.

1. **DNS apply (the confirmed gap).** `Up()` never applies `cfg.DNS`, so the resolver stays on the
   WFP-blocked LAN DNS → names don't resolve (ping-by-IP works, everything else fails — the exact
   macOS symptom). Fix, using the OFFICIAL mechanisms (cleaner than macOS):
   - `winipcfg.LUID(luid).SetDNS(family, cfg.DNS)` on the wintun adapter — adapter-scoped, so it
     **auto-vanishes when the adapter is torn down**. No `/var/run` backup, no crash-restore, no
     strand (unlike macOS `networksetup`).
   - Change `firewall.EnableFirewall(luid, false, nil)` → `EnableFirewall(luid, true, dnsAddrs)`.
     The 2nd param is `restrictDNS`; `false` lets Windows leak DNS out other interfaces (smart
     multi-homed resolution) even with the block armed. `true` + the tunnel resolver forces DNS
     through the tunnel only — closes the leak the same way the official WG Windows client does.
   - IPv6: pass v6 DNS iff a v6 default is in AllowedIPs; otherwise v6 stays dropped (matches the
     macOS "no NAT66 → drop, don't leak" posture).
2. **Kill-switch over-block (verify, likely already correct).** Unlike macOS's hand-rolled pf
   anchor, Windows uses the official `firewall.EnableFirewall`, which permits the tunnel adapter
   LUID — so the macOS `set skip` class of bug should NOT exist. Do NOT assume: PROVE it with the
   live egress test (the macOS bug was invisible in code review too).
3. **Proof (live, real Windows box, against the S3.7 gateway):** full tunnel → `Resolve-DnsName`
   works, `curl ifconfig.me` = the GATEWAY's public IP (egress via gateway NAT), a browser loads.
   Deliberate-red carries over (flush the gateway masquerade → egress dies). Record like
   `docs/S3.7-egress-proof.md`.

Story A alone makes Windows full-tunnel WORK, but it does NOT make it SAFE — the block still
evaporates on process death (Story B). So the guard does not lift on A alone.

---

## Story B — S6.7 Windows kill-switch PERSISTENCE (fail-closed on death)

The security-critical piece. Today the WFP block is armed on a wireguard-windows session that
uses `FWPM_SESSION_FLAG_DYNAMIC` → the OS auto-deletes every filter/sublayer when the process
(session) exits. So on crash / `kill -9` the block is GONE — `CleanStale`/`DisableFirewall` on
the next start find nothing to remove because the OS already tore it down, and traffic reverts to
cleartext (pcap-proven leak). The header comment in `backend_windows.go` claiming "kernel-resident,
survives process death" is aspirational and WRONG; fix the code, then the comment.

- **First step — CONFIRM the session flag** in the vendored `wireguard-windows/tunnel/firewall`
  package (grep the module cache for `FWPM_SESSION_FLAG_DYNAMIC` / the `wfpSession` creation).
  The fix is only correct once the actual persistence mechanism is confirmed on the box, not
  assumed from the ledger.
- **Fix (decision):** arm the WFP objects on a **non-dynamic (persistent) session** under a
  **fixed provider + sublayer GUID** (our own, stable across processes), instead of the official
  package's dynamic session. Then:
  - `CleanStale` (startup self-heal) **enumerates and deletes every object under our fixed GUID**
    — the GUID is the durable key that now actually survives a prior crash.
  - `Down` removes them by the same GUID (graceful).
  - This likely means **forking/vendoring the small `firewall` arm/disable routines** (or
    reimplementing the handful of `FwpmFilterAdd`/sublayer calls) so we control the session flag +
    GUIDs — the upstream package deliberately uses a dynamic session tied to its own service model.
- **Reboot-recovery safety net:** a persistent sublayer that outlives a crash must also survive a
  **reboot** without permanently bricking connectivity. Decide: either (a) the fixed-GUID objects
  are cleared by the helper's `CleanStale` at every service start (the service is `RunAtLoad`, so a
  reboot → service start → CleanStale → clean slate before serving), or (b) a boot-time WFP filter
  that self-expires. Prefer (a) — it reuses the existing startup self-heal and needs no boot magic.
  MUST prove: hard-reboot a box mid-full-tunnel → after boot, the service starts, releases the
  stale block, and the host has normal connectivity (not permanently blocked).
- **Proof (the LIFT gate):** on a real Windows box, full tunnel up → `kill -9` the helper →
  **pcap on the physical NIC shows NO cleartext egress** (the block held past death) → next helper
  start (`CleanStale`) un-strands → connectivity returns. This pcap is the single gate that lifts
  the §0 guard. Plus the reboot-recovery proof above.

---

## One story or two? — TWO, gated by ONE guard

- **Two stories** because A (adapter DNS + WFP restrictDNS) and B (WFP session lifetime + GUID +
  CleanStale) are independent subsystems with independent review + independent live proofs; folding
  them hides the security-critical B inside a correctness change.
- **One guard, one lift-trigger:** the §0 helper refusal stays until A **and** B merge and B's pcap
  (+ reboot recovery) pass live. Neither story lifts it alone — A makes it work, B makes it safe;
  offering a working-but-leaky full tunnel is worse than refusing.

**Ordering:**
1. **§0 guard — NOW, standalone PR.** Makes Windows full-tunnel un-connectable + safe immediately.
2. **Story A (parity)** — cheap, unblocks usability; live egress proof.
3. **Story B (S6.7 persistence)** — security-critical; box-proven, reviewed, `kill -9` pcap +
   reboot recovery.
4. **Lift the guard** — delete the `Up()` refusal + the client toggle disable, in a small PR whose
   description links the passing pcap. Re-run the full Windows full-tunnel proof after the lift to
   confirm the now-unguarded path still works end to end.

Both A and B are box-proven-on-Windows before they merge (no blind WFP changes). The S6.8
orphan/quit logic is Supervisor-level (platform-agnostic) and already applies once the guard lifts.
