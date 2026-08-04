# REGISTER — defects in HOW THE PRODUCT REACHES THE USER, not in what it does

**Against the CURRENT shipped product. None of these belong to the epic that found them.**

A defect in this register is one where **the server is correct and the user still gets the wrong thing.**
That is why it needs its own file: every other register here asks *is this in scope*, *when does this
happen*, or *what can this principal do*. This one asks **does the correct answer actually arrive**, and
nothing about the code under review can answer it.

**HOW TO USE IT:** `grep -i '<name>' docs/REGISTER-shipped-delivery-defects.md` before concluding that a
deploy reached anyone. **HOW TO ADD:** the defect, its blast radius on the SHIPPED product, before/after
evidence you actually ran, and **how it was found** — the last one is the reusable part.

---

## ⛔ 1 — `index.html` HAD NO `Cache-Control`, SO A CORRECT DEPLOY COULD NOT REACH A RETURNING USER

**Status: FIXED on `story/S15.1-owned-machine-principal` (`12257866`, `deploy/nginx/spa.conf`).
Independent of EPIC 15 — it must not be read as part of that slice.**

### What shipped

```nginx
location /assets/ { expires 1y; add_header Cache-Control "public, immutable"; }
location /        { try_files $uri $uri/ /index.html; }   # ← no cache directive at all
```

The hashed assets are pinned `immutable` for a year — correct, they are content-addressed. The **entry
document carried no `Cache-Control` and no `Expires`**, only an ETag.

With no explicit freshness a cache MAY apply a heuristic (RFC 9111 §4.2.2); the widely-implemented one is
**10% of the time since `Last-Modified`**, served **without revalidating**:

| age of the build the user last loaded | stale SPA served with **zero** network requests |
| --- | --- |
| 1 hour | ~0.1 h |
| 1 day | ~2.4 h |
| 1 week | ~16.8 h |
| 1 month | ~72 h |

### Blast radius on the shipped product

**Every Tunnex upgrade left returning users on the superseded SPA**, and the two directives compounded:
the stale `index.html` names the old bundle, and that bundle is pinned `immutable` for a year, so the
browser reconstructs the previous release **making no requests at all**. Nothing server-side can dislodge
it — not a rebuild, not a restart, not a new release. The user has to know to hard-reload, which means the
recovery path for a delivery defect was *the user already suspecting there was one*.

⚠ **Worse for a self-hosted product**: the operator who upgrades is the same person who then reports that
the new feature is missing, and every server-side check they run says the deploy is fine — because it is.

### Before / after — measured, both directions

The fix is one line: `add_header Cache-Control "no-cache";` on `location /`.
`no-cache`, **not** `no-store` — revalidate before reuse, so the ETag still yields a 304 and a ~700-byte
document is re-sent only when it changed.

⛔ **A HEADER THAT HAS ONLY EVER BEEN PRESENT IS INDISTINGUISHABLE FROM ONE THAT DOES NOTHING**, so it was
removed from the running container and re-measured before being restored:

| state | `Cache-Control` on `/` | conditional GET |
| --- | --- | --- |
| with the fix | `no-cache` | **304** |
| directive deleted, `nginx -s reload` | **absent — header count 0** | (heuristic applies; no revalidation required) |
| restored by **rebuild**, not by re-editing | `no-cache` | **304** |

Restored via `docker compose up -d --build --force-recreate web` deliberately: the image is the source of
truth, and re-editing the live file would have proved the running container agreed with itself.

### ⭐ HOW IT WAS FOUND — the reusable part

The reviewer's browser showed a pre-S15.1b screen. Two causes were proposed, and **both were wrong**:

- **A — the containers were never rebuilt** (the prior S14.13 cause, and the more likely one).
- **B — the fix under way had dropped the feature.**

The founder's instruction was to **measure which, and not to rebuild first** — because *a rebuild fixes the
symptom under either cause and destroys the ability to tell them apart.* That instruction is the entire
finding. Under A the rebuild would have been reported as the fix, the register row would never have been
written, and the defect would have shipped to every user.

What the measurement returned:

| check | result |
| --- | --- |
| branch tip / tree | `bbd5f9c5`, clean |
| served hash, both stacks | `index-B7qQkDXb.js` — the one already reported |
| served bundle: the slice's four strings | 1 each — **present** |
| served bundle: `never used` (the copy the reviewer read) | **0 — absent** |
| web container assets | **only** the current hash |
| three prior hashes over `:80` | **404, 404, 404** |

Every server-side answer was *correct*. Neither hypothesis survived, **and the evidence was still
readable** — it named a third cause neither had considered.

> ## **A CORRECTLY-RUN CHECK AIMED AT THE WRONG SUBJECT, ONE LEVEL UP.** The known instance of that law is a
> ## check run against the wrong object. This is the same error at the level of the HYPOTHESIS SET: both
> ## candidates concerned *what the server holds*, and the defect was in *what the client asks for*. When
> ## every hypothesis is refuted, the next move is not to pick the least-refuted one — it is to notice that
> ## the question was scoped to the wrong layer.

### ⚠ AND THE BUNDLE HASH WAS THE WRONG MEASUREMENT — WRITE THIS DOWN

The founder asked, reasonably, for the served hash to be re-verified as **CHANGED** after the fix. Under
cause A or B that is exactly right. **Under this cause it does not apply: the bundle was never wrong, and
the hash was identical before and after (`index-B7qQkDXb.js`).** The fix is in a response header; the only
thing that can verify it is the header.

> ## **"VERIFY THE HASH CHANGED" TESTS THAT A NEW ARTEFACT WAS BUILT. IT CANNOT TEST WHETHER AN UNCHANGED
> ## ARTEFACT NOW REACHES THE USER.** The next person will be handed that check as a rule. It is a good rule
> ## for the cause it was written for. Producing a changed hash here — by touching the bundle to make the
> ## check pass — would have destroyed the evidence that the bundle was innocent.

### Not covered by this fix

- **A browser that already holds a stale `index.html` will not see the new header** until it revalidates —
  the header cannot reach a client that is not asking. One hard reload, once, per already-affected browser.
- **The Electron client** ships its own copy of the bundle over `app://` and does not go through this nginx
  at all. Whether it has the equivalent problem is **unmeasured** — registered here, not answered.
- **No release-notes or version-mismatch surface.** The app cannot tell a user that it is running an older
  build than the API it is talking to. Separate gap, not opened here.
