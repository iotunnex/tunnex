import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { setApiOrigin } from "@tunnex/shared";
// Self-hosted brand fonts (bundled by Vite — no CDN, works fully offline/on-prem).
import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import App from "./App";
import { desktop } from "./lib/desktop";
import { LayoutCapabilityProvider } from "./components/ComposeGate";
import { MotionProvider } from "./components/MotionProvider";
import { ToastProvider } from "./components/Toasts";
// S14.1: the design tokens' CSS custom properties. GENERATED from packages/shared/src/tokens.ts by
// `make generate` and drift-guarded by `make generate-check`. Imported FIRST so `:root` carries the variables
// before any Tailwind utility resolves them.
import "../../../packages/shared/generated/tokens.css";
import "./index.css";

// Desktop transport bootstrap (S6.2): before ANY request (the first is
// /auth/me), point the API client at the configured server origin. The web
// build skips this (no bridge) and stays same-origin. Rendering waits so no
// request can fire same-origin first.
async function boot() {
  const bridge = desktop();
  if (bridge) {
    try {
      const origin = await bridge.config.getServerUrl();
      if (origin) setApiOrigin(origin);
    } catch {
      /* no server configured yet — the setup screen handles it in main */
    }
  }
  ReactDOM.createRoot(document.getElementById("root")!).render(
    <React.StrictMode>
      <BrowserRouter>
        {/* S14.2: the viewport is measured ONCE, here, and handed down as a CAPABILITY. No screen reads a
            pixel width; screens declare what they compose and let the gate decide. */}
        <LayoutCapabilityProvider>
          {/* S14.3 slice B: the motion preference and the toast surface are both APP-EDGE concerns —
              measured/owned once, consumed everywhere, never re-derived per screen. */}
          <MotionProvider>
            <ToastProvider>
              <App />
            </ToastProvider>
          </MotionProvider>
        </LayoutCapabilityProvider>
      </BrowserRouter>
    </React.StrictMode>,
  );
}

void boot();
