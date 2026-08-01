import tokens from "../../packages/shared/generated/tokens.palette.json" with { type: "json" };

/** @type {import('tailwindcss').Config} */
// S14.1: NO HARDCODED HEX LIVES HERE. The palette is `var(--tnx-*)` references GENERATED from the one authored
// form (packages/shared/src/tokens.ts) by `make generate`, and drift is caught by `make generate-check` —
// exactly as api.d.ts and rbac-policy.json already are.
//
// The config reads JSON rather than TypeScript deliberately. Config files load through Node, which cannot read
// a raw .ts entry; importing the .ts by relative path deadlocks TypeScript's project references. Consuming a
// generated artifact removes the whole class of problem.
//
// The semantic reservation (ok = LIVENESS ONLY, never success feedback — S4.4 decision f) travels with the
// tokens as DATA and is asserted in apps/web/test/tokens.test.ts. It used to live in a comment here, which is
// exactly how such a rule dies during a migration.
export default {
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: { extend: { colors: tokens.colors, fontFamily: tokens.fontFamily } },
  plugins: [],
};
