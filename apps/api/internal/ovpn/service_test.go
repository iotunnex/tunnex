package ovpn

import (
	"context"
	"crypto/rand"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
	"github.com/tunnexio/tunnex/apps/api/internal/ovpnca"
)

// setup opens a rolled-back tx against the test DB (skips when unset), plus a CA loaded through the
// real LoadOrCreate path — so this test also covers the DB storage round-trip (D-S9.1-1).
func setup(t *testing.T) (*Service, context.Context, uuid.UUID, uuid.UUID) {
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
	q := sqlc.New(tx)

	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	sealer, err := crypto.NewSealer(key)
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	ca, created, err := ovpnca.LoadOrCreate(ctx, q, sealer)
	if err != nil || !created {
		t.Fatalf("LoadOrCreate: created=%v err=%v", created, err)
	}

	// Minimal fixture: an org, a user, a node, a device to bind the cert to (FKs are enforced).
	orgID := mustOrg(t, ctx, q)
	userID := mustUser(t, ctx, q, orgID)
	nodeID := mustNode(t, ctx, q, orgID)
	deviceID := mustDevice(t, ctx, q, orgID, userID, nodeID)
	return NewService(q, ca), ctx, orgID, deviceID
}

// TestIssueRecordsSerialNotKey is the B2 + D-S9.2-1 red: Issue persists the cert IDENTITY (serial,
// expiry, device binding) so the Slice 5 CRL sweep has its source — and the row carries NO private
// key column at all (the key is ephemeral, returned to the caller only).
func TestIssueRecordsSerialNotKey(t *testing.T) {
	svc, ctx, orgID, deviceID := setup(t)

	p, err := svc.Issue(ctx, orgID, deviceID, "device-"+deviceID.String())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if p.PrivateKeyPEM == "" {
		t.Fatal("caller must receive the ephemeral private key for one-time delivery")
	}

	// The recorded cert identity is findable by the CRL source read, with the returned serial.
	active, err := svc.q.ListActiveOVPNClientCertsByOrg(ctx, orgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("want 1 recorded cert, got %d", len(active))
	}
	row := active[0]
	if row.Serial != p.Serial {
		t.Fatalf("recorded serial %q != issued %q", row.Serial, p.Serial)
	}
	if row.DeviceID != deviceID {
		t.Fatalf("recorded device %v != %v", row.DeviceID, deviceID)
	}
	if row.RevokedAt.Valid {
		t.Fatal("a freshly issued cert must not be revoked")
	}
	if !row.NotAfter.After(row.IssuedAt) {
		t.Fatal("not_after must be after issued_at (long-lived leaf)")
	}
}

// --- minimal FK fixtures (kept local so the test is self-contained) ---

func mustOrg(t *testing.T, ctx context.Context, q *sqlc.Queries) uuid.UUID {
	t.Helper()
	o, err := q.CreateOrganization(ctx, sqlc.CreateOrganizationParams{Name: "ovpn-test", Slug: "ovpn-" + uuid.NewString()[:8]})
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	return o.ID
}

func mustUser(t *testing.T, ctx context.Context, q *sqlc.Queries, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	u, err := q.CreateUser(ctx, sqlc.CreateUserParams{Email: uuid.NewString()[:8] + "@ovpn.test", Name: "t"})
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	return u.ID
}

func mustNode(t *testing.T, ctx context.Context, q *sqlc.Queries, orgID uuid.UUID) uuid.UUID {
	t.Helper()
	n, err := q.CreateNode(ctx, sqlc.CreateNodeParams{OrgID: orgID, Name: "gw", CertSerial: uuid.NewString(), AgentVersion: "test"})
	if err != nil {
		t.Fatalf("node: %v", err)
	}
	return n.ID
}

func mustDevice(t *testing.T, ctx context.Context, q *sqlc.Queries, orgID, userID, nodeID uuid.UUID) uuid.UUID {
	t.Helper()
	d, err := q.CreateDevice(ctx, sqlc.CreateDeviceParams{
		OrgID: orgID, UserID: userID, NodeID: nodeID, Name: "ovpn-dev", PublicKey: "k", Status: "active",
		Transport: "openvpn",
	})
	if err != nil {
		t.Fatalf("device: %v", err)
	}
	return d.ID
}

// TestExportProfileAssemblesAndFingerprints (S9.1 Slice 4b-wiring) locks the export orchestration:
// ExportProfile issues + records the cert, assembles an importable .ovpn, and returns the SERIAL as
// the fingerprint — the keyed identity the caller audits, never the material.
func TestExportProfileAssemblesAndFingerprints(t *testing.T) {
	svc, ctx, orgID, deviceID := setup(t)
	profile, fingerprint, err := svc.ExportProfile(ctx, orgID, deviceID, "gw.example.com", 1194)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	// the fingerprint is the RECORDED cert serial (the audit's keyed identity).
	active, err := svc.q.ListActiveOVPNClientCertsByOrg(ctx, orgID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(active) != 1 || active[0].Serial != fingerprint {
		t.Fatalf("fingerprint must be the recorded cert serial; got %q, rows=%d", fingerprint, len(active))
	}
	// importable profile: client directives + remote + inline material.
	for _, want := range []string{"client\n", "remote gw.example.com 1194\n", "remote-cert-tls server\n", "<ca>\n", "<cert>\n", "<key>\n"} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q; got:\n%s", want, profile)
		}
	}
	// the fingerprint is NEVER the material.
	if strings.Contains(fingerprint, "PRIVATE KEY") || strings.Contains(fingerprint, "BEGIN") {
		t.Fatalf("fingerprint must be the serial, never the material; got %q", fingerprint)
	}
}
