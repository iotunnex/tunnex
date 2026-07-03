package http

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// minimal valid bodies so a gated POST/PATCH passes spec validation and reaches
// the handler's authorization check (which is what we're asserting fails closed).
// keyed by lower(operationId) so a valid body accompanies gated POST/PATCH ops
// (otherwise the validator 400s on the missing body before auth is checked).
var walkBodies = map[string]string{
	"createorganization": `{"name":"Walk","slug":"walk-test"}`,
	"updateorganization": `{"name":"Walk"}`,
}

// TestSessionlessMutationsAre401 walks EVERY operation in the OpenAPI spec and
// asserts that operations requiring auth reject a sessionless request with 401.
// It is spec-driven, so any endpoint a future story adds is covered automatically
// — unless it opts out via `security: []` (the documented public allowlist).
func TestSessionlessRequestsAre401(t *testing.T) {
	swagger, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("GetSwagger: %v", err)
	}
	// No AuthFn => no principal is ever attached => every gated op must 401.
	router, err := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{Orgs: tenancy.NewService(nil)})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	srv := httptest.NewServer(router) // real server: faithful body handling
	defer srv.Close()

	checked := 0
	for path, item := range swagger.Paths.Map() {
		for method, op := range item.Operations() {
			public := op.Security != nil && len(*op.Security) == 0
			if public {
				continue
			}
			reqPath := strings.ReplaceAll(path, "{orgId}", uuid.NewString())

			var body io.Reader
			if b, ok := walkBodies[strings.ToLower(op.OperationID)]; ok {
				body = bytes.NewBufferString(b)
			}
			req, err := http.NewRequest(method, srv.URL+reqPath, body)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			rb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("%s %s (op %s): sessionless status = %d, want 401 — body: %s",
					method, path, op.OperationID, resp.StatusCode, string(rb))
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no gated operations checked — walk is vacuous")
	}
	t.Logf("verified %d gated operations reject sessionless requests with 401", checked)
}
