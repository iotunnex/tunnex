import { useState, type ReactNode } from "react";
import { Button, StatusDot } from "./ui";

/**
 * OneTimeSecretModal is the shared "shown once" ceremony for the app's most
 * security-sensitive moments — a device config (with its private key) and a
 * gateway join token. The secret exists only in the caller's page state, is never
 * re-fetched (the server serves it exactly once), and must be explicitly
 * acknowledged to dismiss; navigating away discards it.
 *
 * Kept as ONE component so both ceremonies can't drift — a hardening of the reveal
 * (e.g. copy-confirmation, redaction) lands in a single place. `leadingActions`
 * lets a caller add extra buttons (e.g. Download) beside the built-in Copy.
 */
export function OneTimeSecretModal({
  title,
  caption,
  secret,
  copyLabel = "Copy",
  leadingActions,
  onDismiss,
}: {
  title: string;
  caption: ReactNode;
  secret: string;
  copyLabel?: string;
  leadingActions?: ReactNode;
  onDismiss: () => void;
}) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard denied — the user can still select the text */
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-ink-950/80 p-4">
      <div className="w-full max-w-lg rounded-xl border-2 border-warn/60 bg-ink-800 p-5 shadow-2xl">
        <div className="flex items-center gap-2">
          <StatusDot tone="warn" />
          <span className="text-sm font-semibold text-warn">{title}</span>
        </div>
        <p className="mt-2 text-xs text-slate-400">{caption}</p>
        <pre className="mt-3 max-h-64 overflow-auto rounded-md bg-ink-950 p-3 font-mono text-xs text-slate-300">
          {secret}
        </pre>
        <div className="mt-4 flex items-center justify-between">
          <div className="flex gap-2">
            {leadingActions}
            <Button variant="ghost" onClick={copy}>
              {copied ? "Copied" : copyLabel}
            </Button>
          </div>
          <Button variant="ghost" onClick={onDismiss}>
            I&rsquo;ve saved it
          </Button>
        </div>
      </div>
    </div>
  );
}
