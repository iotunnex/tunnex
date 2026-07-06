import { useEffect } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { PRODUCT_NAME } from "./brand";
import { AuthProvider, useAuth } from "./lib/auth";
import { AppShell } from "./components/AppShell";
import Login from "./pages/Login";
import Signup from "./pages/Signup";
import ForgotPassword from "./pages/ForgotPassword";
import ResetPassword from "./pages/ResetPassword";
import VerifyEmail from "./pages/VerifyEmail";
import Dashboard from "./pages/Dashboard";
import Devices from "./pages/Devices";
import Users from "./pages/Users";
import Settings from "./pages/Settings";

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
        <Route path="/verify-email" element={<VerifyEmail />} />
        <Route element={<RequireAuth><AppShell /></RequireAuth>}>
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/devices" element={<Devices />} />
          <Route path="/users" element={<Users />} />
          <Route path="/settings" element={<Settings />} />
        </Route>
        {/* Default: the shell decides (RequireAuth bounces anon users to /login). */}
        <Route path="*" element={<Navigate to="/dashboard" replace />} />
      </Routes>
    </AuthProvider>
  );
}

// RequireAuth gates the shell: it waits out the /me bootstrap (no login flash for
// an already-authenticated user), then redirects anonymous users to /login.
function RequireAuth({ children }: { children: React.ReactNode }) {
  const { state } = useAuth();
  if (state.status === "loading") return <FullScreenLoading />;
  if (state.status === "anon") return <Navigate to="/login" replace />;
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
