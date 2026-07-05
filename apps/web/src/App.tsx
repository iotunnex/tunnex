import { useEffect } from "react";
import { Navigate, Route, Routes } from "react-router-dom";
import { PRODUCT_NAME } from "./brand";
import { AuthProvider, useAuth } from "./lib/auth";
import { AppShell } from "./components/AppShell";
import Login from "./pages/Login";
import Devices from "./pages/Devices";

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
        <Route element={<RequireAuth><AppShell /></RequireAuth>}>
          <Route path="/devices" element={<Devices />} />
        </Route>
        {/* Default: the shell decides (RequireAuth bounces anon users to /login). */}
        <Route path="*" element={<Navigate to="/devices" replace />} />
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
  if (state.status === "authed") return <Navigate to="/devices" replace />;
  return <>{children}</>;
}

function FullScreenLoading() {
  return <div className="grid min-h-full place-items-center text-sm text-slate-500">Loading…</div>;
}
