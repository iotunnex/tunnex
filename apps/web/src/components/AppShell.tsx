import { useState } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { Logo, PRODUCT_TAGLINE } from "../brand";
import { useAuth } from "../lib/auth";
import { desktop } from "../lib/desktop";
import { useResendVerification } from "../lib/useResendVerification";
import { Button } from "./ui";
import { HealthStatus } from "./HealthStatus";
import { IdentityBadges } from "./IdentityBadges";
import { useLayoutCapability } from "./ComposeGate";
import { CommandPalette } from "./CommandPalette";
import { useNavCounts } from "../lib/useNavCounts";
import { Icon, type IconName } from "./Icon";
import { badgeText, gatewayBadgeText, type NavCounts } from "../lib/navcounts";

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
  items: Array<{ to: string; label: string; icon: IconName }>;
}> = [
  {
    group: "",
    items: [{ to: "/dashboard", label: "Overview", icon: "layout-dashboard" }],
  },
  {
    group: "NETWORK",
    items: [
      { to: "/sites", label: "Sites", icon: "network" },
      { to: "/kubernetes", label: "Kubernetes", icon: "boxes" },
    ],
  },
  {
    group: "ACCESS",
    items: [
      { to: "/access", label: "Access Policies", icon: "shield" },
      { to: "/devices", label: "Devices", icon: "laptop" },
      { to: "/users", label: "Users & Roles", icon: "users" },
    ],
  },
  {
    group: "OBSERVE",
    items: [{ to: "/audit", label: "Audit Log", icon: "file-text" }],
  },
  {
    group: "SETTINGS",
    items: [{ to: "/settings", label: "Org Settings", icon: "settings" }],
  },
];

/** Flat destination list — the invariant the responsive contract asserts is identical at every width. */
export const NAV_DESTINATIONS = NAV_GROUPS.flatMap((g) => g.items);

/** The triage set — the surfaces mobile exists FOR (read health, work the approval queue, act on devices). */
const TRIAGE_SET = ["/dashboard", "/devices", "/access"];

/**
 * The badge for a destination, or `null`.
 *
 * ⛔ `null` MEANS RENDER NOTHING. Not an empty string, not a dash, and never `0` — see lib/navcounts.ts for
 * why this surface is stricter than any other in the app.
 */
function badgeFor(to: string, c: NavCounts): string | null {
  if (to === "/dashboard")
    return gatewayBadgeText(c.gatewaysOnline, c.gatewaysTotal);
  if (to === "/sites") return badgeText(c.sites);
  if (to === "/devices") return badgeText(c.devices);
  return null;
}

function NavGroups({
  onNavigate,
  counts,
}: {
  onNavigate?: () => void;
  counts: NavCounts;
}) {
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
                    // README: nav item = flex, gap 10, padding 7px 12px, radius 9, 14px icon + 12.5px label,
                    // right-aligned badge. Active = accent at 13%; hover nudges 2px right.
                    `flex items-center gap-10 rounded-nav px-12 py-7 text-nav transition-colors ${
                      isActive
                        ? "bg-white/[.12] text-ink-heading"
                        : "text-ink-body hover:translate-x-[2px] hover:bg-white/[.06] hover:text-ink-primary"
                    }`
                  }
                >
                  <Icon name={item.icon} size={14} className="shrink-0" />
                  <span className="truncate">{item.label}</span>
                  {/* ⛔ The badge is RIGHT-ALIGNED and CONDITIONAL; the destination never is. `null` means
                      render nothing — never 0, never a dash (lib/navcounts.ts). */}
                  {(() => {
                    const b = badgeFor(item.to, counts);
                    return b === null ? null : (
                      <span className="ml-auto font-mono text-badge tracking-[.1em] text-ink-secondary">
                        {b}
                      </span>
                    );
                  })()}
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
  const counts = useNavCounts();
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
          className="absolute inset-y-0 left-0 z-20 w-[228px] border-r border-line bg-bg p-10"
        >
          <NavGroups onNavigate={() => setDrawerOpen(false)} counts={counts} />
        </nav>
      </>
    );
  }

  // rail (compose) and full (operate+) differ in width and label treatment, not in content.
  return (
    <nav
      id="main-nav"
      aria-label="Main"
      // README: 228px, collapsing to 64px. `rail` is our narrow-viewport mode — the designer authored no
      // breakpoints, so the collapsed width is ours (founder-ruled), the 228px is theirs.
      className={`shrink-0 border-r border-line p-10 ${navMode === "rail" ? "w-[64px]" : "w-[228px]"}`}
    >
      <NavGroups counts={counts} />
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
      {/* README: TOP BAR, h:56px — search (opens the palette), spacer, then identity. */}
      <header className="flex h-[56px] shrink-0 items-center justify-between border-b border-line px-16">
        <Logo />
        <div className="flex items-center gap-12">
          {/* The search field IS the command-palette affordance (S14.3 built the palette; this is its
              discoverable entry point, since a shortcut nobody sees is a shortcut nobody uses). */}
          <button
            type="button"
            onClick={() =>
              window.dispatchEvent(
                new KeyboardEvent("keydown", {
                  key: "k",
                  metaKey: true,
                  bubbles: true,
                }),
              )
            }
            className="hidden items-center gap-8 rounded-input border border-line bg-surface-inset px-12 py-7 text-cell text-ink-secondary hover:text-ink-body md:flex"
          >
            <Icon name="search" size={13} />
            <span>Search users, devices, gateways, sites…</span>
            <span className="ml-8 font-mono text-badge text-ink-secondary">
              ⌘K
            </span>
          </button>
          <IdentityBadges />
          <span className="text-cell text-ink-body">{email}</span>
          <Button variant="ghost" onClick={onLogout}>
            Log out
          </Button>
        </div>
      </header>

      <div className="relative flex flex-1">
        <SidebarNav />

        {/* ⛔ NO max-width. README: "Page body max content width: none — grids fill available width."
            The previous `max-w-3xl` capped EVERY screen at 768px, which is why S14.2's `columns` budget was
            computed, asserted, and never consumable — dormant machinery in our own new code (docs/laws.md).
            Padding and gap are the README's: 20px 24px 28px, flex column, gap 14. */}
        <main
          className="flex flex-1 flex-col gap-14 px-24 pb-[28px] pt-20"
          data-columns={columns}
        >
          {/* data-columns publishes the column BUDGET so a page grid can consume it — which nothing could do
              while this element capped the width at 768px. */}
          {state.status === "authed" && !state.user.email_verified && (
            <VerifyEmailBanner />
          )}
          <Outlet />
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
