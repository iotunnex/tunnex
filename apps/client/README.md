# Tunnex desktop client

Electron shell + the privileged helper. The renderer is served over `app://` from a bundled SPA build.

---

## FIRST: verify the tree you are standing in

**Two clones caused two separate confusions this epic** — a stale served bundle, and then a reported SIGKILL
that turned out to be a ten-story-old branch with no client work in it. Run this before anything else:

```bash
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
echo "clone:  ${ROOT:-NOT A GIT REPO}"
echo "branch: $(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
echo "head:   $(git rev-parse --short HEAD 2>/dev/null)"
for f in apps/client/package.json apps/web/client.html apps/web/src/client/ClientApp.tsx; do
  [ -f "$ROOT/$f" ] && echo "  yes  $f" || echo "  NO   $f"
done
E="$ROOT/node_modules/.pnpm/electron@31.7.7/node_modules/electron/dist/Electron.app"
[ -d "$E" ] || E="$(find "$ROOT/node_modules" -maxdepth 8 -name Electron.app -type d 2>/dev/null | head -1)"
[ -n "$E" ] && echo "  yes  Electron.app" || echo "  NO   Electron.app — run: pnpm install"
(cd "$ROOT/apps/client" && npx electron --version 2>&1 | tail -1)
```

A healthy tree prints every `yes` and ends with a version. **`NO Electron.app` means `pnpm install` has not
run in THIS clone** — it does not mean anything is wrong with the app.

---

## ⚠ WITHDRAWN: a "Gatekeeper SIGKILL" section used to live here

**A SIGKILL was reported on launch and this README diagnosed it as Gatekeeper killing the ad-hoc-signed
Electron binary. THAT DIAGNOSIS IS WITHDRAWN — it was never reproduced.**

**What actually happened:** the report came from a DIFFERENT CLONE on a branch ten stories old, which had no
client work and no `Electron.app` at all. The binary was missing, not blocked.

> ⛔ **THE CAUSE WAS INFERRED FROM THE SYMPTOM'S NAME.** "SIGKILL on an unsigned Electron" is a real and
> well-known macOS failure, so it fitted — and I wrote a fix, with working commands, for a cause I had
> explicitly said I could not reproduce. **Real commands around an unconfirmed cause read as evidence.** A
> plausible diagnosis is not a measured one, and shipping it as documentation makes it the next person's
> starting assumption.

**If a genuine SIGKILL appears on a verified tree**, these are the things to check — kept as a lead, NOT as an
established remedy:

```bash
xattr -p com.apple.quarantine "$E"      2>/dev/null || echo "no quarantine"
codesign --verify --strict "$E"         && echo "signature intact"
```

and only if quarantine is present or verification fails:

```bash
xattr -cr "$E"
codesign --force --deep --sign - "$E"
```

⚠ `spctl -a -vv "$E"` reports **rejected on a perfectly working install** — it asks whether the app would pass
Gatekeeper for DISTRIBUTION, which an ad-hoc signature never does. It is not the check that tells you whether
it runs. **`npx electron --version` is.**

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
