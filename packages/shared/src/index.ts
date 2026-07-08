// @tunnex/shared — the single source of API types + a typed client, all derived
// from openapi/openapi.yaml via `make generate`. Consumed by the web app, the
// Electron client, and the CLI. Do not hand-edit api.d.ts.

import createClient, { type Client } from "openapi-fetch";
import type { paths, components } from "./api";

export type { paths, components };

// Convenience aliases for the schemas used across the apps.
export type HealthResponse = components["schemas"]["HealthResponse"];
export type ApiError = components["schemas"]["Error"];

export type TunnexClient = Client<paths>;

const UNSAFE_METHODS = new Set(["POST", "PUT", "PATCH", "DELETE"]);

// Desktop transport switch (S6.2). The web SPA is same-origin (baseUrl "/"), but
// the Electron renderer loads over app:// and must reach the CONFIGURED server
// origin. setApiOrigin rewrites the ORIGIN of every request URL at the client
// layer — one bundle, runtime branch, no build fork. Null (the default) leaves
// requests same-origin, so the browser build is unaffected. Auth is untouched
// here: the desktop bearer is injected by the Electron MAIN process on the
// configured origin (S6.1), never by this middleware — the token never enters
// renderer JS.
let apiOrigin: string | null = null;
export function setApiOrigin(origin: string | null): void {
  apiOrigin = origin && origin.replace(/\/+$/, ""); // trim trailing slash; "" → null
}

/**
 * Create a typed Tunnex API client. baseUrl defaults to same-origin ("/").
 *
 * Every unsafe-method request carries the X-Tunnex-CSRF header AT THE CLIENT
 * LAYER, so no call site can forget it. The server's csrfGuard requires the
 * header whenever a request CARRIES the session cookie — including a stale,
 * already-revoked cookie — so pre-auth calls (login/signup/reset) need it too:
 * a browser holding a revoked session cookie would otherwise be locked out of
 * login until the cookie expires (Round-2 walk, bug B1). The header is
 * presence-only (a cross-site form cannot set custom headers), so sending it
 * on every mutation is always correct and never leaks anything.
 */
export function createTunnexClient(baseUrl = "/"): TunnexClient {
  const client = createClient<paths>({ baseUrl });
  client.use({
    onRequest({ request }) {
      let req = request;
      // Desktop: re-home the request onto the configured server origin (path +
      // query preserved). Rebuild rather than mutate (Request.url is read-only).
      if (apiOrigin) {
        const u = new URL(request.url);
        req = new Request(apiOrigin + u.pathname + u.search, request);
      }
      if (UNSAFE_METHODS.has(req.method)) {
        req.headers.set("X-Tunnex-CSRF", "1");
      }
      return req;
    },
  });
  return client;
}
