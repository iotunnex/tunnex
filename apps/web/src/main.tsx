import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { setApiOrigin } from "@tunnex/shared";
// Self-hosted brand fonts (bundled by Vite — no CDN, works fully offline/on-prem).
import "@fontsource-variable/inter";
import "@fontsource-variable/jetbrains-mono";
import App from "./App";
import { desktop } from "./lib/desktop";
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
        <App />
      </BrowserRouter>
    </React.StrictMode>,
  );
}

void boot();
