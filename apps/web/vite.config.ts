import { defineConfig, type Plugin } from "vite";
import react from "@vitejs/plugin-react";
// Imported from the BUILT tokens (`@tunnex/shared/tokens` -> dist/tokens.js). Config files load through
// Node, which cannot read a raw .ts entry; and importing the .ts by relative path deadlocks TypeScript's
// project references. One authored form (src/tokens.ts), one emitted form, no second hand-maintained copy.
import { themeCss } from "@tunnex/shared/tokens";

// S14.1 — THE DESIGN TOKENS' CSS, EMITTED FROM THE SAME TS SOURCE THE TAILWIND CONFIG READS.
//
// A VIRTUAL MODULE rather than a checked-in .css artifact, and rather than a build step in packages/shared.
// The founder approved a build step to guarantee ONE AUTHORED FORM; this achieves the same guarantee more
// cheaply, because there is no emitted file to drift. Two hand-maintained copies of the same values is
// fixture-restates-production applied to design tokens: they diverge, and the divergence is invisible because
// both still render something.
//
// It is ~10 lines and adds no dependency, no package build, and no artifact to commit.
function tunnexTokens(): Plugin {
  const id = "virtual:tunnex-tokens.css";
  const resolved = "\0" + id;
  return {
    name: "tunnex-tokens",
    resolveId: (source) => (source === id ? resolved : null),
    load: (i) => (i === resolved ? themeCss() : null),
  };
}

// The SPA is served as static files (by nginx in compose) and reused by the
// Electron renderer, so the build output is a plain static bundle.
export default defineConfig({
  plugins: [tunnexTokens(), react()],
  server: {
    host: true,
    port: 5173,
    // In `pnpm dev`, proxy API calls to the local API so the dev experience
    // matches the nginx-proxied production path (relative /api and /healthz).
    proxy: {
      "/api": "http://localhost:8080",
      "/healthz": "http://localhost:8080",
    },
  },
  build: {
    outDir: "dist",
    sourcemap: true,
  },
});
