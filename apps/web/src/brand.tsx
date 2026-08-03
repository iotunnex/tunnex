import logoUrl from "./assets/tunnex-logo.svg";
import wordmarkUrl from "./assets/tunnex-wordmark.svg";

// Brand is DATA, deliberately isolated to this one module (plus the color tokens
// in tailwind.config.js). Dropping the real logo/brand kit is then a ~2-file
// swap: replace <Logo> here and the palette there — no component touches brand
// details directly. The placeholder identity is intentionally restrained: a plain
// wordmark on a dark, security-product palette, not a decorative mark.

export const PRODUCT_NAME = "tunnex";
export const PRODUCT_TAGLINE = "self-hosted VPN & Zero Trust";

/**
 * Logo renders the real mark + wordmark.
 *
 * ⛔ THE BRAND KIT HAS LANDED, so the placeholder this file invited replacing is gone. It was a
 * plain accent square plus the text "tunnex.io" — which is why the mark was MISSING and the type
 * was wrong everywhere the shell renders it, not only on the login page.
 *
 * ⚠ AND `tunnex-wordmark.svg` IS ALREADY THE DARK-BACKGROUND VARIANT: white letters with the red
 * `nexgrad` on "EX". The first attempt applied `filter: invert(1)` to it, which turned the letters
 * black and — the visible symptom — **the red EX cyan**, because inverting #FF5A63 gives roughly
 * #00A59C. There is a separate `tunnex-wordmark-light.svg` (#17171A letters) for light surfaces;
 * neither needs a filter. **A filter that "fixes" a colour is a sign the wrong asset was chosen.**
 */
export function Logo({ className = "" }: { className?: string }) {
  return (
    <span className={`flex items-center gap-2.5 ${className}`}>
      <img src={logoUrl} alt="" aria-hidden className="h-7 w-7 rounded-lg" />
      <img src={wordmarkUrl} alt={PRODUCT_NAME} className="h-[15px]" />
    </span>
  );
}
