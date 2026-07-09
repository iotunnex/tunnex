import net from "node:net";

// TS side of the S6.3 helper wire protocol. Framing MUST match apps/helper/ipc.go:
// a 4-byte big-endian length prefix + that many JSON bytes. Keep the two in sync.

export const PROTOCOL_VERSION = 1;
export const MAX_MESSAGE_BYTES = 64 * 1024;

export type AuthMode = "path_check" | "code_signing";
export type Verb = "tunnel_up" | "tunnel_down" | "status";

export interface TunnelConfig {
  private_key: string;
  peer_public_key: string;
  endpoint: string;
  address: string;
  allowed_ips: string[];
  dns?: string[];
  mtu?: number;
  persistent_keepalive?: number;
}

export interface HelperRequest {
  version: number;
  auth_mode: AuthMode;
  verb: Verb;
  config?: TunnelConfig;
}

export interface TunnelStatus {
  state: "down" | "up" | "failed";
  interface?: string;
  last_handshake_sec?: number;
  rx_bytes?: number;
  tx_bytes?: number;
}

export interface HelperResponse {
  version: number;
  ok: boolean;
  code?: string;
  error?: string;
  status?: TunnelStatus;
}

// encodeFrame length-prefixes a JSON value. Throws if it would exceed the cap
// (the helper rejects oversize frames too — fail before writing).
export function encodeFrame(value: unknown): Buffer {
  const body = Buffer.from(JSON.stringify(value), "utf8");
  if (body.length > MAX_MESSAGE_BYTES) throw new Error("message exceeds MAX_MESSAGE_BYTES");
  const hdr = Buffer.alloc(4);
  hdr.writeUInt32BE(body.length, 0);
  return Buffer.concat([hdr, body]);
}

// FrameDecoder accumulates bytes and yields complete JSON messages. It enforces
// the size cap on the advertised length before allocating.
export class FrameDecoder {
  private buf = Buffer.alloc(0);

  push(chunk: Buffer): unknown[] {
    this.buf = Buffer.concat([this.buf, chunk]);
    const out: unknown[] = [];
    for (;;) {
      if (this.buf.length < 4) break;
      const len = this.buf.readUInt32BE(0);
      if (len > MAX_MESSAGE_BYTES) throw new Error("incoming frame exceeds MAX_MESSAGE_BYTES");
      if (this.buf.length < 4 + len) break;
      const body = this.buf.subarray(4, 4 + len);
      out.push(JSON.parse(body.toString("utf8")));
      this.buf = this.buf.subarray(4 + len);
    }
    return out;
  }
}

// HelperClient is a single-shot request/response over the helper's local socket
// (unix socket on macOS, named pipe on Windows — the path is platform-resolved by
// the caller). The token is never involved here; this channel carries only the
// typed tunnel protocol.
export class HelperClient {
  constructor(private readonly socketPath: string) {}

  request(req: HelperRequest, timeoutMs = 15000): Promise<HelperResponse> {
    return new Promise((resolve, reject) => {
      const sock = net.connect(this.socketPath);
      const decoder = new FrameDecoder();
      let settled = false;
      const done = (fn: () => void) => {
        if (settled) return;
        settled = true;
        sock.destroy();
        fn();
      };
      const timer = setTimeout(() => done(() => reject(new Error("helper request timed out"))), timeoutMs);

      sock.on("connect", () => {
        try {
          sock.write(encodeFrame(req));
        } catch (e) {
          clearTimeout(timer);
          done(() => reject(e as Error));
        }
      });
      sock.on("data", (chunk: Buffer) => {
        let msgs: unknown[];
        try {
          msgs = decoder.push(chunk);
        } catch (e) {
          clearTimeout(timer);
          return done(() => reject(e as Error));
        }
        if (msgs.length > 0) {
          clearTimeout(timer);
          done(() => resolve(msgs[0] as HelperResponse));
        }
      });
      sock.on("error", (e) => {
        clearTimeout(timer);
        done(() => reject(e));
      });
      sock.on("close", () => {
        clearTimeout(timer);
        done(() => reject(new Error("helper connection closed before a response")));
      });
    });
  }
}
