package http

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/cliauth"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/tenancy"
)

// S5.1 bearer-credential semantics at the FULL router (middleware chain incl.
// csrfGuard + spec validator):
//   - a valid bearer authenticates exactly like a cookie (bearer ≡ cookie);
//   - a bearer MUTATION needs no cookie and no X-Tunnex-CSRF header (csrfGuard
//     is cookie-keyed and therefore inert for the CLI — D3's point);
//   - a REVOKED bearer is a generic 401 (no revocation oracle);
//   - an EXPIRED bearer is a DISTINCT 401 credential_expired (the CLI's
//     "run 'tunnex login'" UX hangs off this code).
func TestBearerCredentialSemantics(t *testing.T) {
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	// A real (non-tx) user: the router's queries run on the pool. Cleanup cascades.
	userID := uuid.New()
	email := "bearer-" + userID.String() + "@walk.local"
	if _, err := pool.Exec(ctx,
		"INSERT INTO users (id,email,name,email_verified_at) VALUES ($1,$2,$3,now())", userID, email, "Bearer T"); err != nil {
		t.Fatalf("user: %v", err)
	}
	defer pool.Exec(context.Background(), "DELETE FROM audit_logs WHERE actor_user_id=$1", userID) //nolint:errcheck
	defer pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", userID)                 //nolint:errcheck

	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	sealer, _ := crypto.NewSealer(key)
	svc := cliauth.NewService(pool, sealer)

	router, err := NewRouter(slog.New(slog.NewTextHandler(io.Discard, nil)), Deps{
		Orgs:    tenancy.NewService(pool),
		CliAuth: svc,
		BearerFn: BearerAuth(sqlc.New(pool)),
	})
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	srv := httptest.NewServer(router)
	defer srv.Close()

	// Mint two credentials via the REAL loopback exchange.
	mint := func() cliauth.Credential {
		vb := make([]byte, 32)
		_, _ = rand.Read(vb)
		verifier := base64.RawURLEncoding.EncodeToString(vb)
		ch := sha256.Sum256([]byte(verifier))
		code, _, err := svc.MintAuthCode(ctx, userID, "http://127.0.0.1:7/callback", base64.RawURLEncoding.EncodeToString(ch[:]))
		if err != nil {
			t.Fatalf("mint code: %v", err)
		}
		cred, err := svc.ExchangeCode(ctx, code, verifier, "http://127.0.0.1:7/callback")
		if err != nil {
			t.Fatalf("exchange: %v", err)
		}
		return cred
	}
	cred1, cred2 := mint(), mint()

	do := func(method, path, bearer string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, nil)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// bearer ≡ cookie: an org-gated endpoint accepts the bearer principal.
	if resp := do("GET", "/api/v1/organizations", cred1.Token); resp.StatusCode != 200 {
		t.Fatalf("bearer on gated GET: want 200, got %d", resp.StatusCode)
	}
	// Self-scoped list works and never contains token material.
	resp := do("GET", "/api/v1/auth/cli/credentials", cred1.Token)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), cred1.Fingerprint) {
		t.Fatalf("list: %d %s", resp.StatusCode, body)
	}
	if strings.Contains(string(body), cred1.Token) || strings.Contains(string(body), cred2.Token) {
		t.Fatal("credential list leaked token material")
	}

	// THE CSRF-INERT PROOF: an unsafe-method mutation with a bearer, NO cookie,
	// NO X-Tunnex-CSRF header — csrfGuard must not interfere (it is cookie-keyed).
	var cred2ID string
	for _, line := range strings.Split(string(body), "},") {
		if strings.Contains(line, cred2.Fingerprint) {
			cred2ID = line[strings.Index(line, `"id":"`)+6:]
			cred2ID = cred2ID[:36]
		}
	}
	if cred2ID == "" {
		t.Fatal("could not locate cred2 id in list")
	}
	if resp := do("DELETE", "/api/v1/auth/cli/credentials/"+cred2ID, cred1.Token); resp.StatusCode != 204 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("bearer DELETE without CSRF header: want 204, got %d — %s", resp.StatusCode, b)
	}

	// REVOKED bearer → generic 401 (cred2 was just revoked).
	resp = do("GET", "/api/v1/auth/cli/credentials", cred2.Token)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 401 || strings.Contains(string(body), "credential_expired") {
		t.Fatalf("revoked bearer: want generic 401, got %d — %s", resp.StatusCode, body)
	}

	// EXPIRED bearer → DISTINCT 401 credential_expired.
	h := sha256.Sum256([]byte(cred1.Token))
	if _, err := pool.Exec(ctx, "UPDATE cli_credentials SET expires_at = now() - interval '1 second' WHERE token_hash=$1", h[:]); err != nil {
		t.Fatalf("expire: %v", err)
	}
	resp = do("GET", "/api/v1/auth/cli/credentials", cred1.Token)
	body, _ = io.ReadAll(resp.Body)
	if resp.StatusCode != 401 || !strings.Contains(string(body), "credential_expired") {
		t.Fatalf("expired bearer: want 401 credential_expired, got %d — %s", resp.StatusCode, body)
	}

	// No bearer at all → generic 401 (the walk covers this per-op already).
	if resp := do("GET", "/api/v1/auth/cli/credentials", ""); resp.StatusCode != 401 {
		t.Fatalf("no auth: want 401, got %d", resp.StatusCode)
	}
}
