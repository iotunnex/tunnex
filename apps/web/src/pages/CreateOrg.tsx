import { useState, type FormEvent } from "react";
import { Link, Navigate, useNavigate } from "react-router-dom";
import { PRODUCT_NAME } from "../brand";
import { api, CSRF, apiErrorCode, apiErrorMessage } from "../lib/api";
import { useAuth } from "../lib/auth";
import { AuthLayout } from "../components/AuthLayout";
import { Button, ErrorText, Field, Input } from "../components/ui";

// slugify derives a URL slug from the org name, matching the server's slug
// pattern (^[a-z0-9]+(-[a-z0-9]+)*$): lowercase, non-alphanumerics collapse to a
// single hyphen, and leading/trailing hyphens are trimmed.
function slugify(s: string): string {
  return s
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

/**
 * CreateOrg is the explicit create-organization step in the onboarding funnel
 * (S4.7): a freshly-verified user with zero memberships lands here (routed by
 * RequireOrg) instead of a dead-end dashboard. The SSO-JIT and invite paths never
 * reach here — they already produce a membership.
 *
 * Two refusals are surfaced honestly rather than hidden:
 *  - Unverified email: create-org is verified-gated server-side (requireVerifiedUser),
 *    so we route to /verify-pending up front — the refusal is structural, not a
 *    surprise 403 after the user fills in the form.
 *  - Single-org cap (open edition): the server owns the limit (org_limit_reached);
 *    on that code we swap the form for an invitation-only message. The UI mirrors
 *    the server's truth, it never invents the permission.
 */
export default function CreateOrg() {
  const { state } = useAuth();
  const navigate = useNavigate();
  const [name, setName] = useState("");
  // The slug tracks the name until the user edits it directly (then it sticks).
  const [slug, setSlug] = useState("");
  const [slugEdited, setSlugEdited] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [capped, setCapped] = useState(false);
  const [busy, setBusy] = useState(false);

  // Verified-email gate (decision 3): unverified users can't create an org, so
  // route them to verify first rather than let the POST 403 after data entry.
  if (state.status === "authed" && !state.user.email_verified) {
    return <Navigate to="/verify-pending" replace />;
  }

  // The slug tracks the name (slugify) until the user edits the slug directly;
  // slugify() runs again at submit to trim any transient trailing hyphen.
  const effectiveSlug = slugEdited ? slug : slugify(name);
  const finalSlug = slugify(effectiveSlug);

  function onSlug(v: string) {
    // Lowercase and collapse invalid runs to a single hyphen, but do NOT trim a
    // trailing hyphen here — that would delete the '-' the instant it's typed, so
    // "acme-corp" couldn't be entered left-to-right. Trailing hyphens are trimmed
    // by slugify() at submit. An emptied field unlatches back to name-derived.
    const cleaned = v.toLowerCase().replace(/[^a-z0-9-]+/g, "-");
    setSlug(cleaned);
    setSlugEdited(cleaned.trim() !== "");
  }

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const { data, error } = await api.POST("/api/v1/organizations", {
        headers: CSRF,
        body: { name: name.trim(), slug: finalSlug },
      });
      if (error || !data) {
        // Cap reached: before showing the dead-end, RE-CHECK membership. Between
        // the funnel routing us here (0 orgs) and this refusal, the user may have
        // gained a membership (an invite accepted in another tab, JIT-join, or an
        // admin adding them) — that user belongs in the dashboard, not a dead-end.
        // Only a still-0-membership user sees the invitation-only card (branch 3).
        if (apiErrorCode(error) === "org_limit_reached") {
          const { data: orgs } = await api.GET("/api/v1/organizations");
          if ((orgs?.length ?? 0) > 0) return navigate("/dashboard", { replace: true });
          return setCapped(true);
        }
        return setError(apiErrorMessage(error, "Could not create the organization."));
      }
      navigate("/dashboard", { replace: true });
    } catch {
      // A network-level failure rejects instead of returning {error}; without this
      // the button would stay stuck on "Creating…".
      setError("Could not reach the API.");
    } finally {
      setBusy(false);
    }
  }

  if (capped) {
    return (
      <AuthLayout>
        <h1 className="text-xl font-semibold text-white">Invitation required</h1>
        <p className="mt-2 text-sm text-slate-400">
          This {PRODUCT_NAME} deployment already has an organization and the open edition supports a single one. Ask an
          administrator to invite you, then sign in to accept.
        </p>
        <Link to="/login" className="mt-5 inline-block text-xs text-slate-400 hover:text-slate-200">
          Back to sign in
        </Link>
      </AuthLayout>
    );
  }

  return (
    <AuthLayout>
      <h1 className="text-xl font-semibold text-white">Create your organization</h1>
      <p className="mt-1 text-sm text-slate-400">
        One more step — name the organization that will own your gateways, devices, and members.
      </p>
      <form onSubmit={submit} className="mt-5 space-y-4">
        <Field label="Organization name">
          <Input value={name} onChange={(e) => setName(e.target.value)} required autoFocus placeholder="Acme Corp" />
        </Field>
        <Field label="Slug">
          <Input value={effectiveSlug} onChange={(e) => onSlug(e.target.value)} required placeholder="acme-corp" />
        </Field>
        <ErrorText>{error}</ErrorText>
        <Button type="submit" disabled={busy || !name.trim() || !finalSlug} className="w-full">
          {busy ? "Creating…" : "Create organization"}
        </Button>
      </form>
    </AuthLayout>
  );
}
