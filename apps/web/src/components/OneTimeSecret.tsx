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
// legacyCopy copies via a throwaway <textarea> + document.execCommand("copy") —
// the only clipboard path available in an insecure (plain-HTTP) context, where
// the async Clipboard API is undefined. Returns whether the copy succeeded.
function legacyCopy(text: string): boolean {
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch {
    return false;
  }
}

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
  const [copyFailed, setCopyFailed] = useState(false);

  async function copy() {
    try {
      // The async Clipboard API only exists in a SECURE context (HTTPS or
      // localhost). A plain-HTTP self-host (e.g. http://<ip>) — exactly the POC
      // case — leaves navigator.clipboard UNDEFINED, so fall back to the legacy
      // execCommand path, which works off-HTTPS.
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(secret);
      } else if (!legacyCopy(secret)) {
        throw new Error("clipboard unavailable");
      }
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Never fail silently — tell the user to select + copy by hand.
      setCopyFailed(true);
      setTimeout(() => setCopyFailed(false), 4000);
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
              {copied ? "Copied" : copyFailed ? "Select + ⌘C to copy" : copyLabel}
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
