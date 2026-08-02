# THE DEFERRAL REGISTER — one line per deferral, each with a NAMED TRIGGER.

**Split out of `docs/CUT-REGISTER.md` on 2026-08-02, founder-ordered, on that file's own founding rationale:
a register works because a grep is cheap, and it stops working when it holds two different questions.**

> ## **A CUT ANSWERS "IS THIS IN SCOPE?". A DEFERRAL ANSWERS "WHEN DOES THIS HAPPEN?".**
> ## **A DEFERRAL WITHOUT A TRIGGER IS NOT DEFERRED. IT IS DROPPED, SLOWLY.**

**HOW TO USE IT:** `grep -i '<name>' docs/DEFERRAL-REGISTER.md` before assuming something is unbuilt by
choice. **HOW TO ADD:** the deferral, its trigger, why it is deferred, **where it was FOUND, and whether it
has been REVIEWED.** Provenance is part of the entry — an item whose origin nobody can name gets re-litigated
from scratch.

---

| deferral | trigger | why deferred | **found where** | **reviewed?** |
|---|---|---|---|---|
| **`site_id` on `RoutedRange`** | **an org crosses ~50 sites**, OR any story that revisits what `/routed-ranges` may carry | `/routed-ranges` is a **device-facing projection** — *ranges only, no keys, endpoints, pool or policy*. Adding an org-structure field needs a decision about whether a DEVICE should learn site topology, which is not a screen's call. Until then attribution is a per-visit fan-out | S14.7 commit-one, endpoint census | **NO** — paper only, not yet reviewed |
| **The ~50-site fan-out tripwire** (Routed Ranges `SITE` column) | **51 requests / ~9 waves at 6-per-origin.** Fires when an org's site count approaches 50 | The fan-out is correct and cheap at realistic N. It is recorded as a **THRESHOLD, not a limit**, so the next reader inherits the number instead of rediscovering it at a customer | S14.7 commit-one, after the founder asked what happens at 50 | **NO** — paper only |
| **`Modal` has no Escape / focus-trap / initial-focus / focus-return** | the next slice that touches `Modal`, or S14.8 | shared primitive, 20 call sites, and it DECLARES `aria-modal="true"` while implementing none of it | S14.5, founder-ordered measurement after I reported only *"no Escape"* from a single grep. All four behaviours then measured | **NO — REGISTERED, NEVER REVIEWED.** Not fixed, not looked at on a screen |
| **`site_link_down` is an org-level headline printed per row** | the next control-plane story touching site-link health | suppressing a server-owned verdict client-side is the one-truth violation already swept off Sites | S14.5 Sites map (N=1, meaningless), evidence upgraded S14.6 Gateways (N=6, four rows incl. the hub) | **Founder SAW it** on both screens and ruled *register, do not resolve* — the DEFECT is reviewed, the FIX is unruled |
| **The peer/device count column** (Gateways) | its own slice | spec + codegen ×3 + drift guard + both editions + query-lint + sqlc | S14.6 commit-one; founder corrected my "one cheap query" estimate | **Founder ruled it its own slice.** Scope reviewed, not built |
| **`Histogram` has no shipping consumer** | EPIC 14 close | Access Events moved REDESIGN → BUILD, so the clock got LONGER — which is how a deferral becomes permanent | S14.3 build (named Access Events as consumer); flagged S14.5 when the nav audit moved Access Events REDESIGN → BUILD | **NO** — never reviewed; the component exists only in the gallery |
| **Access screen's em-dashes** | the Access section pass | **MEASURED S14.7:** `policyview.ts:436` *"Rule status unavailable — refresh."* and `:442` *"Policy not enforced — open mesh."*, both asserted in `accesswiring.test.tsx:103,144`. **Those two assertions WILL break when that section clears its em-dashes — known in advance rather than discovered** | S14.7, censusing the em-dash blast radius across the component tier | **NO** — measured, not looked at |

