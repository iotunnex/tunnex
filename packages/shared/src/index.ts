// @tunnex/shared — the single source of TypeScript types shared by the web app,
// the Electron client, and the CLI's config schema.
//
// From S0.5 this file re-exports types generated from the OpenAPI spec. For the
// foundation story it carries just the health contract so the web app and API
// agree on the shape returned by /healthz.

export interface HealthResponse {
  status: "ok";
  service: string;
  request_id?: string;
}
