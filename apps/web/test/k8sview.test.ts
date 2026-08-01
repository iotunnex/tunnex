import { describe, it, expect } from "vitest";
import {
  k8sGate,
  portLabel,
  assembleClusters,
  serviceFqdnById,
  objectControls,
} from "../src/lib/k8sview";
import type { K8sCluster, K8sService } from "../src/lib/api";

const CL = (id: string, name: string): K8sCluster =>
  ({
    id,
    site_id: "s1",
    name,
    vip_range: "100.64.0.0/16",
    service_cidr: "10.96.0.0/12",
    dns_zone: "k8s.acme.com",
    dns_vip: "100.64.0.2",
  }) as K8sCluster;
const SVC = (
  id: string,
  cluster: string,
  name: string,
  fqdn: string,
): K8sService =>
  ({
    id,
    cluster_id: cluster,
    name,
    namespace: "prod",
    protocol: "tcp",
    vip: "100.64.0.5",
    fqdn,
  }) as K8sService;

describe("k8sGate — CORE (no edition bit): org:view reads, k8s:manage + verified email mutates", () => {
  it("a member reads but cannot manage", () => {
    const g = k8sGate({ role: "member", emailVerified: true });
    expect(g.canView).toBe(true);
    expect(g.canManage).toBe(false);
  });
  it("an admin with a verified email manages", () => {
    const g = k8sGate({ role: "admin", emailVerified: true });
    expect(g.canView).toBe(true);
    expect(g.canManage).toBe(true);
  });
  it("an unverified admin cannot manage (mirrors the server mutating gate)", () => {
    expect(k8sGate({ role: "admin", emailVerified: false }).canManage).toBe(
      false,
    );
  });
  it("no role → neither (fail-closed)", () => {
    const g = k8sGate({ role: undefined, emailVerified: true });
    expect(g.canView).toBe(false);
    expect(g.canManage).toBe(false);
  });
});

describe("portLabel — the wire port_low/port_high projection", () => {
  it("both null/absent = any", () => {
    expect(portLabel(null, null)).toBe("any");
    expect(portLabel(undefined, undefined)).toBe("any");
  });
  it("single port (low only, or low==high)", () => {
    expect(portLabel(80, null)).toBe("80");
    expect(portLabel(80, 80)).toBe("80");
  });
  it("a range renders low–high", () => {
    expect(portLabel(8000, 8100)).toBe("8000–8100");
  });
});

describe("assembleClusters — group Services under their cluster (the wire-truth join)", () => {
  it("services attach to their own cluster; the FQDN is READ, never constructed", () => {
    const clusters = [CL("c1", "prod"), CL("c2", "staging")];
    const services = [
      SVC("k1", "c1", "api", "api.prod.svc.prod.k8s.acme.com"),
      SVC("k2", "c2", "web", "web.prod.svc.staging.k8s.acme.com"),
    ];
    const cards = assembleClusters(clusters, services);
    expect(cards).toHaveLength(2);
    expect(cards[0].services).toHaveLength(1);
    expect(cards[0].services[0].fqdn).toBe("api.prod.svc.prod.k8s.acme.com");
    expect(cards[0].dnsVip).toBe("100.64.0.2");
    // c2 got only its own Service (no cross-cluster bleed).
    expect(cards[1].services.map((s) => s.id)).toEqual(["k2"]);
  });
  it("a cluster with no exposed Services renders an empty list, not an error", () => {
    expect(assembleClusters([CL("c1", "prod")], [])[0].services).toEqual([]);
  });
});

describe("serviceFqdnById — the grant-picker label source", () => {
  const svc = [SVC("k1", "c1", "api", "api.prod.svc.prod.k8s.acme.com")];
  it("resolves a live id to its fqdn", () => {
    expect(serviceFqdnById(svc, "k1")).toBe("api.prod.svc.prod.k8s.acme.com");
  });
  it("an absent id resolves to null (the vanished case)", () => {
    expect(serviceFqdnById(svc, "gone")).toBeNull();
  });
});

describe("managed-by-operator ownership surface (S10.2 D2 cond 1)", () => {
  it("carries managed_by_operator from the wire onto clusters and services", () => {
    const cl = { ...CL("c1", "prod"), managed_by_operator: true } as K8sCluster;
    const managed = {
      ...SVC("k1", "c1", "api", "api.prod.svc.prod.k8s.acme.com"),
      managed_by_operator: true,
    } as K8sService;
    const human = {
      ...SVC("k2", "c1", "web", "web.prod.svc.prod.k8s.acme.com"),
      managed_by_operator: false,
    } as K8sService;
    const cards = assembleClusters([cl], [managed, human]);
    expect(cards[0].managedByOperator).toBe(true);
    expect(
      cards[0].services.find((s) => s.id === "k1")!.managedByOperator,
    ).toBe(true);
    expect(
      cards[0].services.find((s) => s.id === "k2")!.managedByOperator,
    ).toBe(false);
  });
});

describe("objectControls — the withhold decision (M3)", () => {
  it("withholds the destructive control on a managed object, offers it otherwise", () => {
    expect(objectControls(true).withheld).toBe(true);
    expect(objectControls(false).withheld).toBe(false);
  });
});
