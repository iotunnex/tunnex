import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, apiErrorMessage, type Meta } from "../lib/api";
import { useAuth } from "../lib/auth";
import { AuthLayout } from "../components/AuthLayout";
import { Button, ErrorText, Field, Input } from "../components/ui";

// Human-readable text for SSO callback reject codes (watch-item d) — the server
// redirects failures to /login?sso_error=<code> instead of a raw error body.
const SSO_ERRORS: Record<string, string> = {
  unverified_local_exists:
    "An account with this email already exists. Sign in with your password first, then link SSO from settings.",
  idp_email_unverified: "Your identity provider hasn't verified this email address. Verify it there and try again.",
  edition_required: "SSO is not enabled on this deployment.",
};
function ssoErrorText(code: string): string {
  return SSO_ERRORS[code] ?? "Single sign-on failed. Please try again or sign in with your password.";
}

export default function Login() {
  const { setUser } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(params.get("sso_error") ? ssoErrorText(params.get("sso_error")!) : null);
  const [busy, setBusy] = useState(false);
  const [meta, setMeta] = useState<Meta | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .GET("/api/v1/meta")
      .then(({ data }) => {
        if (!cancelled) setMeta(data ?? null);
      })
      .catch(() => {
        /* meta unavailable — SSO section simply stays hidden */
      });
    return () => {
      cancelled = true;
    };
  }, []);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const { data, error } = await api.POST("/api/v1/auth/login", { body: { email, password } });
    setBusy(false);
    if (error || !data) {
      // The server keeps invalid-credentials generic and account_deactivated
      // distinct; we render its message verbatim (no client-side enumeration tell).
      setError(apiErrorMessage(error, "Invalid email or password."));
      return;
    }
    setUser(data);
    navigate("/devices", { replace: true });
  }

  return (
    <AuthLayout>
      <h1 className="text-xl font-semibold text-white">Sign in</h1>
      <p className="mt-1 text-sm text-slate-400">Access your devices and WireGuard configs.</p>
      <form onSubmit={submit} className="mt-5 space-y-4">
        <Field label="Email">
          <Input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </Field>
        <Field label="Password">
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </Field>
        <ErrorText>{error}</ErrorText>
        <Button type="submit" disabled={busy} className="w-full">
          {busy ? "Signing in…" : "Sign in"}
        </Button>
      </form>

      {meta && meta.sso_providers.length > 0 && <SsoSection providers={meta.sso_providers} onError={setError} />}

      <div className="mt-5 flex justify-between text-xs text-slate-400">
        <Link to="/signup" className="hover:text-slate-200">
          Create an account
        </Link>
        <Link to="/forgot-password" className="hover:text-slate-200">
          Forgot password?
        </Link>
      </div>
    </AuthLayout>
  );
}

// SsoSection (enterprise only — hidden entirely in the open build via meta). SSO
// is configured per-org, so the user names their organization, then picks a
// provider; we redirect the browser to the IdP URL the API returns.
function SsoSection({ providers, onError }: { providers: string[]; onError: (m: string) => void }) {
  const [org, setOrg] = useState("");
  async function start(provider: "google" | "microsoft") {
    if (!org) {
      onError("Enter your organization to sign in with SSO.");
      return;
    }
    const { data, error } = await api.GET("/api/v1/auth/sso/{provider}/start", {
      params: { path: { provider }, query: { org } },
    });
    if (error || !data) {
      onError(apiErrorMessage(error, "Could not start single sign-on."));
      return;
    }
    window.location.href = data.redirect_url;
  }
  return (
    <div className="mt-6 border-t border-white/5 pt-5">
      <p className="text-xs text-slate-500">Or sign in with SSO</p>
      <Field label="Organization">
        <Input value={org} onChange={(e) => setOrg(e.target.value)} placeholder="acme" />
      </Field>
      <div className="mt-3 flex gap-2">
        {providers.includes("google") && (
          <Button variant="ghost" className="flex-1" onClick={() => start("google")}>
            Google
          </Button>
        )}
        {providers.includes("microsoft") && (
          <Button variant="ghost" className="flex-1" onClick={() => start("microsoft")}>
            Microsoft
          </Button>
        )}
      </div>
    </div>
  );
}
