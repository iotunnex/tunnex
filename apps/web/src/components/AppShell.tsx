import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { Logo, PRODUCT_TAGLINE } from "../brand";
import { api, CSRF } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button } from "./ui";
import { HealthStatus } from "./HealthStatus";

// Nav items. Dashboard + Devices are live (EPIC 3, S4.3); the rest are
// placeholders the later EPIC 4 stories (users, settings, audit) fill in.
const NAV = [
  { to: "/dashboard", label: "Dashboard", enabled: true },
  { to: "/devices", label: "Devices", enabled: true },
  { to: "/users", label: "Users", enabled: false },
  { to: "/settings", label: "Settings", enabled: false },
  { to: "/audit", label: "Audit log", enabled: false },
];

/** AppShell is the authenticated layout: header (brand + user + logout), sidebar
 * nav, and the routed page in the main area. */
export function AppShell() {
  const { state, logout } = useAuth();
  const navigate = useNavigate();
  const email = state.status === "authed" ? state.user.email : "";

  async function onLogout() {
    await logout();
    navigate("/login", { replace: true });
  }

  return (
    <div className="flex min-h-full flex-col">
      <header className="flex items-center justify-between border-b border-white/5 px-6 py-4">
        <Logo />
        <div className="flex items-center gap-4">
          <span className="text-sm text-slate-400">{email}</span>
          <Button variant="ghost" onClick={onLogout}>
            Log out
          </Button>
        </div>
      </header>

      <div className="flex flex-1">
        <nav className="w-48 shrink-0 border-r border-white/5 p-4">
          <ul className="space-y-1">
            {NAV.map((item) =>
              item.enabled ? (
                <li key={item.to}>
                  <NavLink
                    to={item.to}
                    className={({ isActive }) =>
                      `block rounded-md px-3 py-2 text-sm ${
                        isActive ? "bg-white/5 text-white" : "text-slate-400 hover:bg-white/5 hover:text-slate-200"
                      }`
                    }
                  >
                    {item.label}
                  </NavLink>
                </li>
              ) : (
                <li key={item.to}>
                  <span
                    className="block cursor-not-allowed rounded-md px-3 py-2 text-sm text-slate-600"
                    title="Coming soon"
                  >
                    {item.label}
                  </span>
                </li>
              ),
            )}
          </ul>
        </nav>

        <main className="flex-1 px-6 py-8">
          <div className="mx-auto w-full max-w-3xl">
            {state.status === "authed" && !state.user.email_verified && <VerifyEmailBanner />}
            <Outlet />
          </div>
        </main>
      </div>

      <footer className="flex items-center justify-between border-t border-white/5 px-6 py-3 text-xs text-slate-600">
        <HealthStatus />
        <span>{PRODUCT_TAGLINE}</span>
      </footer>
    </div>
  );
}

// VerifyEmailBanner nudges an unverified user (login is allowed unverified, but
// org-mutating actions are gated server-side). Resend goes through the real
// mailer flow (POST /auth/verify-email/resend).
function VerifyEmailBanner() {
  const [state, setState] = useState<"idle" | "busy" | "sent" | "error">("idle");
  async function resend() {
    setState("busy");
    // Only claim success on a real success — a failed/errored request must not
    // show "Sent" (which would hide the button and mislead the user).
    try {
      const { error } = await api.POST("/api/v1/auth/verify-email/resend", { headers: CSRF });
      setState(error ? "error" : "sent");
    } catch {
      setState("error");
    }
  }
  return (
    <div className="mb-6 flex items-center justify-between rounded-lg border border-warn/40 bg-warn/5 px-4 py-3">
      <span className="text-sm text-slate-300">
        Verify your email to unlock all actions.
        {state === "sent" && <span className="ml-1 text-ok">Sent — check your inbox.</span>}
        {state === "error" && <span className="ml-1 text-danger">Couldn&rsquo;t send — try again.</span>}
      </span>
      {state !== "sent" && (
        <Button variant="ghost" onClick={resend} disabled={state === "busy"}>
          {state === "busy" ? "Sending…" : "Resend verification"}
        </Button>
      )}
    </div>
  );
}
