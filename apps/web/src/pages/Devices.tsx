import { useEffect, useState, type FormEvent } from "react";
import { PRODUCT_NAME } from "../brand";
import { api, CSRF, apiErrorMessage, type Device, type Node, type Org } from "../lib/api";
import { Button, Card, ErrorText, Field, Input, StatusDot } from "../components/ui";

// lastSeen renders honest recency ("last seen 42s ago"), never a faked live claim
// — WireGuard only knows the last handshake time (online is derived from it).
function lastSeen(at?: string): string {
  if (!at) return "never connected";
  const s = Math.max(0, Math.floor((Date.now() - new Date(at).getTime()) / 1000));
  if (s < 60) return `last seen ${s}s ago`;
  if (s < 3600) return `last seen ${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `last seen ${Math.floor(s / 3600)}h ago`;
  return `last seen ${Math.floor(s / 86400)}d ago`;
}

export default function Devices() {
  const [org, setOrg] = useState<Org | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [fullTunnel, setFullTunnel] = useState(false);
  const [config, setConfig] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [busy, setBusy] = useState(false);

  async function loadDevices(orgId: string) {
    const { data, error } = await api.GET("/api/v1/organizations/{orgId}/devices", { params: { path: { orgId } } });
    if (error) {
      setError(apiErrorMessage(error, "Could not load devices."));
      return;
    }
    setDevices(data ?? []);
  }

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const { data: orgs, error: orgErr } = await api.GET("/api/v1/organizations");
        if (cancelled) return;
        if (orgErr) {
          setError(apiErrorMessage(orgErr, "Could not load your organizations."));
          return;
        }
        const first = orgs?.[0];
        if (!first) {
          setError("You are not a member of any organization yet.");
          return;
        }
        setOrg(first);
        const { data: ns, error: nodeErr } = await api.GET("/api/v1/organizations/{orgId}/nodes", {
          params: { path: { orgId: first.id } },
        });
        if (cancelled) return;
        if (nodeErr) {
          setError(apiErrorMessage(nodeErr, "Could not load gateway nodes."));
          return;
        }
        setNodes(ns ?? []);
        if (!cancelled) await loadDevices(first.id);
      } catch {
        if (!cancelled) setError("Could not reach the API.");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  async function create(e: FormEvent) {
    e.preventDefault();
    if (!org || nodes.length === 0) return;
    setBusy(true);
    setError(null);
    setConfig(null);
    const { data, error } = await api.POST("/api/v1/organizations/{orgId}/devices", {
      params: { path: { orgId: org.id } },
      headers: CSRF,
      body: { name, node_id: nodes[0].id, full_tunnel: fullTunnel },
    });
    setBusy(false);
    if (error || !data) {
      setError(apiErrorMessage(error, "Could not create the device."));
      return;
    }
    setName("");
    setConfig(data.config ?? null); // shown once — the private key is never re-served
    await loadDevices(org.id);
  }

  async function revoke(id: string) {
    if (!org) return;
    setError(null);
    const { error } = await api.POST("/api/v1/organizations/{orgId}/devices/{deviceId}/revoke", {
      params: { path: { orgId: org.id, deviceId: id } },
      headers: CSRF,
    });
    if (error) {
      setError(apiErrorMessage(error, "Could not revoke the device."));
      return;
    }
    await loadDevices(org.id);
  }

  async function copy() {
    if (!config) return;
    try {
      await navigator.clipboard.writeText(config);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard denied — the user can still select/download */
    }
  }

  function download() {
    if (!config) return;
    // The private key is served exactly once, so this download must not fail:
    // the anchor is attached to the DOM (Firefox ignores clicks on detached
    // anchors) and the object URL is revoked on the next tick (not synchronously,
    // which can abort the save before the browser reads the Blob).
    const url = URL.createObjectURL(new Blob([config], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = `${PRODUCT_NAME}.conf`;
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Devices</h1>
          <p className="text-sm text-slate-400">{org ? org.name : "…"}</p>
        </div>
      </div>

      <ErrorText>{error}</ErrorText>

      <form onSubmit={create} className="mt-6">
        <Card>
          <div className="flex flex-wrap items-end gap-3">
            <div className="min-w-[12rem] flex-1">
              <Field label="New device name">
                <Input value={name} onChange={(e) => setName(e.target.value)} required placeholder="my-laptop" />
              </Field>
            </div>
            <label className="flex items-center gap-2 text-sm text-slate-300">
              <input type="checkbox" checked={fullTunnel} onChange={(e) => setFullTunnel(e.target.checked)} />
              Full tunnel
            </label>
            <Button type="submit" disabled={busy || nodes.length === 0}>
              {busy ? "Creating…" : "Create device"}
            </Button>
          </div>
          {nodes.length === 0 && (
            <p className="mt-3 text-xs text-amber-400">No gateway node is enrolled yet — enroll one to create devices.</p>
          )}
        </Card>
      </form>

      {/* The one-time config CEREMONY: the most security-sensitive moment in the
          app. A deliberate modal (amber, blocks the page) — the config exists only
          in page state, is never re-fetched (the private key is served exactly
          once), and must be explicitly acknowledged ("I've saved it") to dismiss.
          Navigating away also discards it. */}
      {config && (
        <div className="fixed inset-0 z-50 grid place-items-center bg-ink-950/80 p-4">
          <div className="w-full max-w-lg rounded-xl border-2 border-warn/60 bg-ink-800 p-5 shadow-2xl">
            <div className="flex items-center gap-2">
              <StatusDot tone="warn" />
              <span className="text-sm font-semibold text-warn">Your configuration — shown once</span>
            </div>
            <p className="mt-2 text-xs text-slate-400">
              This file contains your device's <span className="text-warn">private key</span>. It is shown{" "}
              <span className="font-semibold">exactly once</span> and cannot be retrieved again — save it now.
            </p>
            <pre className="mt-3 max-h-64 overflow-auto rounded-md bg-ink-950 p-3 font-mono text-xs text-slate-300">
              {config}
            </pre>
            <div className="mt-4 flex items-center justify-between">
              <div className="flex gap-2">
                <Button onClick={download}>Download {PRODUCT_NAME}.conf</Button>
                <Button variant="ghost" onClick={copy}>
                  {copied ? "Copied" : "Copy"}
                </Button>
              </div>
              <Button variant="ghost" onClick={() => setConfig(null)}>
                I&rsquo;ve saved it
              </Button>
            </div>
          </div>
        </div>
      )}

      <ul className="mt-6 space-y-2">
        {devices.map((d) => (
          <li key={d.id} className="flex items-center justify-between rounded-lg border border-white/5 bg-ink-800 px-4 py-3">
            <div>
              <span className="text-sm text-white">{d.name}</span>
              <span className="ml-2 font-mono text-xs text-slate-500">{d.assigned_ip ?? "—"}</span>
              {d.status === "revoked" ? (
                <span className="ml-2 text-xs text-rose-400">revoked</span>
              ) : (
                <span className="ml-2 inline-flex items-center gap-1 text-xs text-slate-400">
                  <StatusDot tone={d.online ? "on" : "off"} />
                  {lastSeen(d.last_handshake_at)}
                </span>
              )}
            </div>
            {d.status === "active" && (
              <Button variant="danger" onClick={() => revoke(d.id)}>
                Revoke
              </Button>
            )}
          </li>
        ))}
        {devices.length === 0 && <li className="text-sm text-slate-500">No devices yet.</li>}
      </ul>
    </div>
  );
}
