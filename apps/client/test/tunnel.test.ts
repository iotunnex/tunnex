import { test } from "node:test";
import assert from "node:assert/strict";
import net from "node:net";
import os from "node:os";
import path from "node:path";
import fs from "node:fs";

import { encodeFrame, FrameDecoder, HelperConnection, MAX_MESSAGE_BYTES, PROTOCOL_VERSION, type TunnelConfig } from "../src/main/helperclient";
import { helperSocketPath, TunnelController } from "../src/main/tunnel";

const delay = (ms: number) => new Promise((r) => setTimeout(r, ms));

// The framing MUST match apps/helper/ipc.go (4-byte BE length + JSON body). These
// tests pin the wire contract on the TS side so the two can't silently diverge.
test("encodeFrame writes a 4-byte big-endian length prefix", () => {
  const frame = encodeFrame({ a: 1 });
  const body = Buffer.from(JSON.stringify({ a: 1 }), "utf8");
  assert.equal(frame.readUInt32BE(0), body.length);
  assert.deepEqual(frame.subarray(4), body);
});

test("FrameDecoder reassembles a message split across chunks", () => {
  const frame = encodeFrame({ version: PROTOCOL_VERSION, verb: "status" });
  const dec = new FrameDecoder();
  // Feed it one byte at a time — nothing yields until the full frame arrives.
  for (let i = 0; i < frame.length - 1; i++) {
    assert.equal(dec.push(frame.subarray(i, i + 1)).length, 0);
  }
  const out = dec.push(frame.subarray(frame.length - 1));
  assert.equal(out.length, 1);
  assert.deepEqual(out[0], { version: PROTOCOL_VERSION, verb: "status" });
});

test("FrameDecoder yields multiple messages from one chunk", () => {
  const two = Buffer.concat([encodeFrame({ n: 1 }), encodeFrame({ n: 2 })]);
  const out = new FrameDecoder().push(two);
  assert.deepEqual(out, [{ n: 1 }, { n: 2 }]);
});

test("oversize frames are rejected before allocation, both directions", () => {
  const big = "x".repeat(MAX_MESSAGE_BYTES + 1);
  assert.throws(() => encodeFrame({ big }), /MAX_MESSAGE_BYTES/);
  // A hostile length prefix (> cap) must throw on decode without allocating it.
  const evil = Buffer.alloc(4);
  evil.writeUInt32BE(MAX_MESSAGE_BYTES + 1, 0);
  assert.throws(() => new FrameDecoder().push(evil), /MAX_MESSAGE_BYTES/);
});

test("helperSocketPath is platform-specific", () => {
  assert.equal(helperSocketPath("win32"), "\\\\.\\pipe\\tunnex-helper");
  assert.equal(helperSocketPath("darwin"), "/var/run/tunnex/helper.sock");
});

// The helper reports runtime stats but NOT the tunnel address (it's config), so
// MAIN attaches it — this is what lets the UI show "Your IP". Guard the plumb.
test("TunnelController attaches the config's tunnel address to forwarded status", async () => {
  const sockPath = path.join(os.tmpdir(), `tnx-addr-test-${process.pid}.sock`);
  try { fs.unlinkSync(sockPath); } catch { /* fresh */ }
  const server = net.createServer((sock) => {
    const dec = new FrameDecoder();
    sock.on("data", (chunk: Buffer) => {
      // The helper never sends `address`; main must add it.
      for (const _ of dec.push(chunk)) sock.write(encodeFrame({ version: PROTOCOL_VERSION, ok: true, status: { state: "up", last_handshake_sec: 3 } }));
    });
  });
  await new Promise<void>((r) => server.listen(sockPath, r));
  try {
    const cfg = { address: "10.99.0.2/32" } as unknown as TunnelConfig;
    const ctrl = new TunnelController(sockPath, async () => cfg);
    const up = await ctrl.up();
    assert.equal(up.address, "10.99.0.2/32", "main must attach the config's tunnel address");
    await ctrl.down();
  } finally {
    server.close();
    try { fs.unlinkSync(sockPath); } catch { /* gone */ }
  }
});

test("HelperConnection: persistent round-trip, intentional close is quiet, unexpected close fires onLost", async () => {
  const sockPath = path.join(os.tmpdir(), `tnx-helper-test-${process.pid}.sock`);
  try { fs.unlinkSync(sockPath); } catch { /* fresh */ }

  const serverSockets: net.Socket[] = [];
  const server = net.createServer((sock) => {
    serverSockets.push(sock);
    const dec = new FrameDecoder();
    sock.on("data", (chunk: Buffer) => {
      for (const msg of dec.push(chunk)) {
        const req = msg as { verb: string };
        const state = req.verb === "tunnel_down" ? "down" : "up";
        sock.write(encodeFrame({ version: PROTOCOL_VERSION, ok: true, status: { state } }));
      }
    });
  });
  await new Promise<void>((r) => server.listen(sockPath, r));

  try {
    // Persistent round-trip: two requests over ONE held connection, FIFO-matched.
    let lost = false;
    const conn = new HelperConnection(sockPath, () => { lost = true; });
    const up = await conn.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "tunnel_up" });
    assert.equal(up.ok, true);
    assert.equal(up.status?.state, "up");
    const st = await conn.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "status" });
    assert.equal(st.status?.state, "up");

    // Intentional close → onLost must NOT fire (this is graceful, not app-death).
    conn.close();
    await delay(50);
    assert.equal(lost, false, "intentional close must be quiet");

    // Reconnect, then simulate HELPER DEATH (server destroys the socket) → onLost.
    let lost2 = false;
    const conn2 = new HelperConnection(sockPath, () => { lost2 = true; });
    await conn2.request({ version: PROTOCOL_VERSION, auth_mode: "path_check", verb: "status" });
    serverSockets.forEach((s) => s.destroy());
    await delay(50);
    assert.equal(lost2, true, "unexpected drop must fire onLost (helper death)");
    conn2.close();
  } finally {
    await new Promise<void>((r) => server.close(() => r()));
    try { fs.unlinkSync(sockPath); } catch { /* gone */ }
  }
});
