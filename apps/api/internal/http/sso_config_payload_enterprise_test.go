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

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/api"
	tcrypto "github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/rbac"
)

// TestGetSsoConfigPayloadCarriesNoSecret is the BLOCKING security assertion
// (gates job, `make test-editions`) that SATISFIES the twice-deferred S4.5
// secret-payload claim: the SSO config READ endpoint returns the keyed
// fingerprint but NEVER the client secret (plaintext or sealed). It exercises
// the REAL read handler against a live DB in the enterprise build — the
// substitute (settings.spec.ts:25) only checked the open-edition 403 gate, which
// proves the endpoint is hidden, NOT that its payload is secret-free when served.
//
// The config is written via a DIRECT UpsertSSOConfig (a sealed secret + keyed
// fingerprint), NOT the audited ConfigService.Set — an audit_logs row is
// append-only (its org FK is SET NULL, which the append-only trigger REFUSES), so
// an audited write would make the org un-deletable and the test would leak rows
// into the shared compose DB. The READ path under assertion is unchanged.
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
	org := uuid.New()
	// Cleanup DELETEs the org (cascading sso_configs) THEN closes the pool. A
	// separate `defer pool.Close()` would run BEFORE t.Cleanup (own-defers unwind
	// before registered cleanups), leaving the delete to hit a closed pool and
	// silently leak the org into the shared DB (inflating countRealOrgs, tripping
	// the seed guard). No audit row is created, so the org delete is unblocked.
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), 10*time.Second)
		defer cc()
		_, _ = pool.Exec(c, "DELETE FROM organizations WHERE id=$1", org) // cascades sso_configs
		pool.Close()
	})

	if _, err := pool.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)",
		org, "O", "s45-"+org.String()); err != nil {
		t.Fatalf("org: %v", err)
	}

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

	// Store a config with a KNOWN sealed secret + its keyed fingerprint (un-audited).
	sealed, err := sealer.Seal([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlc.New(pool).UpsertSSOConfig(ctx, sqlc.UpsertSSOConfigParams{
		OrgID:              org,
		Provider:           "google",
		ClientID:           clientID,
		ClientSecretSealed: []byte(sealed),
		SecretFingerprint:  sealer.Fingerprint([]byte(secret)),
		Enabled:            true,
	}); err != nil {
		t.Fatalf("upsert sso config: %v", err)
	}

	// Call the ACTUAL read handler as an authorized owner.
	port := NewSSOPort(pool, sealer, nil, "", slog.Default())
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
