package http

import (
	"net/http"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/routers/gorillamux"
	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
)

// TestEnrollmentGateSelfArming drives the gate's resolve+allowlist decision over EVERY operation in
// the OpenAPI spec and asserts: a MFA-enrollment-gated user is allowed EXACTLY the allowlist and
// DENIED everything else. It is spec-driven, so a future endpoint is gated BY CONSTRUCTION — a new
// op that isn't explicitly allowlisted resolves to allowed=false (fail-closed), and the test proves
// it. It also proves every real op RESOLVES (no fail-closed false-deny on a legit route) and that
// cliAuthorize + disenroll are NOT reachable while gated.
func TestEnrollmentGateSelfArming(t *testing.T) {
	swagger, err := api.GetSwagger()
	if err != nil {
		t.Fatalf("GetSwagger: %v", err)
	}
	swagger.Servers = nil // match paths as-is (we run behind nginx)
	oapiRouter, err := gorillamux.NewRouter(swagger)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	fill := func(p string) string {
		for _, param := range []string{"orgId", "userId", "nodeId", "deviceId", "credentialId", "groupId", "resourceId", "ruleId"} {
			p = strings.ReplaceAll(p, "{"+param+"}", uuid.NewString())
		}
		p = strings.ReplaceAll(p, "{provider}", "google")
		p = strings.ReplaceAll(p, "{checkKind}", "disk_encryption")
		return p
	}
	checked := 0
	for path, item := range swagger.Paths.Map() {
		for method, op := range item.Operations() {
			req, err := http.NewRequest(method, "http://x"+fill(path), nil)
			if err != nil {
				t.Fatalf("build %s %s: %v", method, path, err)
			}
			got := gateAllows(oapiRouter, req)
			want := enrollmentGateAllow[op.OperationID]
			if got != want {
				t.Errorf("gated user on %s %s (op %q): allowed=%v, want %v", method, path, op.OperationID, got, want)
			}
			checked++
		}
	}
	if checked < 40 {
		t.Fatalf("walk only covered %d operations — spec load looks wrong", checked)
	}
	// Belt-and-suspenders: the two operations the fork explicitly excluded must be denied.
	for _, op := range []string{"mfaDisenroll", "cliAuthorize"} {
		if enrollmentGateAllow[op] {
			t.Fatalf("%s must NOT be on the enrollment-gate allowlist", op)
		}
	}
}
