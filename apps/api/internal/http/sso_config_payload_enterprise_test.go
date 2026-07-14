//go:build enterprise

package http

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/api"
	tcrypto "github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// TestGetSsoConfigPayloadCarriesNoSecret is the BLOCKING security assertion
// (gates job, `make test-editions`) that SATISFIES the twice-deferred S4.5
// secret-payload claim: the SSO config READ endpoint returns the keyed
// fingerprint but NEVER the client secret (plaintext or sealed). It exercises
// the REAL handler against a live DB in the enterprise build — the substitute
// (settings.spec.ts:25) only checked the open-edition 403 gate, which proves the
// endpoint is hidden, NOT that its payload is secret-free when it IS served.
func TestGetSsoConfigPayloadCarriesNoSecret(t *testing.T) {
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// A real org + actor to satisfy FKs (Set writes an audit row referencing both).
	org := uuid.New()
	actor := uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)",
		org, "O", "s45-"+org.String()); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,'Actor')",
		actor, actor.String()+"@t.local"); err != nil {
		t.Fatalf("actor: %v", err)
	}
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(c, "DELETE FROM organizations WHERE id=$1", org) // cascades sso_configs + audit_logs
		_, _ = pool.Exec(c, "DELETE FROM users WHERE id=$1", actor)
	})

	masterKey := make([]byte, tcrypto.KeySize)
	if _, err := rand.Read(masterKey); err != nil {
		t.Fatal(err)
	}
	sealer, err := tcrypto.NewSealer(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "super-secret-google-client-secret-payload-guard"
	const clientID = "client-id-abc.apps.googleusercontent.com"

	// Real enterprise SSO port; seal + store a config with a KNOWN secret.
	port := NewSSOPort(pool, sealer, nil, "", slog.Default())
	if err := port.SetConfig(ctx, actor, org, "google", clientID, secret, "", true); err != nil {
		t.Fatalf("set config: %v", err)
	}

	// Call the ACTUAL read handler as an authorized owner.
	s := apiServer{sso: port}
	authed := principalWithRole(org, rbac.RoleOwner)
	resp, err := s.GetSsoConfig(authed, api.GetSsoConfigRequestObject{OrgId: org, Provider: "google"})
	if err != nil {
		t.Fatalf("GetSsoConfig: %v", err)
	}
	ok, isOK := resp.(api.GetSsoConfig200JSONResponse)
	if !isOK {
		t.Fatalf("want 200 JSON response, got %T", resp)
	}

	raw, err := json.Marshal(ok.Body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	blob := string(raw)

	// (1) The secret must not appear anywhere in the wire payload.
	if strings.Contains(blob, secret) {
		t.Fatalf("payload leaked the client secret: %s", blob)
	}
	// (2) No field named client_secret is serialized.
	if strings.Contains(strings.ToLower(blob), "client_secret") {
		t.Fatalf("payload exposes a client_secret field: %s", blob)
	}
	// (3) The keyed fingerprint IS present — proves which secret is stored without
	//     ever revealing it (the positive half of the S4.5 assertion).
	wantFP := sealer.Fingerprint([]byte(secret))
	if !strings.Contains(blob, wantFP) {
		t.Fatalf("payload missing secret_fingerprint %q: %s", wantFP, blob)
	}
	// (4) The client id IS surfaced — the View is functional, not an empty stub.
	if !strings.Contains(blob, clientID) {
		t.Fatalf("payload missing client_id: %s", blob)
	}
}
