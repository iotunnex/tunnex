import { useEffect, useState } from "react";
import { createTunnexClient, type components } from "@tunnex/shared";

const api = createTunnexClient("/");
// The CSRF guard only requires this header to be present on state-changing
// requests carrying the session cookie (a value a cross-site form can't set).
const CSRF = { "X-Tunnex-CSRF": "1" };

type Device = components["schemas"]["Device"];
type Org = components["schemas"]["Organization"];
type Node = components["schemas"]["Node"];

/**
 * Bare device-management page (S3.4). It is intentionally unstyled beyond the
 * foundation shell — the design system arrives in S4.1 — but it is REAL: the
 * first UI consumer of the whole API stack (login → orgs → nodes → devices →
 * config download), driven entirely by the generated typed client.
 */
export default function App() {
  const [user, setUser] = useState<{ email: string } | null>(null);
  return (
    <div className="min-h-full flex flex-col">
      <header className="flex items-center justify-between px-6 py-4 border-b border-white/5">
        <Wordmark />
        <span className="text-xs uppercase tracking-widest text-slate-500">Devices · S3.4</span>
      </header>
      <main className="flex-1 px-6 py-8">
        <div className="mx-auto w-full max-w-2xl">
          {user ? <Devices email={user.email} /> : <Login onLogin={setUser} />}
        </div>
      </main>
      <footer className="px-6 py-4 text-center text-xs text-slate-600">
        tunnex.io — self-hosted VPN &amp; Zero Trust
      </footer>
    </div>
  );
}

function Login({ onLogin }: { onLogin: (u: { email: string }) => void }) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const { data, error } = await api.POST("/api/v1/auth/login", { body: { email, password } });
    setBusy(false);
    if (error || !data) {
      setError("Invalid email or password.");
      return;
    }
    onLogin({ email: data.email });
  }

  return (
    <form onSubmit={submit} className="rounded-xl border border-white/5 bg-ink-800 p-6">
      <h1 className="text-xl font-semibold text-white">Sign in</h1>
      <p className="mt-1 text-sm text-slate-400">Access your devices and download WireGuard configs.</p>
      <label className="mt-5 block text-sm text-slate-300">Email</label>
      <input
        type="email" value={email} onChange={(e) => setEmail(e.target.value)} required
        className="mt-1 w-full rounded-md border border-white/10 bg-ink-900 px-3 py-2 text-sm text-white"
      />
      <label className="mt-4 block text-sm text-slate-300">Password</label>
      <input
        type="password" value={password} onChange={(e) => setPassword(e.target.value)} required
        className="mt-1 w-full rounded-md border border-white/10 bg-ink-900 px-3 py-2 text-sm text-white"
      />
      {error && <p className="mt-3 text-xs text-rose-400">{error}</p>}
      <button
        type="submit" disabled={busy}
        className="mt-5 w-full rounded-md bg-accent-500 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
      >
        {busy ? "Signing in…" : "Sign in"}
      </button>
    </form>
  );
}

function Devices({ email }: { email: string }) {
  const [org, setOrg] = useState<Org | null>(null);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [devices, setDevices] = useState<Device[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [fullTunnel, setFullTunnel] = useState(false);
  const [config, setConfig] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function loadDevices(orgId: string) {
    const { data } = await api.GET("/api/v1/organizations/{orgId}/devices", {
      params: { path: { orgId } },
    });
    setDevices(data ?? []);
  }

  useEffect(() => {
    (async () => {
      try {
        const { data: orgs } = await api.GET("/api/v1/organizations");
        const first = orgs?.[0];
        if (!first) {
          setError("You are not a member of any organization yet.");
          return;
        }
        setOrg(first);
        const { data: ns } = await api.GET("/api/v1/organizations/{orgId}/nodes", {
          params: { path: { orgId: first.id } },
        });
        setNodes(ns ?? []);
        await loadDevices(first.id);
      } catch {
        setError("Could not reach the API. Is the stack up?");
      }
    })();
  }, []);

  async function create(e: React.FormEvent) {
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
      // Surface the API's specific message (e.g. node_not_ready, device_limit).
      setError(error?.error?.message ?? "Could not create the device.");
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
      setError(error?.error?.message ?? "Could not revoke the device.");
      return;
    }
    await loadDevices(org.id);
  }

  function download() {
    if (!config) return;
    const url = URL.createObjectURL(new Blob([config], { type: "text/plain" }));
    const a = document.createElement("a");
    a.href = url;
    a.download = "tunnex.conf";
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div>
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold text-white">Your devices</h1>
          <p className="text-sm text-slate-400">
            {email}
            {org ? ` · ${org.name}` : ""}
          </p>
        </div>
      </div>

      {error && <p className="mt-4 text-sm text-rose-400">{error}</p>}

      <form onSubmit={create} className="mt-6 rounded-xl border border-white/5 bg-ink-800 p-5">
        <div className="flex flex-wrap items-end gap-3">
          <div className="flex-1 min-w-[12rem]">
            <label className="block text-sm text-slate-300">New device name</label>
            <input
              value={name} onChange={(e) => setName(e.target.value)} required placeholder="my-laptop"
              className="mt-1 w-full rounded-md border border-white/10 bg-ink-900 px-3 py-2 text-sm text-white"
            />
          </div>
          <label className="flex items-center gap-2 text-sm text-slate-300">
            <input type="checkbox" checked={fullTunnel} onChange={(e) => setFullTunnel(e.target.checked)} />
            Full tunnel
          </label>
          <button
            type="submit" disabled={busy || nodes.length === 0}
            className="rounded-md bg-accent-500 px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
          >
            {busy ? "Creating…" : "Create device"}
          </button>
        </div>
        {nodes.length === 0 && (
          <p className="mt-3 text-xs text-amber-400">No gateway node is enrolled yet — enroll one to create devices.</p>
        )}
      </form>

      {config && (
        <div className="mt-4 rounded-xl border border-accent-500/30 bg-ink-800 p-5">
          <div className="flex items-center justify-between">
            <span className="text-sm font-medium text-accent-300">Your config (shown once)</span>
            <button onClick={download} className="rounded-md border border-white/10 px-3 py-1 text-xs text-white">
              Download .conf
            </button>
          </div>
          <pre className="mt-3 overflow-x-auto rounded-md bg-ink-900 p-3 text-xs text-slate-300">{config}</pre>
          <p className="mt-2 text-xs text-slate-500">
            The private key is in this file and is never shown again — download it now.
          </p>
        </div>
      )}

      <ul className="mt-6 space-y-2">
        {devices.map((d) => (
          <li
            key={d.id}
            className="flex items-center justify-between rounded-lg border border-white/5 bg-ink-800 px-4 py-3"
          >
            <div>
              <span className="text-sm text-white">{d.name}</span>
              <span className="ml-2 text-xs text-slate-500">{d.assigned_ip ?? "—"}</span>
              {d.status === "revoked" ? (
                <span className="ml-2 text-xs text-rose-400">revoked</span>
              ) : (
                <span className="ml-2 inline-flex items-center gap-1 text-xs text-slate-400">
                  <span className={`h-1.5 w-1.5 rounded-full ${d.online ? "bg-accent-400" : "bg-slate-600"}`} />
                  {lastSeen(d.last_handshake_at)}
                </span>
              )}
            </div>
            {d.status === "active" && (
              <button onClick={() => revoke(d.id)} className="text-xs text-slate-400 hover:text-rose-400">
                Revoke
              </button>
            )}
          </li>
        ))}
        {devices.length === 0 && <li className="text-sm text-slate-500">No devices yet.</li>}
      </ul>
    </div>
  );
}

// lastSeen renders honest recency ("last seen 42s ago") rather than faking a
// live-connection claim — WireGuard only knows the last handshake time.
function lastSeen(at?: string): string {
  if (!at) return "never connected";
  const secs = Math.max(0, Math.floor((Date.now() - new Date(at).getTime()) / 1000));
  if (secs < 60) return `last seen ${secs}s ago`;
  if (secs < 3600) return `last seen ${Math.floor(secs / 60)}m ago`;
  if (secs < 86400) return `last seen ${Math.floor(secs / 3600)}h ago`;
  return `last seen ${Math.floor(secs / 86400)}d ago`;
}

function Wordmark() {
  return (
    <div className="flex items-center gap-2">
      <span aria-hidden className="inline-block h-5 w-5 rounded-md bg-accent-500 shadow-[0_0_20px] shadow-accent-500/40" />
      <span className="text-lg font-semibold tracking-tight text-white">
        tunnex<span className="text-accent-400">.io</span>
      </span>
    </div>
  );
}
