import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { api, apiErrorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { AuthLayout } from "../components/AuthLayout";
import { Button, ErrorText, Field, Input } from "../components/ui";

/**
 * AcceptInvite is the landing page for the invitation email link
 * (/accept-invite?token=…). WITHOUT it the link fell through to `*` → /dashboard →
 * /login and the token was dropped, so an invited user signed up fresh with no org
 * and got bounced into create-org onboarding instead of joining the inviting org.
 *
 * Accepting provisions/links the user, adds the membership, and AUTO-LOGS-IN (the
 * server sets the session cookie), so we hydrate auth from /auth/me and land the
 * user directly in their new org — RequireOrg passes because the membership exists.
 */
export default function AcceptInvite() {
  const { setUser } = useAuth();
  const navigate = useNavigate();
  const [params] = useSearchParams();
  // Capture the token ONCE, then strip it from the URL so the secret doesn't linger
  // in browser history / leak via the Referer header (same hygiene as ResetPassword).
  const [token] = useState(() => params.get("token") ?? "");
  useEffect(() => {
    if (params.get("token")) window.history.replaceState(null, "", window.location.pathname);
  }, [params]);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const { error } = await api.POST("/api/v1/auth/invitations/accept", { body: { token, name, password } });
    if (error) {
      setBusy(false);
      setError(apiErrorMessage(error, "This invitation is invalid or has expired."));
      return;
    }
    // Auto-login: the accept set the session cookie. Hydrate the SPA's auth state
    // from /auth/me, then route into the org.
    const { data } = await api.GET("/api/v1/auth/me");
    setBusy(false);
    if (data) setUser(data);
    navigate("/dashboard", { replace: true });
  }

  if (!token) {
    return (
      <AuthLayout>
        <h1 className="text-xl font-semibold text-white">Invalid invitation link</h1>
        <p className="mt-2 text-sm text-slate-400">
          This link is missing its token. Ask your administrator to resend the invitation.
        </p>
        <Link to="/login" className="mt-5 inline-block text-xs text-slate-400 hover:text-slate-200">
          Go to sign in
        </Link>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <h1 className="text-xl font-semibold text-white">Accept your invitation</h1>
      <p className="mt-1 text-sm text-slate-400">Set up your account to join your organization.</p>
      <form onSubmit={submit} className="mt-5 space-y-4">
        <Field label="Your name">
          <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
        </Field>
        <Field label="Password">
          <Input type="password" value={password} onChange={(e) => setPassword(e.target.value)} required minLength={12} />
        </Field>
        <ErrorText>{error}</ErrorText>
        <Button type="submit" disabled={busy} className="w-full">
          {busy ? "Joining…" : "Accept invitation"}
        </Button>
      </form>
    </AuthLayout>
  );
}
