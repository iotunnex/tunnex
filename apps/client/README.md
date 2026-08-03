# Tunnex desktop client

Electron shell + the privileged helper. The renderer is served over `app://` from a bundled SPA build.

---

## ⛔ FIRST RUN ON macOS: Electron may be SIGKILLed before your code runs

**Symptom:** `pnpm --filter @tunnex/client start` exits immediately with **SIGKILL / `Killed: 9`** and no
stack trace. **This is not a code error** — Gatekeeper is killing the Electron binary that pnpm unpacked into
`node_modules`, before your main process is ever entered.

**Why it happens.** Electron's prebuilt `Electron.app` is **ad-hoc signed** (`flags=0x2(adhoc)`) — not
Developer ID. If the download also carries the **quarantine** attribute, macOS refuses to execute it. Whether
you get the quarantine bit depends on how the tarball reached your disk, which is why **this fails on some
machines and not others**, and why it is easy to ship a dev path that "works on my machine".

**The fix — run both, in this order:**

```bash
E="$(node -p "require('electron')" 2>/dev/null | xargs dirname)/dist/Electron.app"
# or find it: find node_modules/.pnpm -maxdepth 6 -name Electron.app -type d | head -1

xattr -cr "$E"                          # clear quarantine (safe if there is none)
codesign --force --deep --sign - "$E"   # re-apply the ad-hoc signature
```

Then `pnpm --filter @tunnex/client start` again.

**Both commands are safe to run when nothing is wrong** — `xattr -cr` exits 0 with nothing to clear, and the
re-sign replaces an ad-hoc signature with an equivalent one. **Re-run them after any `pnpm install` that
touches Electron**, because a fresh unpack restores the original state.

**Verify rather than guess:**

```bash
xattr -l "$E"                    # empty = no quarantine
codesign --verify --strict "$E"  # exit 0 = signature intact
npx electron --version           # the real test
```

⚠ `spctl -a -vv "$E"` will say **rejected** even on a working install. That is expected: `spctl` asks
"would Gatekeeper allow this for distribution", and an ad-hoc signature never passes that. **It is not the
check that tells you whether it will run locally** — `npx electron --version` is.

> ⛔ **THIS IS THE SIGNING GAP SHOWING UP IN DEVELOPMENT.** The client has no code signing, no notarization
> and no entitlements — registered as a **distribution** blocker. This symptom is the same absence in a
> different costume: **a new developer cannot run the client without knowing an undocumented workaround.**
> See `docs/REGISTER-nonhuman-principal-defects.md`.

---

## Run it

```bash
COMPOSE_PROJECT_NAME=tunnex-s141 make up-enterprise   # a stack to point at
pnpm --filter @tunnex/web build                        # the renderer the client loads
pnpm --filter @tunnex/client build                     # main + preload (tsc → dist)
pnpm --filter @tunnex/client start                     # electron .
```

⚠ **`pnpm --filter @tunnex/client build` is `tsc -b`, which is INCREMENTAL.** A green local build means
"whatever tsc chose to re-check is green", not "the build is green" — a clean CI container can fail on the
same tree. Delete `*.tsbuildinfo` if you need the real answer.

## Test / typecheck

```bash
pnpm --filter @tunnex/client typecheck
pnpm --filter @tunnex/client test    # node --test; imports NO electron at runtime
```

Client tests must never `require("electron")` at runtime — CI sets `ELECTRON_SKIP_BINARY_DOWNLOAD`, so the
import throws. Pure view-models live in Electron-free modules (`trayview.ts`, `notifyview.ts`).

## Package

```bash
bash apps/client/scripts/pack.sh [mac|win]
# 1 web build → 2 tsc → 3 stage-helper → 4 electron-builder → 5 SHA256SUMS
```

**Unsigned and un-notarized.** macOS: Gatekeeper will warn (curl without quarantine, or
Settings → Open Anyway). Windows: SmartScreen will warn. Both are the registered signing gate, not a defect
in the build.
