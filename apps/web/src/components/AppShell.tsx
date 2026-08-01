import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { Logo, PRODUCT_TAGLINE } from "../brand";
import { useAuth } from "../lib/auth";
import { desktop } from "../lib/desktop";
import { useResendVerification } from "../lib/useResendVerification";
import { Button } from "./ui";
import { HealthStatus } from "./HealthStatus";
import { useLayoutCapability } from "./ComposeGate";
import { CommandPalette } from "./CommandPalette";

// S14.2 — THE NAV, GROUPED. The wireframe groups destinations NETWORK / ACCESS / OBSERVE / OPERATE / SETTINGS,
// and that grouping is preserved at EVERY width; only its PRESENTATION changes.
//
// ⛔ RESPONSIVE MAY RE-ARRANGE, NEVER REMOVE. Every destination is in the DOM at every width. A CSS-hidden
// destination is a navigation surface that exists for some users and not others, DECIDED BY VIEWPORT RATHER
// THAN BY PERMISSION — and permission is a render decision while width never is (docs/laws.md).
//
// Sites (S8.3) and Kubernetes (S10.3) are shown to everyone: each page owns its own edition upsell (the Access
// precedent, D5), so a non-enterprise org sees the entry and a clear explanation rather than a dead link.
export const NAV_GROUPS: Array<{
  group: string;
  items: Array<{ to: string; label: string }>;
}> = [
  { group: "", items: [{ to: "/dashboard", label: "Dashboard" }] },
  {
    group: "NETWORK",
    items: [
      { to: "/sites", label: "Sites" },
      { to: "/kubernetes", label: "Kubernetes" },
    ],
  },
  {
    group: "ACCESS",
    items: [
      { to: "/access", label: "Access" },
      { to: "/devices", label: "Devices" },
      { to: "/users", label: "Users" },
    ],
  },
  { group: "OBSERVE", items: [{ to: "/audit", label: "Audit log" }] },
  { group: "SETTINGS", items: [{ to: "/settings", label: "Settings" }] },
];

/** Flat destination list — the invariant the responsive contract asserts is identical at every width. */
export const NAV_DESTINATIONS = NAV_GROUPS.flatMap((g) => g.items);

/** The triage set — the surfaces mobile exists FOR (read health, work the approval queue, act on devices). */
const TRIAGE_SET = ["/dashboard", "/devices", "/access"];

function NavGroups({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <>
      {NAV_GROUPS.map((g) => (
        <div key={g.group || "root"} className="mb-3">
          {g.group && (
            <p className="px-3 pb-1 text-[10px] font-semibold uppercase tracking-wider text-slate-600">
              {g.group}
            </p>
          )}
          <ul className="space-y-1">
            {g.items.map((item) => (
              <li key={item.to}>
                <NavLink
                  to={item.to}
                  onClick={onNavigate}
                  className={({ isActive }) =>
                    `block rounded-md px-3 py-2 text-sm ${
                      isActive
                        ? "bg-white/5 text-white"
                        : "text-slate-400 hover:bg-white/5 hover:text-slate-200"
                    }`
                  }
                >
                  {item.label}
                </NavLink>
              </li>
            ))}
          </ul>
        </div>
      ))}
    </>
  );
}

/**
 * SidebarNav renders every destination in every nav mode. `navMode` changes how the groups are PRESENTED — a
 * drawer behind a menu button, a compact rail, or the full labelled rail — and never WHICH destinations exist.
 *
 * ⚠ THE DRAWER IS `hidden` WHEN CLOSED, DELIBERATELY, and that is not a contradiction of "never remove".
 * An off-canvas panel whose links stay in the accessible tree is a keyboard trap: tab order walks through
 * destinations the user cannot see. So the closed drawer is genuinely absent — and the invariant it must
 * satisfy is that OPENING it yields the SAME destination set as the widest rail. That is what the responsive
 * contract asserts: it clicks the menu button at `triage` and compares the set. A destination dropped from the
 * narrow build fails there.
 */
function SidebarNav() {
  const { navMode } = useLayoutCapability();
  const [drawerOpen, setDrawerOpen] = useState(false);

  if (navMode === "drawer") {
    return (
      <>
        <button
          type="button"
          aria-expanded={drawerOpen}
          aria-controls="main-nav"
          onClick={() => setDrawerOpen((o) => !o)}
          className="absolute left-4 top-4 rounded-md border border-white/10 px-3 py-2 text-sm text-slate-300"
        >
          Menu
        </button>
        <nav
          id="main-nav"
          aria-label="Main"
          hidden={!drawerOpen}
          className="absolute inset-y-0 left-0 z-20 w-56 border-r border-white/5 bg-ink-950 p-4"
        >
          <NavGroups onNavigate={() => setDrawerOpen(false)} />
        </nav>
      </>
    );
  }

  // rail (compose) and full (operate+) differ in width and label treatment, not in content.
  return (
    <nav
      id="main-nav"
      aria-label="Main"
      className={`shrink-0 border-r border-white/5 p-4 ${navMode === "rail" ? "w-40" : "w-48"}`}
    >
      <NavGroups />
    </nav>
  );
}

/**
 * The triage bottom bar: the on-call subset, one tap away, at `triage` only.
 *
 * It is a SECOND surface carrying destinations that already exist in the drawer, so it is derived from
 * NAV_DESTINATIONS rather than re-listed — a hand-written copy is how the two drift apart.
 */
function TriageBar() {
  const items = NAV_DESTINATIONS.filter((i) => TRIAGE_SET.includes(i.to));
  return (
    <nav
      aria-label="Triage"
      className="sticky bottom-0 flex justify-around border-t border-white/5 bg-ink-950 px-2 py-2"
    >
      {items.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          className="px-3 py-1 text-xs text-slate-400"
        >
          {item.label}
        </NavLink>
      ))}
    </nav>
  );
}

/** AppShell is the authenticated layout: header (brand + user + logout), sidebar
 * nav, and the routed page in the main area. */
export function AppShell() {
  const { state, logout } = useAuth();
  const { navMode, columns } = useLayoutCapability();
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
      {/* Mounted on the SHELL, not per screen: ⌘K must work wherever the user is. */}
      <CommandPalette />
      <header className="flex items-center justify-between border-b border-white/5 px-6 py-4">
        <Logo />
        <div className="flex items-center gap-4">
          <span className="text-sm text-slate-400">{email}</span>
          <Button variant="ghost" onClick={onLogout}>
            Log out
          </Button>
        </div>
      </header>

      <div className="relative flex flex-1">
        <SidebarNav />

        <main className="flex-1 px-6 py-8" data-columns={columns}>
          {/* data-columns publishes the column BUDGET for the page grid to consume. `max` clamps rather than
              stretching: the content max-width holds and the extra space becomes margin. */}
          <div className="mx-auto w-full max-w-3xl">
            {state.status === "authed" && !state.user.email_verified && (
              <VerifyEmailBanner />
            )}
            <Outlet />
          </div>
        </main>
      </div>

      {navMode === "drawer" && <TriageBar />}

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
        {state === "sent" && (
          <span className="ml-1 text-accent-400">Sent — check your inbox.</span>
        )}
        {state === "error" && (
          <span className="ml-1 text-danger">
            Couldn&rsquo;t send — try again.
          </span>
        )}
      </span>
      {state !== "sent" && (
        <Button variant="ghost" onClick={resend} disabled={state === "busy"}>
          {state === "busy" ? "Sending…" : "Resend verification"}
        </Button>
      )}
    </div>
  );
}
