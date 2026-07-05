// Brand is DATA, deliberately isolated to this one module (plus the color tokens
// in tailwind.config.js). Dropping the real logo/brand kit is then a ~2-file
// swap: replace <Logo> here and the palette there — no component touches brand
// details directly. The placeholder identity is intentionally restrained: a plain
// wordmark on a dark, security-product palette, not a decorative mark.

export const PRODUCT_NAME = "tunnex";
export const PRODUCT_TAGLINE = "self-hosted VPN & Zero Trust";

/** Logo renders the wordmark. Swap the mark + type here when the brand kit lands. */
export function Logo({ className = "" }: { className?: string }) {
  return (
    <span className={`flex items-center gap-2 ${className}`}>
      <span
        aria-hidden
        className="inline-block h-5 w-5 rounded-md bg-accent-500 shadow-[0_0_20px] shadow-accent-500/40"
      />
      <span className="text-lg font-semibold tracking-tight text-white">
        {PRODUCT_NAME}
        <span className="text-accent-400">.io</span>
      </span>
    </span>
  );
}
