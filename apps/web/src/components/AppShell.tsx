import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { Logo, PRODUCT_TAGLINE } from "../brand";
import { useAuth } from "../lib/auth";
import { desktop } from "../lib/desktop";
import { useResendVerification } from "../lib/useResendVerification";
import { Button } from "./ui";
import { HealthStatus } from "./HealthStatus";

// Nav items. Dashboard + Devices are live (EPIC 3, S4.3); the rest are
// placeholders the later EPIC 4 stories (users, settings, audit) fill in.
const NAV = [
  { to: "/dashboard", label: "Dashboard", enabled: true },
  { to: "/devices", label: "Devices", enabled: true },
  { to: "/users", label: "Users", enabled: true },
  { to: "/settings", label: "Settings", enabled: true },
  { to: "/audit", label: "Audit log", enabled: true },
];

/** AppShell is the authenticated layout: header (brand + user + logout), sidebar
 * nav, and the routed page in the main area. */
export function AppShell() {
  const { state, logout } = useAuth();
  const navigate = useNavigate();
  const email = state.status === "authed" ? state.user.email : "";

  async function onLogout() {
    // Desktop: revoke the credential + clear the keychain via the bridge (main
    // reloads the window afterward). Browser: the cookie-session logout.
    const d = desktop();
    if (d) {
      await d.auth.logout().catch(() => {});
      return; // main reloads → /auth/me (no bearer) → anon → /login
    }
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
// mailer flow (POST /auth/verify-email/resend) via the shared hook.
function VerifyEmailBanner() {
  const { state, resend } = useResendVerification();
  return (
    <div className="mb-6 flex items-center justify-between rounded-lg border border-warn/40 bg-warn/5 px-4 py-3">
      <span className="text-sm text-slate-300">
        Verify your email to unlock all actions.
        {/* Success feedback uses the accent, not green: green is reserved for
            liveness ("alive right now"), not "the action worked" (S4.4 decision f). */}
        {state === "sent" && <span className="ml-1 text-accent-400">Sent — check your inbox.</span>}
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
