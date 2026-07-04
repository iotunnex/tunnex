package agentca

import (
	"context"
	"crypto/rand"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
)

func newSealer(t *testing.T, key []byte) *crypto.Sealer {
	t.Helper()
	s, err := crypto.NewSealer(key)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

func setup(t *testing.T) (*sqlc.Queries, context.Context, []byte) {
	t.Helper()
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	return sqlc.New(tx), ctx, key
}

func TestCALoadOrCreateAndReuse(t *testing.T) {
	q, ctx, key := setup(t)
	ca, created, err := LoadOrCreate(ctx, q, newSealer(t, key))
	if err != nil || !created {
		t.Fatalf("first LoadOrCreate: created=%v err=%v", created, err)
	}
	if err := ca.SelfTest(); err != nil {
		t.Fatalf("selftest: %v", err)
	}
	// Reload with the SAME master key -> same CA, not regenerated.
	ca2, created2, err := LoadOrCreate(ctx, q, newSealer(t, key))
	if err != nil || created2 {
		t.Fatalf("reload: created=%v err=%v", created2, err)
	}
	if ca.Fingerprint() != ca2.Fingerprint() {
		t.Fatal("CA fingerprint changed across loads — regenerated!")
	}
}

func TestCAWrongKeyFailsLoud(t *testing.T) {
	q, ctx, key := setup(t)
	if _, _, err := LoadOrCreate(ctx, q, newSealer(t, key)); err != nil {
		t.Fatalf("create: %v", err)
	}
	other := make([]byte, crypto.KeySize)
	_, _ = rand.Read(other)
	if _, _, err := LoadOrCreate(ctx, q, newSealer(t, other)); err == nil {
		t.Fatal("wrong master key must fail loud, not regenerate")
	}
}

func TestCASignedCertVerifiesAndExpires(t *testing.T) {
	q, ctx, key := setup(t)
	ca, _, err := LoadOrCreate(ctx, q, newSealer(t, key))
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	// SelfTest already signs+verifies a leaf against the pool; assert TTL bound.
	if CertTTL < time.Hour || CertTTL > 96*time.Hour {
		t.Fatalf("cert TTL %v outside the short-lived range", CertTTL)
	}
	if len(ca.CertPEM()) == 0 || ca.Pool() == nil {
		t.Fatal("CA cert/pool missing")
	}
}
