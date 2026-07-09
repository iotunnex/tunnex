import { test } from "node:test";
import assert from "node:assert/strict";

import { messageFor } from "../src/main/notify";
import { trayMenuModel } from "../src/main/tray";

// notify + tray carry no Electron calls at module load (Notification/Tray are only
// touched inside methods), so their PURE view-models are unit-testable here without
// an Electron main process — the wiring itself is live-verified at S6.5a packaging.

test("notify: every tunnel event has non-empty, distinct copy", () => {
  const events = ["connected", "disconnected", "failed", "revoked"] as const;
  const titles = new Set<string>();
  for (const ev of events) {
    const { title, body } = messageFor(ev);
    assert.ok(title.length > 0 && body.length > 0, `${ev} has copy`);
    titles.add(title);
  }
  assert.equal(titles.size, events.length); // no two events share a title
  // The revoked message must actually say "revoked" — a revoked device disconnects
  // LOUDLY (watch-item #1), not with a generic "disconnected".
  assert.match(messageFor("revoked").body, /revoked/i);
  assert.match(messageFor("failed").body, /kill-switch/i);
});

test("tray: menu model offers the right actions per state", () => {
  // Connected → only Disconnect.
  assert.deepEqual(trayMenuModel("connected"), { statusLabel: "Connected", showConnect: false, showDisconnect: true });
  // Disconnected → only Connect.
  assert.equal(trayMenuModel("disconnected").showConnect, true);
  assert.equal(trayMenuModel("disconnected").showDisconnect, false);
  // Failed (kill-switch active) → BOTH: reconnect or tear the kill-switch down.
  const failed = trayMenuModel("failed");
  assert.ok(failed.showConnect && failed.showDisconnect);
  assert.match(failed.statusLabel, /kill-switch/i);
  // Revoked → reconnect only (the dead config was already cleared).
  const revoked = trayMenuModel("revoked");
  assert.ok(revoked.showConnect && !revoked.showDisconnect);
  assert.match(revoked.statusLabel, /revoked/i);
});
