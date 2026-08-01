import type { Config } from "tailwindcss";
// From the BUILT tokens, same reason as vite.config.ts: config files load through Node.
import { tailwindColors, TYPOGRAPHY } from "@tunnex/shared/tokens";

// S14.1: NO HARDCODED HEX LIVES HERE ANY MORE. The palette is `var(--tnx-*)` references emitted by
// @tunnex/shared, so a theme swap is one attribute on <html> and needs no rebuild. Every existing utility
// class (`bg-ink-900`, `text-danger`, …) keeps working unchanged — which is the point: this slice must not
// alter rendering, and the proof is that no markup and no test changed.
//
// The semantic reservation (ok = LIVENESS ONLY, never success feedback — S4.4 decision f) travels with the
// tokens as DATA and is asserted in apps/web/test/tokens.test.ts. It used to live in a comment here, which is
// precisely how such a rule dies during a migration.
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: tailwindColors(),
      fontFamily: { sans: [...TYPOGRAPHY.sans], mono: [...TYPOGRAPHY.mono] },
    },
  },
  plugins: [],
} satisfies Config;
