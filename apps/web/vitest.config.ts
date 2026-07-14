import { defineConfig } from "vitest/config";

// S7.4a: view-model unit tests only — the pure functions in src/lib that encode the
// consequential decisions (mode-enable count copy, create-then-delete rule swap, the
// deleted-vs-unresolved rule-label join, the policy RBAC/edition gate). No DOM: these
// are pure functions, deliberately kept out of the React components so they test
// without jsdom / testing-library (kit-minimum — no component-render harness).
export default defineConfig({
  test: {
    environment: "node",
    include: ["test/**/*.test.ts"],
  },
});
