package nodes

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/policyspec"
)

// stubHashProvider controls the pushed hash the CP sees for a node (and can force a compile
// fault). Distinct from policy_surface_test's fakeProvider so the desync inputs are direct.
type stubHashProvider struct {
	pushed string
	err    error
}

func (s stubHashProvider) CompiledForNode(context.Context, uuid.UUID, uuid.UUID) (*policyspec.Compiled, error) {
	return nil, s.err
}
func (s stubHashProvider) CompiledHashesForNodes(_ context.Context, _ uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	if s.err != nil {
		return nil, s.err
	}
	out := make(map[uuid.UUID]string, len(ids))
	for _, id := range ids {
		out[id] = s.pushed
	}
	return out, nil
}

func desyncPool(t *testing.T) *pgxpool.Pool {
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

func seedNode(t *testing.T, pool *pgxpool.Pool) sqlc.Node {
	t.Helper()
	ctx := context.Background()
	org, id := uuid.New(), uuid.New()
	exec := func(q string, a ...any) {
		if _, err := pool.Exec(ctx, q, a...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO organizations (id, name, slug) VALUES ($1,$2,$3)`, org, "dh", "dh-"+org.String()[:8])
	exec(`INSERT INTO nodes (id, org_id, name, cert_serial) VALUES ($1,$2,'gw',$3)`, id, org, "s-"+id.String())
	t.Cleanup(func() { _, _ = pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, org) })
	// trackDesync only reads node.ID + node.OrgID.
	return sqlc.Node{ID: id, OrgID: org}
}

func desyncSince(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) pgtype.Timestamptz {
	t.Helper()
	var ts pgtype.Timestamptz
	if err := pool.QueryRow(context.Background(), `SELECT policy_desync_since FROM nodes WHERE id=$1`, id).Scan(&ts); err != nil {
		t.Fatalf("read desync_since: %v", err)
	}
	return ts
}

// trackDesync is the SINGLE WRITER of policy_desync_since. Reds pin: stamp on term-3, clear on
// reconvergence/non-enforcing, idempotent onset per episode, and the open-build silence.
func TestTrackDesync(t *testing.T) {
	pool := desyncPool(t)
	ctx := context.Background()
	svc := func(pushed string) *Service { return &Service{pool: pool, q: sqlc.New(pool), policy: stubHashProvider{pushed: pushed}} }

	t.Run("stamp on enforcing mismatch, then idempotent onset", func(t *testing.T) {
		n := seedNode(t, pool)
		svc("new").trackDesync(ctx, n, "old") // pushed != applied
		first := desyncSince(t, pool, n.ID)
		if !first.Valid {
			t.Fatal("mismatch must STAMP policy_desync_since")
		}
		svc("new").trackDesync(ctx, n, "old") // still mismatched → onset PRESERVED (WHERE IS NULL)
		if again := desyncSince(t, pool, n.ID); !again.Valid || !again.Time.Equal(first.Time) {
			t.Fatalf("repeated mismatch must preserve the first onset: %v vs %v", again.Time, first.Time)
		}
	})

	t.Run("clear on reconvergence (applied == pushed)", func(t *testing.T) {
		n := seedNode(t, pool)
		svc("h").trackDesync(ctx, n, "old") // stamp
		svc("h").trackDesync(ctx, n, "h")   // applied caught up → CLEAR
		if desyncSince(t, pool, n.ID).Valid {
			t.Fatal("reconvergence must CLEAR the stamp")
		}
	})

	t.Run("clear on non-enforcing (pushed == '')", func(t *testing.T) {
		n := seedNode(t, pool)
		svc("h").trackDesync(ctx, n, "old") // stamp under enforcing
		svc("").trackDesync(ctx, n, "old")  // org went off/mesh → pushed "" → CLEAR
		if desyncSince(t, pool, n.ID).Valid {
			t.Fatal("non-enforcing must CLEAR the stamp")
		}
	})

	t.Run("revert-to-clear then re-push re-stamps a NEW onset (per-episode)", func(t *testing.T) {
		n := seedNode(t, pool)
		svc("new").trackDesync(ctx, n, "old") // episode 1: stamp
		t1 := desyncSince(t, pool, n.ID)
		svc("old").trackDesync(ctx, n, "old") // revert target back to applied → CLEAR
		svc("newer").trackDesync(ctx, n, "old") // episode 2: fresh mismatch → NEW onset
		t2 := desyncSince(t, pool, n.ID)
		if !t2.Valid || t2.Time.Before(t1.Time) {
			t.Fatalf("a new episode must stamp a NEW onset (not the cleared one): t1=%v t2=%v", t1.Time, t2.Time)
		}
	})

	t.Run("open build (nil policy) is SILENT — no write", func(t *testing.T) {
		n := seedNode(t, pool)
		open := &Service{pool: pool, q: sqlc.New(pool), policy: nil}
		open.trackDesync(ctx, n, "anything")
		if desyncSince(t, pool, n.ID).Valid {
			t.Fatal("open build must NOT write policy_desync_since")
		}
	})

	t.Run("compile fault (provider error) never stamps/clears", func(t *testing.T) {
		n := seedNode(t, pool)
		svc("new").trackDesync(ctx, n, "old") // stamp first
		faulted := &Service{pool: pool, q: sqlc.New(pool), policy: stubHashProvider{err: context.DeadlineExceeded}}
		faulted.trackDesync(ctx, n, "old") // can't-determine → leave the stamp UNTOUCHED
		if !desyncSince(t, pool, n.ID).Valid {
			t.Fatal("a compile fault must NOT clear an existing stamp")
		}
	})
}
