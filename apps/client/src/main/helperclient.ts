import net from "node:net";

// TS side of the S6.3 helper wire protocol. Framing MUST match apps/helper/ipc.go:
// a 4-byte big-endian length prefix + that many JSON bytes. Keep the two in sync.

export const PROTOCOL_VERSION = 1;
export const MAX_MESSAGE_BYTES = 64 * 1024;

export type AuthMode = "path_check" | "code_signing";
export type Verb = "tunnel_up" | "tunnel_down" | "status" | "posture_status";

export interface TunnelConfig {
  private_key: string;
  peer_public_key: string;
  endpoint: string;
  address: string;
  allowed_ips: string[];
  // full_tunnel intent: when true the helper requires both 0.0.0.0/0 AND ::/0
  // present (no single-family leak). Mirrors apps/helper TunnelConfig.
  full_tunnel?: boolean;
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
  // The device's assigned tunnel address (from the loaded config, e.g.
  // "10.99.0.2/32"). MAIN attaches it — it's config, not a helper runtime stat.
  address?: string;
}

// PostureStatus mirrors apps/helper PostureStatus (S7.5.3): read-only local
// posture facts. null/undefined = the helper could not determine the fact —
// reported ABSENT upstream, never guessed.
export interface PostureStatus {
  disk_encrypted?: boolean | null;
}

export interface HelperResponse {
  version: number;
  ok: boolean;
  code?: string;
  error?: string;
  status?: TunnelStatus;
  posture?: PostureStatus;
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

// HelperConnection is a PERSISTENT request/response channel to the helper's local
// socket. It is deliberately NOT single-shot: the connection that brings a tunnel
// up must stay OPEN for the tunnel's lifetime, because the helper treats the
// owning connection dropping as app-death and fails the tunnel closed. Responses
// are matched to requests FIFO (the helper answers one request per connection in
// order). An UNEXPECTED close (helper died) invokes onLost so the UI can surface
// it. The token/key are never involved here — only the typed tunnel protocol.
export class HelperConnection {
  private sock: net.Socket | null = null;
  private connecting: Promise<void> | null = null;
  private decoder = new FrameDecoder();
  private waiters: Array<{ resolve: (r: HelperResponse) => void; reject: (e: Error) => void }> = [];
  private closedByUs = false;

  constructor(
    private readonly socketPath: string,
    private readonly onLost?: () => void,
  ) {}

  private ensure(): Promise<void> {
    if (this.sock && !this.sock.destroyed) return Promise.resolve();
    if (this.connecting) return this.connecting;
    this.closedByUs = false;
    this.decoder = new FrameDecoder();
    this.connecting = new Promise((resolve, reject) => {
      const sock = net.connect(this.socketPath);
      sock.on("connect", () => {
        this.sock = sock;
        this.connecting = null;
        resolve();
      });
      sock.on("data", (chunk: Buffer) => this.onData(chunk));
      sock.on("error", (e) => {
        this.connecting = null;
        reject(e);
      });
      sock.on("close", () => this.onClose());
    });
    return this.connecting;
  }

  private onData(chunk: Buffer): void {
    let msgs: unknown[];
    try {
      msgs = this.decoder.push(chunk);
    } catch (e) {
      this.failAll(e as Error);
      this.sock?.destroy();
      return;
    }
    for (const m of msgs) {
      this.waiters.shift()?.resolve(m as HelperResponse);
    }
  }

  private onClose(): void {
    const hadSock = this.sock !== null;
    this.sock = null;
    this.failAll(new Error("helper connection closed"));
    if (hadSock && !this.closedByUs) this.onLost?.(); // an UNEXPECTED drop = helper death
  }

  private failAll(e: Error): void {
    while (this.waiters.length) this.waiters.shift()!.reject(e);
  }

  async request(req: HelperRequest, timeoutMs = 15000): Promise<HelperResponse> {
    await this.ensure();
    return new Promise<HelperResponse>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("helper request timed out")), timeoutMs);
      this.waiters.push({
        resolve: (r) => {
          clearTimeout(timer);
          resolve(r);
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      });
      try {
        this.sock!.write(encodeFrame(req));
      } catch (e) {
        clearTimeout(timer);
        // Pop the waiter we just pushed and fail it.
        const w = this.waiters.pop();
        w?.reject(e as Error);
      }
    });
  }

  // close is the INTENTIONAL teardown (graceful disconnect / app quit): it marks
  // the close as expected so onLost does NOT fire.
  close(): void {
    this.closedByUs = true;
    this.sock?.destroy();
    this.sock = null;
  }
}
