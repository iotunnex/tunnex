# THE WIREFRAME, EXTRACTED — the visual specification for EPIC 14

**Source: `docs/design/TUNNEX-wireframe-v2.html.txt`. Extracted 2026-08-01 by shell, never by dumping the file
into context.**

## ⚠ HOW THIS DOCUMENT CAME TO EXIST, RECORDED ACCURATELY

**The founder wrote *"do not read the wireframe's contents for design detail"* scoped to the registration
session, then later ruled *"KEEP the palette, glassmorphism, card-and-panel, sectioned nav, THE COPY
verbatim"* — which requires reading it. THE STALE INSTRUCTION WAS CARRIED FORWARD, and the founder has
recorded that as their own, not the assistant's.**

**LIFTED PERMANENTLY:** read the wireframe for design detail on every screen slice, **by extraction**.

> ## ⛔ ITS DOM IS **NOT** A MARKUP SPECIFICATION.
> **Take layout, hierarchy, spacing, colour and copy. Take NONE of its structure.**

The artifact is a **bundler output**: 426 lines, 3.04 MB, of which one line is a 2.5 MB asset map and another
is the rendered document as a JSON-escaped string. It is **inline-styled with no utility classes and no CSS
custom properties** — so there is nothing to copy structurally even if we wanted to.

**Extraction recipe (re-runnable):**

```python
line = open(W).read().split('\n')[423]     # the rendered document, JSON-escaped
html = json.loads(line)                     # ~498 KB of real markup
```

---

# 1. PALETTE — **WARM MONOCHROME. NOT THE VIOLET ACCENT CURRENTLY SHIPPING.**

**This is the single largest visual divergence and it is not a nuance.** S14.1 copied the *existing app's*
violet (`#7c5cff`) forward. **The wireframe has no violet at all.**

| role | value | uses |
|---|---|---|
| text primary | `#F5F5F5` | 151 |
| text secondary | `#A9A9A6` | **384** |
| text muted | `#858582` | 326 |
| text dim | `#5E5E5B` | 241 |
| surface base | `#1A1A1A` | 240 |
| surface raised | `#2E2E2E` | 228 |
| page background | `#101010` | 37 |
| **accent (muted green)** | **`#6E9C7C`** | 33 |
| borders | `rgba(255,255,255,0.14)` (186×) · `rgba(255,255,255,0.09)` (57×) | |

**Status hues** (badges): `#c77474`/`#9a5757` red family · `#c39a4e`/`#cbae72` amber family · `#6e9c7c` green ·
`#1c7c3f` deep green. **Every one is desaturated** — there is no pure `#22c55e` or `#ef4444` anywhere.

---

# 2. THE GLASS SURFACE — one recipe, used everywhere

```css
background: rgba(31,31,31,.72);
backdrop-filter: blur(24px) saturate(140%);
-webkit-backdrop-filter: blur(16px);
border: 1px solid rgba(255,255,255,0.14);
border-radius: 14px;
box-shadow: 0 10px 30px rgba(0,0,0,.3);
padding: 14px;        /* stat cards */   16px /* panels */
display: flex; flex-direction: column; gap: 8px;   /* 10px on panels */
```

**Radii in use:** `14px` (cards/panels, 18×) · `99px` (pills, 24×) · `8px`/`9px` (icon chips) · `7px`/`6px`.

---

# 3. TYPOGRAPHY — `Instrument Sans`, and the scale is COMPACT

| use | spec |
|---|---|
| stat number | `700 26px` `#F5F5F5` |
| panel title | `600 13.5px` `#F5F5F5` |
| stat label | `500 11px` `#858582` |
| **stat sub-line** | `500 10px` `#5E5E5B` or `#A9A9A6` |
| section label | `10px`, `letter-spacing: .4px`, uppercase |

**`10px` is the most common size in the document (90×).** The design is **denser than what currently ships** —
our `text-sm`/`text-xs` defaults are larger than the artifact.

---

# 4. LAYOUT GRID — measured, not guessed

| grid | uses | meaning |
|---|---|---|
| **`8fr 4fr`, gap 12px** | **6×** | **THE DOMINANT PAGE LAYOUT** — main column + right rail |
| `repeat(4,1fr)`, gap 12px | 3× | stat rows |
| `repeat(12,1fr)`, gap 12px | 1× | the 12-column base; panels span (`grid-column: span 3`) |
| `1fr 1fr` gap 12px · `repeat(3,1fr)` gap 10px · `7fr 5fr` · `1.15fr 400px` | | panel pairs / trios |

**Gap is `12px` almost everywhere, `10px` inside dense panels.** There is **no `max-width` container** on the
dashboard — it fills the viewport.

---

# 5. THE STAT CARD — exact composition

```
┌ glass card, padding 14px, gap 8px ─────────────┐
│  [icon chip 30×30, r8, bg white/.09, border]   │  ← flex row, gap 9px
│  LABEL   500 11px #858582                      │
│                                                 │
│  VALUE   700 26px #F5F5F5   (+ "/ 6" 600 13px #5E5E5B for ratios)
│                                                 │
│  SUB-LINE  500 10px #5E5E5B                    │
└────────────────────────────────────────────────┘
```

**THE SUB-LINE IS STRUCTURAL, NOT DECORATION.** Every card has one, and it carries the *qualification*:

| card | label | value | **sub-line** |
|---|---|---|---|
| Members | Members | 48 | `↑4 vs last 7 days` |
| Devices | Devices | 129 | `3 awaiting approval` |
| Gateways | Gateways | `6 / 6` | `4 reporting degraded kinds` |
| **Online Peers** | Online Peers | 83 | **`seen in last {{ liveWindow }} min`** |
| Sites | Sites | 4 | `1 link down` |
| Access Rules | Access Rules | 27 | `enforcing · 2 temp grants` |

## ⚠ AND THIS CHANGES THE "PEERS ONLINE" RULING — RAISING IT RATHER THAN OVERRIDING IT

**The wireframe puts the honest qualifier in the SUB-LINE**: label `Online Peers`, sub-line
`seen in last N min`. **That composition is arguably honest** — the claim and its basis are adjacent, and the
sub-line is a template placeholder, so the author intended it live.

**The current ruling is to keep `Seen in last 3 min` as the LABEL.** That remains in force and is *safer*.
**But the wireframe's shape is not the violation I described it as** — it qualifies in the line below rather
than not at all. **Founder's call; I am not changing it unilaterally.**

---

# 6. WHAT THE DASHBOARD ACTUALLY CONTAINS — verified present in the artifact

`Site-Link Throughput` · `Peer Connection Status` · `Recent Activity` · `Device Posture` · `Needs Attention` ·
`System Health` · `Network map` · `HA Hub Set` · `Access Rules` · `Online Peers` — **all confirmed by offset,
not from memory.**

---

# 7. ⛔ UNBUILT **LAYOUT** vs UNBUILT **PRODUCT** — so the next comparison does not re-raise it

**Founder-ruled: state which is which. A destination drawn for a capability that is not there is the same
violation as a chart drawn for an endpoint that is not there.**

| wireframe element | endpoint / screen exists? | verdict |
|---|---|---|
| stat cards, sub-lines, icons | yes | **UNBUILT LAYOUT — mine to close now** |
| `8fr 4fr` grid, glass, palette, type scale | n/a | **UNBUILT LAYOUT** |
| System Health panel | `/healthz` | **UNBUILT LAYOUT** |
| Device Posture donut | `/health-checks`, device posture fields | **UNBUILT LAYOUT** |
| Needs Attention | composed from pending/subnets/nodes | **UNBUILT LAYOUT** |
| Network map | `/sites` + `siteLinkGraph` | **UNBUILT LAYOUT** (S14.4-Sites slice) |
| HA Hub Set | `/sites`, hub fields | **UNBUILT LAYOUT** |
| nav: Routed Ranges | `/routed-ranges` ✓ screen ✗ | **UNBUILT PRODUCT — later story** |
| nav: Groups | `/groups` ✓ screen ✗ | **UNBUILT PRODUCT** |
| nav: Access Events | `/access-events` ✓ screen ✗ | **UNBUILT PRODUCT** |
| **nav: Operations** | **neither** | **UNBUILT PRODUCT — drawing it is a render-floor violation** |
| Site-Link Throughput | **spec forbids the field's use this way** | **ROADMAP — never build** |
| Fleet risk | Tier-3, not built | **ROADMAP** |

---

# 8. WHAT THIS MEANS FOR THE TOKEN SET

**S14.1's `dark` theme is the OLD app's palette, not the wireframe's.** Adopting the wireframe means either a
third theme or re-pointing `dark`. **That is a decide-item, not a fold** — it changes every rendered colour in
the product, and S14.1's contrast gate must be re-run against the new values (the warm greys are lower-contrast
than the current set and **may not clear 4.5:1** on some pairs).

---

# 9. ⛔ THE WIREFRAME'S DIMMEST TEXT **FAILS THE ACCESSIBILITY GATE WE ALREADY BUILT AND PROVED**

**Measured with S14.1's own `contrastRatio()` against the extracted values:**

| pair | ratio | floor | verdict |
|---|---|---|---|
| `#F5F5F5` primary on card | 15.96 | 4.5 | PASS |
| `#A9A9A6` secondary on card | 7.39 | 4.5 | PASS |
| `#858582` stat **label** on card | **4.70** | 4.5 | PASS — *barely* |
| **`#5E5E5B` stat SUB-LINE on card** | **2.68** | **4.5** | ⛔ **FAILS** |
| `#6E9C7C` / `#C39A4E` / `#C77474` badges | 5.56 / 6.67 / 5.12 | 3.0 | PASS |

**TWO STANDING RULINGS COLLIDE HERE, and neither yields quietly:**

- *"KEEP the wireframe's look and feel"*
- *"WCAG 2.1 AA is the floor; the contrast test **fails the build**, it does not warn"* — a gate that is
  already mutation-proven.

**Adopting `#5E5E5B` for sub-lines verbatim would require weakening a gate we deliberately made unweakenable.**
And the sub-line is not decoration — §5 shows it carries the *qualification* (`seen in last N min`,
`3 awaiting approval`), which is precisely the text that must remain readable.

**RECOMMENDED, for the founder's ruling: raise the dimmest tone to the minimum that clears 4.5:1** and keep
everything else verbatim. `#858582` already clears at 4.70. **The visual delta is one step of grey; the
alternative is shipping unreadable qualifiers or disarming an accessibility gate.**

**NOT DECIDED UNILATERALLY. Building with the raised tone and flagging it here, so a side-by-side against the
artifact shows one intentional difference rather than an unexplained one.**
