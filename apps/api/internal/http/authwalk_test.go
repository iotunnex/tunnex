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
	"setssoconfig":       `{"client_id":"x","client_secret":"y","enabled":true}`,
	"createdomainclaim":  `{"domain":"walk.example.com"}`,
	"verifydomainclaim":  `{"domain":"walk.example.com"}`,
	"createinvitation":   `{"email":"walk@example.com","role":"member"}`,
	"changememberrole":   `{"role":"member"}`,
	"resizepool":         `{"cidr":"10.0.0.0/24"}`,
	"resendinvitation":   `{"email":"walk@example.com"}`,
	"revokeinvitation":   `{"email":"walk@example.com"}`,
	"issuejointoken":     `{"node_name":"walk-node"}`,
	"createdevice":       `{"name":"walk-device","node_id":"00000000-0000-0000-0000-000000000000"}`,
	// S5.1 CLI-auth gated ops (cliToken/cliDeviceStart/cliDeviceToken are public).
	"cliauthorize":     `{"redirect_uri":"http://127.0.0.1:1/callback","code_challenge":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","state":"walk"}`,
	"clideviceapprove": `{"user_code":"WALK-CODE"}`,
	// S7.5.5 MFA: enroll/confirm is session-gated (mfaVerify is public; enroll-start/disenroll have no body).
	"mfaenrollconfirm": `{"code":"123456"}`,
	"setmfaenforce":    `{"enforce":false}`,
	// S7.1 Zero Trust policy gated ops (all enterprise; each still 401s sessionless).
	"creategroup":      `{"name":"Walk"}`,
	"updategroup":      `{"name":"Walk"}`,
	"addgroupmember":   `{"user_id":"00000000-0000-0000-0000-000000000000"}`,
	"createresource":   `{"name":"Walk","cidr":"10.0.0.0/24","protocol":"any"}`,
	"updateresource":   `{"name":"Walk","cidr":"10.0.0.0/24","protocol":"any"}`,
	"createpolicyrule": `{"src_group_id":"00000000-0000-0000-0000-000000000000","dst_kind":"group","dst_group_id":"00000000-0000-0000-0000-000000000000"}`,
	"extendgrant":      `{"expires_at":"2099-01-01T00:00:00Z"}`,
	// S8.1 site-to-site gated ops (site:manage; each still 401s sessionless. approveSiteSubnet + listPending have no body).
	"registersite":      `{"name":"Walk"}`,
	"addsitesubnet":     `{"cidr":"10.20.0.0/24"}`,
	"bindsitenode":      `{"node_id":"00000000-0000-0000-0000-000000000000"}`,
	"setzerotrustmode":  `{"mode":"off"}`,
	"setdeviceapproval": `{"mode":"off"}`,
	// S7.5.2 IdP-group sync gated ops (enterprise; each still 401s sessionless).
	"putidpsyncconfig": `{"client_id":"x","client_secret":"y"}`,
	"mapidpgroup":      `{"idp_group_id":"grp-walk"}`,
	// S7.5.3 device health gated ops (enterprise; each still 401s sessionless).
	"puthealthcheck":     `{"mode":"warn"}`,
	"reportdevicehealth": `{"platform":"macos","os_version":"14.0","disk_encrypted":true}`,
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
			reqPath = strings.ReplaceAll(reqPath, "{provider}", "google")
			reqPath = strings.ReplaceAll(reqPath, "{userId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{nodeId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{deviceId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{credentialId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{groupId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{resourceId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{ruleId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{siteId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{subnetId}", uuid.NewString())
			reqPath = strings.ReplaceAll(reqPath, "{checkKind}", "disk_encryption")

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
