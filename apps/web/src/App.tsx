import { useEffect, useState } from "react";
import { createTunnexClient } from "@tunnex/shared";

const api = createTunnexClient("/");

type HealthState =
  | { status: "loading" }
  | { status: "up"; requestId?: string }
  | { status: "down"; error: string };

/**
 * Foundation shell (S0.1). It renders the Tunnex brand placeholder and live-checks
 * the API through the nginx-proxied /healthz endpoint so `docker compose up` is
 * visibly working end-to-end. Real routing, auth, and dashboard arrive in EPIC 4.
 */
export default function App() {
  const [health, setHealth] = useState<HealthState>({ status: "loading" });

  useEffect(() => {
    let cancelled = false;
    // Typed client generated from the OpenAPI spec — response shape is checked.
    api
      .GET("/healthz")
      .then(({ data, error }) => {
        if (cancelled) return;
        if (error || !data) {
          setHealth({ status: "down", error: "unexpected response" });
          return;
        }
        setHealth({ status: "up", requestId: data.request_id });
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setHealth({ status: "down", error: err instanceof Error ? err.message : "unknown" });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  return (
    <div className="min-h-full flex flex-col">
      <header className="flex items-center justify-between px-6 py-4 border-b border-white/5">
        <Wordmark />
        <span className="text-xs uppercase tracking-widest text-slate-500">Foundation · S0.1</span>
      </header>

      <main className="flex-1 grid place-items-center px-6">
        <div className="w-full max-w-md">
          <h1 className="text-2xl font-semibold text-white text-balance">
            Your Tunnex control plane is running.
          </h1>
          <p className="mt-2 text-sm text-slate-400">
            Self-hosted, multi-tenant VPN &amp; Zero Trust access. Login, organizations, and
            WireGuard management land in the next stories.
          </p>

          <div className="mt-8 rounded-xl border border-white/5 bg-ink-800 p-5">
            <div className="flex items-center justify-between">
              <span className="text-sm font-medium text-slate-300">API health</span>
              <HealthPill state={health} />
            </div>
            {health.status === "up" && health.requestId && (
              <p className="mt-3 font-mono text-xs text-slate-500 break-all">
                request&nbsp;id: {health.requestId}
              </p>
            )}
            {health.status === "down" && (
              <p className="mt-3 text-xs text-rose-400">
                Could not reach the API ({health.error}). Is the stack up? Try{" "}
                <code className="font-mono">make up</code>.
              </p>
            )}
          </div>
        </div>
      </main>

      <footer className="px-6 py-4 text-center text-xs text-slate-600">
        tunnex.io — self-hosted VPN &amp; Zero Trust
      </footer>
    </div>
  );
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

function HealthPill({ state }: { state: HealthState }) {
  const map = {
    loading: { label: "checking…", cls: "bg-slate-500/15 text-slate-400" },
    up: { label: "operational", cls: "bg-accent-500/15 text-accent-400" },
    down: { label: "unreachable", cls: "bg-rose-500/15 text-rose-400" },
  } as const;
  const { label, cls } = map[state.status];
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium ${cls}`}>
      <span className="h-1.5 w-1.5 rounded-full bg-current" />
      {label}
    </span>
  );
}
