import { describe, it, expect } from "vitest";
import { remoteEnrollCommand, cpEndpoints, GATEWAY_IMAGE } from "../src/components/Gateways";

// S8.2c D4: the emitted remote-gateway command is the ZERO-TOUCH artifact — pasted verbatim on a clean VM
// it must reach agent_ready on real WireGuard with NO edits. These reds encode tonight's double
// paste-failure: it must be a SINGLE `docker run` (no compose, no line breaks) with EVERY piece the demo
// added by hand baked in.

describe("remoteEnrollCommand — the one true zero-touch docker run", () => {
  const base = cpEndpoints({ protocol: "https:", hostname: "cp.example.com", origin: "https://cp.example.com" });

  it("is a SINGLE docker run — no compose, no newlines (the paste-mismatch is structurally impossible)", () => {
    const cmd = remoteEnrollCommand({ token: "TKN", name: "gw-aws", endpoint: "203.0.113.7:51820", ...base });
    expect(cmd.startsWith("docker run ")).toBe(true);
    expect(cmd).not.toContain("docker compose");
    expect(cmd).not.toContain("\n");
    expect(cmd).not.toContain("tunnex.yml");
  });

  it("bakes in EVERY hand-fixed piece from the demo (host net, wgctrl, tun, token, CP urls, servername, image)", () => {
    const cmd = remoteEnrollCommand({ token: "TKN", name: null, endpoint: null, ...base });
    for (const piece of [
      "--network host",
      "--cap-add NET_ADMIN",
      "--device /dev/net/tun",
      "-e TUNNEX_WG_BACKEND=wgctrl",
      "-e TUNNEX_JOIN_TOKEN=TKN",
      "-e TUNNEX_API_URL=https://cp.example.com",
      "-e TUNNEX_AGENT_URL=https://cp.example.com:8443",
      "-e TUNNEX_AGENT_SERVERNAME=tunnex-control",
      GATEWAY_IMAGE,
    ]) {
      expect(cmd, `missing: ${piece}`).toContain(piece);
    }
  });

  it("endpoint present → TUNNEX_NODE_ENDPOINT set (hub); absent → omitted (NAT'd spoke)", () => {
    expect(remoteEnrollCommand({ token: "T", name: null, endpoint: "1.2.3.4:51820", ...base })).toContain("-e TUNNEX_NODE_ENDPOINT=1.2.3.4:51820");
    expect(remoteEnrollCommand({ token: "T", name: null, endpoint: null, ...base })).not.toContain("TUNNEX_NODE_ENDPOINT");
  });

  it("a name is shell-quoted (a space can't truncate it into a node_name_mismatch loop)", () => {
    expect(remoteEnrollCommand({ token: "T", name: "my gw", endpoint: null, ...base })).toContain('-e TUNNEX_NODE_NAME="my gw"');
  });

  it("cpEndpoints derives the public CP urls from the dashboard origin (REST on origin, agent :8443)", () => {
    const e = cpEndpoints({ protocol: "http:", hostname: "40.65.63.141", origin: "http://40.65.63.141" });
    expect(e.apiURL).toBe("http://40.65.63.141");
    expect(e.agentURL).toBe("https://40.65.63.141:8443");
    expect(e.serverName).toBe("tunnex-control");
  });
});
