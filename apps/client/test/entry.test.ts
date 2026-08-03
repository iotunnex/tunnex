import { test } from "node:test";
import assert from "node:assert/strict";

import { CLIENT_ENTRY, postServerUrlAction } from "../src/main/entry";

// ⛔ THIS FILE EXISTS BECAUSE A SOURCE CENSUS FOUND A BUG A SOURCE CENSUS HAD MISSED.
//
// Step 3 flipped the renderer entry from the web dashboard to the client's own page, and reported
// it as a one-line change. There were TWO load sites. The second — `config:setServerUrl` on the
// wasUnset branch — is the FIRST-RUN path: setup screen, server URL, load. It still said
// `index.html`, so a fresh install landed on the web dashboard and only a SECOND launch reached
// the client.
//
// It survived because nothing could run it. `ipc.ts` imports `electron` at module scope, and client
// tests import no Electron at runtime (CI sets ELECTRON_SKIP_BINARY_DOWNLOAD, which makes
// `require("electron")` throw), so `config:setServerUrl` has never been executed by a test.
//
// > **A BRANCH NO TEST CAN REACH IS NOT UNDER-TESTED, IT IS UNTESTED** — and "we have a census over
// > it" is not a substitute, because the census is the instrument that missed it the first time.
//
// So the decision moved into an electron-free module, which is this repo's standing answer to that
// constraint (trayview / notifyview did the same). It can now be RUN rather than scanned.

test("first run LOADS the client entry — reload cannot change origin from the setup data: URL", () => {
  const act = postServerUrlAction(true);
  assert.equal(act.kind, "load");
  assert.equal(act.kind === "load" && act.url, CLIENT_ENTRY);
});

test("⛔ first run does NOT load the web dashboard — the exact regression that shipped", () => {
  const act = postServerUrlAction(true);
  assert.ok(
    act.kind === "load" && !act.url.endsWith("/index.html"),
    "the first-run path is loading the web SPA's index.html again",
  );
  assert.ok(
    act.kind === "load" && act.url.endsWith("/client.html"),
    "the first-run path must load the client's own entry",
  );
});

test("a later URL change RELOADS — it must not re-navigate to the entry", () => {
  // Not a cosmetic difference: a load would discard renderer state on every server-URL edit,
  // where a reload picks up the new auth/config state in place.
  assert.deepEqual(postServerUrlAction(false), { kind: "reload" });
});

test("the entry is an app:// URL — the navigation lock rejects anything else", () => {
  // main/index.ts refuses any navigation that does not start with app://. An entry that failed this
  // would be blocked by the client's own security guard and render as a dead window.
  assert.ok(CLIENT_ENTRY.startsWith("app://"), CLIENT_ENTRY);
});
