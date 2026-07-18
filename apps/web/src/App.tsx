import { useEffect, useState } from "react";
import { Navigate, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { PRODUCT_NAME } from "./brand";
import { api } from "./lib/api";
import { resolveMfaGateRoute } from "./lib/authroute";
import { AuthProvider, useAuth } from "./lib/auth";
import { AuthLayout } from "./components/AuthLayout";
import { MfaSettings } from "./components/MfaSettings";
import { AppShell } from "./components/AppShell";
import Login from "./pages/Login";
import Signup from "./pages/Signup";
import ForgotPassword from "./pages/ForgotPassword";
import ResetPassword from "./pages/ResetPassword";
import AcceptInvite from "./pages/AcceptInvite";
import VerifyEmail from "./pages/VerifyEmail";
import VerifyPending from "./pages/VerifyPending";
import CreateOrg from "./pages/CreateOrg";
import CliAuth from "./pages/CliAuth";
import CliDevice from "./pages/CliDevice";
import Dashboard from "./pages/Dashboard";
import Devices from "./pages/Devices";
import Sites from "./pages/Sites";
import Access from "./pages/Access";
import Users from "./pages/Users";
import Settings from "./pages/Settings";
import AuditLog from "./pages/AuditLog";

/**
 * App is the router + auth shell (S4.1). Authenticated pages live under AppShell
 * behind RequireAuth; the design system (brand, tokens, primitives) is wired so a
 * brand-kit swap touches only brand.tsx + the Tailwind palette. Login/signup/SSO
 * screens (S4.2) and the dashboard/users/settings/audit pages (S4.3–S4.6) fill in
 * the placeholder nav items.
 */
export default function App() {
  // The tab title comes from the brand module (the static index.html title is a
  // pre-hydration fallback), keeping the product name a single source of truth.
  useEffect(() => {
    document.title = PRODUCT_NAME;
  }, []);
  return (
    <AuthProvider>
      <Routes>
        <Route path="/login" element={<AnonOnly><Login /></AnonOnly>} />
        <Route path="/signup" element={<AnonOnly><Signup /></AnonOnly>} />
        <Route path="/forgot-password" element={<AnonOnly><ForgotPassword /></AnonOnly>} />
        {/* Reset + verify are reached from emailed links; usable while logged out
            and harmless while logged in, so they are not auth-gated. */}
        <Route path="/reset-password" element={<ResetPassword />} />
        <Route path="/accept-invite" element={<AcceptInvite />} />
        <Route path="/verify-email" element={<VerifyEmail />} />
        {/* Authenticated area. The onboarding funnel (S4.7) lives BETWEEN auth and
            the shell: /create-org and /verify-pending are reachable while
            authenticated with no org yet; the shell itself is gated by RequireOrg. */}
        <Route element={<RequireAuth />}>
          <Route path="/create-org" element={<RequireNoOrg><CreateOrg /></RequireNoOrg>} />
          <Route path="/verify-pending" element={<VerifyPending />} />
          {/* S5.1 CLI auth: the browser consent leg (`tunnex login`) and the
              device-code approval page. Authenticated but org-independent. */}
          <Route path="/cli-auth" element={<CliAuth />} />
          <Route path="/cli-device" element={<CliDevice />} />
          {/* S7.5.5 D8: a MFA-enforcement-gated user (org requires 2FA, none set up) is routed here
              by RequireAuth — enrollment only, until they confirm a TOTP. Org-independent. */}
          <Route path="/enroll-mfa" element={<ForcedEnroll />} />
          <Route element={<RequireOrg><AppShell /></RequireOrg>}>
            <Route path="/dashboard" element={<Dashboard />} />
            <Route path="/devices" element={<Devices />} />
            <Route path="/sites" element={<Sites />} />
            <Route path="/access" element={<Access />} />
            <Route path="/users" element={<Users />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="/audit" element={<AuditLog />} />
          </Route>
        </Route>
        {/* Default: the shell decides (RequireAuth bounces anon users to /login). */}
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </AuthProvider>
  );
}

// RequireAuth gates the authenticated area: it waits out the /me bootstrap (no
// login flash for an already-authenticated user), then redirects anonymous users
// to /login. Renders the nested routes via <Outlet />.
function RequireAuth() {
  const { state } = useAuth();
  const location = useLocation();
  if (state.status === "loading") return <FullScreenLoading />;
  if (state.status === "anon") {
    // Preserve the intended destination so it survives the login round-trip —
    // the CLI login flow (`tunnex login` → /cli-auth?…) on a fresh machine
    // depends on landing back on /cli-auth WITH its query params (S5.1).
    const next = encodeURIComponent(location.pathname + location.search);
    return <Navigate to={`/login?next=${next}`} replace />;
  }
  // S7.5.5 D8: an enrollment-gated user is confined to the enrollment ceremony until they set up 2FA;
  // once the gate clears they are released back to the app (WF-3 — the inverse redirect the original
  // code was missing, which trapped a now-enrolled user on /enroll-mfa). The SERVER enforces the gate
  // (default-deny middleware, typed mfa_enrollment_required 403); this is the client routing so the
  // user lands on the ceremony rather than hitting dead 403s. Decision is a pure fn (authroute.ts),
  // table-pinned in BOTH directions (resolveMfaGateRoute).
  const gateRoute = resolveMfaGateRoute(Boolean(state.user.mfa_enrollment_required), location.pathname);
  if (gateRoute) {
    return <Navigate to={gateRoute} replace />;
  }
  return <Outlet />;
}

// ForcedEnroll is the enrollment-gated landing (D8): the shared MfaSettings ceremony with a
// blocking header + a sign-out escape. Confirming clears mfa_enrollment_required (MfaSettings updates
// the auth user), and RequireAuth then releases the user to the app.
function ForcedEnroll() {
  const { logout } = useAuth();
  return (
    <AuthLayout>
      <h1 className="text-xl font-semibold text-white">Set up two-factor authentication</h1>
      <p className="mt-1 text-sm text-slate-400">Your organization requires 2FA. Finish setup to continue to Tunnex.</p>
      <div className="mt-5">
        <MfaSettings />
      </div>
      <button type="button" className="mt-4 text-xs text-slate-400 underline hover:text-slate-200" onClick={logout}>
        Sign out
      </button>
    </AuthLayout>
  );
}

// RequireNoOrg is RequireOrg's inverse, guarding the create-org step itself
// (S4.8/F4): a user who ALREADY belongs to an org and navigates to /create-org
// manually is re-routed to the dashboard at VISIT time — previously only the
// submit path re-checked (403 → membership re-check), so the form rendered
// pointlessly. Fail-open on a fetch error: the form is safe to show (the
// submit path still ends in the server's answer).
function RequireNoOrg({ children }: { children: React.ReactNode }) {
  const [status, setStatus] = useState<"loading" | "none" | "has">("loading");

  useEffect(() => {
    let cancelled = false;
    api
      .GET("/api/v1/organizations")
      .then(({ data, error }) => {
        if (cancelled) return;
        if (error) return setStatus("none"); // fail open to the form
        setStatus((data?.length ?? 0) > 0 ? "has" : "none");
      })
      .catch(() => {
        if (!cancelled) setStatus("none");
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (status === "loading") return <FullScreenLoading />;
  if (status === "has") return <Navigate to="/dashboard" replace />;
  return <>{children}</>;
}

// RequireOrg is the onboarding funnel's router (S4.7). It gates the app shell on
// having at least one organization, sending a user with none through the funnel:
//   - >=1 membership          -> render the shell
//   - 0 memberships, verified -> /create-org (the explicit create-org step)
//   - 0 memberships, unverified -> /verify-pending (create-org is verified-gated)
// The SSO-JIT and invite paths never trip this: they produce a membership, so the
// caller already has >=1 org and lands straight in the shell.
//
// This runs one GET /organizations per shell entry (the layout route stays mounted
// across page navigations, so it does NOT refetch on every nav). Each page still
// fetches its own org — a deliberate small duplication until the deferred
// useCurrentOrg hook (org-switcher story) lifts org context app-wide.
//
// The create-org → /dashboard handoff assumes read-your-writes: after a 201 the
// remounted RequireOrg refetches and must see the new org. That holds for the
// single-primary Postgres this product deploys; a read-replica topology could
// briefly bounce the user back to /create-org (accepted — tunnex has no replicas).
function RequireOrg({ children }: { children: React.ReactNode }) {
  const { state } = useAuth();
  const [status, setStatus] = useState<"loading" | "none" | "has">("loading");

  useEffect(() => {
    let cancelled = false;
    api
      .GET("/api/v1/organizations")
      .then(({ data, error }) => {
        if (cancelled) return;
        // Fail OPEN on a fetch error: let the shell render and surface the real
        // error, rather than trapping a transient failure in the create-org funnel
        // (an errored fetch is not the same signal as an empty list).
        if (error) return setStatus("has");
        setStatus((data?.length ?? 0) > 0 ? "has" : "none");
      })
      .catch(() => {
        if (!cancelled) setStatus("has");
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (status === "loading") return <FullScreenLoading />;
  if (status === "none") {
    const unverified = state.status === "authed" && !state.user.email_verified;
    return <Navigate to={unverified ? "/verify-pending" : "/create-org"} replace />;
  }
  return <>{children}</>;
}

// AnonOnly keeps an authenticated user off the login page (sends them to the app).
function AnonOnly({ children }: { children: React.ReactNode }) {
  const { state } = useAuth();
  if (state.status === "loading") return <FullScreenLoading />;
  if (state.status === "authed") return <Navigate to="/dashboard" replace />;
  return <>{children}</>;
}

function FullScreenLoading() {
  return <div className="grid min-h-full place-items-center text-sm text-slate-500">Loading…</div>;
}
