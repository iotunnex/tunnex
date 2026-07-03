import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The SPA is served as static files (by nginx in compose) and reused by the
// Electron renderer, so the build output is a plain static bundle.
export default defineConfig({
  plugins: [react()],
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
