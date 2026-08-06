import { useOrg } from "../lib/useOrg";

/**
 * OrgSwitcher selects which organization the UI acts on.
 *
 * ⛔ IT RENDERS NOTHING WHEN THE CALLER HAS ONE ORGANIZATION, and that is not a cosmetic choice. Almost
 * every user has exactly one; a permanent control offering a choice that does not exist trains people to
 * ignore the place where a real choice will later appear.
 *
 * ⚠ SO ITS ABSENCE IS INFORMATION: seeing it means you belong to more than one tenant. That is a fact worth
 * knowing before you delete something.
 *
 * ⭐ AND IT GRANTS NOTHING. Selecting here changes which orgId the pages send; the server resolves the
 * caller's role from its own per-request membership query and answers 404 for anything else. This control
 * can only pick among organizations the server would already authorize.
 */
export function OrgSwitcher() {
  const { orgs, org, setOrg } = useOrg();

  if (orgs.length < 2 || !org) return null;

  return (
    <label className="flex min-w-0 items-center gap-2">
      {/* Labelled for screen readers without spending header width — the control's purpose is legible from
          its content (it shows the current org name), so a visible label would be redundant, but an
          unlabelled <select> in a header is unnavigable. */}
      <span className="sr-only">Organization</span>
      <select
        aria-label="Organization"
        value={org.id}
        onChange={(e) => setOrg(e.target.value)}
        className="min-w-0 max-w-[180px] truncate rounded-input border border-line bg-surface-inset px-2 py-[6px] text-cell text-ink-body"
      >
        {orgs.map((o) => (
          <option key={o.id} value={o.id}>
            {o.name}
          </option>
        ))}
      </select>
    </label>
  );
}
