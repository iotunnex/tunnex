package machineauth

import (
	"context"
	"crypto/sha256"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func testSealer(t *testing.T) *crypto.Sealer {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	s, err := crypto.NewSealer(key)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

// TestMintUseRevoke — S10.2 Slice 1: the machine-credential lifecycle end to end. Mint returns a `tnxm_`
// token ONCE with a keyed fingerprint; the token HASH resolves the active row (the "use" the auth path
// performs), scoped to the org with the fixed 'operator' role; the mint is audited to the HUMAN owner
// (fingerprint only, never the token); Revoke severs (RevokedAt set → the auth path returns nil → denied)
// and drops it from List; a second revoke is a no-op (no leak).
func TestMintUseRevoke(t *testing.T) {
	pool := testPool(t)
	svc := NewService(pool, testSealer(t))
	ctx := context.Background()

	org, owner := uuid.New(), uuid.New()
	ex := func(sql string, args ...any) {
		if _, e := pool.Exec(ctx, sql, args...); e != nil {
			t.Fatalf("seed %q: %v", sql, e)
		}
	}
	ex(`INSERT INTO organizations (id, name, slug, pool_cidr) VALUES ($1,'M',$2,'10.99.0.0/24')`, org, "m-"+org.String()[:8])
	ex(`INSERT INTO users (id, email) VALUES ($1,$2)`, owner, "m-"+owner.String()[:8]+"@ex.com")

	// MINT — one-time token, keyed fingerprint.
	cred, err := svc.Mint(ctx, org, owner, "gitops")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !strings.HasPrefix(cred.Token, TokenPrefix) {
		t.Fatalf("token must be %s-prefixed, got %q", TokenPrefix, cred.Token)
	}
	if cred.Fingerprint == "" {
		t.Fatal("fingerprint must be set")
	}

	q := sqlc.New(pool)
	h := sha256.Sum256([]byte(cred.Token))

	// USE — the token hash resolves the active row (what MachineAuth does), org-scoped, role=operator.
	row, err := q.GetMachineCredentialByHash(ctx, h[:])
	if err != nil {
		t.Fatalf("the mint token must resolve by hash: %v", err)
	}
	if row.RevokedAt.Valid {
		t.Fatal("a freshly-minted credential must not be revoked")
	}
	if row.Role != "operator" {
		t.Fatalf("the machine credential must hold the fixed 'operator' role, got %q", row.Role)
	}
	if row.OrgID != org {
		t.Fatal("the credential must be scoped to its org")
	}

	// The mint is audited to the human owner (org-scoped, fingerprint only — never the token).
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action='machine.credential_issued'
		   AND actor_user_id=$2 AND metadata->>'fingerprint'=$3`, org, owner, cred.Fingerprint).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("mint must audit machine.credential_issued (owner, fingerprint), got %d", n)
	}
	if list, _ := svc.List(ctx, org); len(list) != 1 {
		t.Fatalf("List must show the 1 active credential, got %d", len(list))
	}

	// REVOKE — severs (the auth path re-reads and returns nil on RevokedAt), and drops from List.
	ok, err := svc.Revoke(ctx, org, owner, cred.ID)
	if err != nil || !ok {
		t.Fatalf("revoke: ok=%v err=%v", ok, err)
	}
	row2, err := q.GetMachineCredentialByHash(ctx, h[:])
	if err != nil {
		t.Fatal(err)
	}
	if !row2.RevokedAt.Valid {
		t.Fatal("a revoked credential must have RevokedAt set (severs on its next request)")
	}
	if list, _ := svc.List(ctx, org); len(list) != 0 {
		t.Fatalf("a revoked credential must be excluded from List, got %d", len(list))
	}

	// Idempotent — a second revoke is a no-op (no leak of whether the id existed).
	if ok2, _ := svc.Revoke(ctx, org, owner, cred.ID); ok2 {
		t.Fatal("re-revoking must be a no-op (0 rows)")
	}
}
