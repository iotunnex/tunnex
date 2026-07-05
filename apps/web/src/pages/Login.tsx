import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { Logo, PRODUCT_TAGLINE } from "../brand";
import { api, apiErrorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { Button, Card, ErrorText, Field, Input } from "../components/ui";
import { HealthStatus } from "../components/HealthStatus";

/** Login is the public entry. Full auth screens (signup, SSO, reset) arrive in
 * S4.2; this is the functional sign-in that gates the shell. */
export default function Login() {
  const { setUser } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    const { data, error } = await api.POST("/api/v1/auth/login", { body: { email, password } });
    setBusy(false);
    if (error || !data) {
      setError(apiErrorMessage(error, "Invalid email or password."));
      return;
    }
    setUser(data);
    navigate("/devices", { replace: true });
  }

  return (
    <div className="flex min-h-full flex-col">
      <main className="grid flex-1 place-items-center px-6">
        <div className="w-full max-w-sm">
          <div className="mb-6 flex justify-center">
            <Logo />
          </div>
          <Card>
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
          </Card>
        </div>
      </main>
      <footer className="flex items-center justify-between px-6 py-4 text-xs text-slate-600">
        <HealthStatus />
        <span>{PRODUCT_TAGLINE}</span>
      </footer>
    </div>
  );
}
