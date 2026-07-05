import { cloneElement, isValidElement, useId } from "react";
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactElement, ReactNode } from "react";

// A small, deliberate set of primitives — enough to compose the app's pages
// consistently without a heavyweight component library. Colors come only from the
// theme tokens (accent/ink/slate), so a palette swap restyles everything.

export function Button({
  variant = "primary",
  className = "",
  ...props
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "ghost" | "danger" }) {
  const base =
    "inline-flex items-center justify-center rounded-md px-4 py-2 text-sm font-medium transition-colors disabled:opacity-50 disabled:pointer-events-none focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent-400";
  const variants = {
    primary: "bg-accent-500 text-white hover:bg-accent-600",
    ghost: "border border-white/10 text-slate-200 hover:bg-white/5",
    danger: "text-slate-400 hover:text-rose-400",
  } as const;
  return <button className={`${base} ${variants[variant]} ${className}`} {...props} />;
}

export function Card({ className = "", children }: { className?: string; children: ReactNode }) {
  return <div className={`rounded-xl border border-white/5 bg-ink-800 p-5 ${className}`}>{children}</div>;
}

export function Field({ label, children }: { label: string; children: ReactNode }) {
  // Explicit id/htmlFor association (not just implicit wrapping) so the label
  // stays linked to the control even once helper/error text is added, and the
  // accessible name is exactly the label — not the concatenated subtree text.
  const id = useId();
  const control = isValidElement(children)
    ? cloneElement(children as ReactElement<{ id?: string }>, { id })
    : children;
  return (
    <div className="block">
      <label htmlFor={id} className="block text-sm text-slate-300">
        {label}
      </label>
      <span className="mt-1 block">{control}</span>
    </div>
  );
}

export function Input({ className = "", ...props }: InputHTMLAttributes<HTMLInputElement>) {
  return (
    <input
      className={`w-full rounded-md border border-white/10 bg-ink-900 px-3 py-2 text-sm text-white placeholder:text-slate-600 focus-visible:outline focus-visible:outline-2 focus-visible:outline-accent-400 ${className}`}
      {...props}
    />
  );
}

/** StatusDot: a small colored dot for online/offline/neutral state. */
export function StatusDot({ tone }: { tone: "on" | "off" | "warn" }) {
  const cls = { on: "bg-accent-400", off: "bg-slate-600", warn: "bg-amber-400" }[tone];
  return <span className={`inline-block h-1.5 w-1.5 rounded-full ${cls}`} />;
}

export function ErrorText({ children }: { children: ReactNode }) {
  return children ? <p className="text-xs text-rose-400">{children}</p> : null;
}
