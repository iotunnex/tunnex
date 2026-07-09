import { test } from "node:test";
import assert from "node:assert/strict";

import { encodeFrame, FrameDecoder, MAX_MESSAGE_BYTES, PROTOCOL_VERSION } from "../src/main/helperclient";
import { helperSocketPath } from "../src/main/tunnel";

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
