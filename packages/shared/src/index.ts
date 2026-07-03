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

/** Create a typed Tunnex API client. baseUrl defaults to same-origin ("/"). */
export function createTunnexClient(baseUrl = "/"): TunnexClient {
  return createClient<paths>({ baseUrl });
}
