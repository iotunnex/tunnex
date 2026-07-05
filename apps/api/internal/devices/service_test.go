package devices

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/nodepush"
	"github.com/tunnexio/tunnex/apps/api/internal/wgkey"
)

// code returns the apierr code of err, or "" — for asserting typed failures.
func code(err error) string {
	var a *apierr.Error
	if err != nil && errors.As(err, &a) {
		return a.Code
	}
	return ""
}

// setup returns a device Service bound to a rolled-back tx, plus seeded org/user/
// node ids. maxDevices sets the org's per-user cap.
func setup(t *testing.T, tx pgx.Tx, maxDevices int) (*Service, uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	org, user, node := uuid.New(), uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO organizations (id,name,slug,max_devices_per_user) VALUES ($1,$2,$3,$4)",
		org, "O", "s-"+org.String(), maxDevices); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users (id,email,name,status) VALUES ($1,$2,$3,'active')",
		user, "u-"+user.String()+"@t", "U"); err != nil {
		t.Fatalf("user: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'member')", org, user); err != nil {
		t.Fatalf("membership: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO nodes (id,org_id,name,cert_serial,wg_public_key,endpoint) VALUES ($1,$2,$3,$4,$5,$6)",
		node, org, "gw", "serial-"+node.String(), "c2VydmVycHVia2V5MDAwMDAwMDAwMDAwMDAwMDAwMD0=", "gw.example.com:51820"); err != nil {
		t.Fatalf("node: %v", err)
	}
	return &Service{q: sqlc.New(tx), logger: slog.Default()}, org, user, node
}

func txOrSkip(t *testing.T) (context.Context, pgx.Tx) {
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
	return ctx, tx
}

func peerKeys(t *testing.T, svc *Service, node uuid.UUID) []string {
	t.Helper()
	rows, err := svc.q.ListActivePeersForNode(context.Background(), node)
	if err != nil {
		t.Fatalf("list peers: %v", err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.PublicKey)
	}
	return out
}

// TestServerGeneratedKeyNeverStored: the server-generated flow returns a valid
// private key ONCE, and the stored row holds only the public key (watch-item a).
func TestServerGeneratedKeyNeverStored(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)

	res, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "laptop"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !wgkey.Valid(res.Device.PublicKey) {
		t.Fatal("stored public key is not a valid WG key")
	}
	if res.PrivateKeyOneTime == "" || !wgkey.Valid(res.PrivateKeyOneTime) {
		t.Fatal("server-generated flow must return a valid one-time private key")
	}
	if res.PrivateKeyOneTime == res.Device.PublicKey {
		t.Fatal("private and public key must differ")
	}
	if res.Device.UserID != user {
		t.Fatal("device not bound to its owner")
	}
	// The server-generated flow returns a complete, ready-to-use config carrying
	// the one-time private key and the gateway endpoint (watch-items a + b).
	if !strings.Contains(res.Config, "PrivateKey = "+res.PrivateKeyOneTime) ||
		!strings.Contains(res.Config, "Endpoint = gw.example.com:51820") ||
		!strings.Contains(res.Config, "Address = "+*res.Device.AssignedIp+"/32") {
		t.Fatalf("config incomplete:\n%s", res.Config)
	}
}

// TestClientGeneratedKeyAccepted: a client-supplied public key is stored and no
// private key is ever returned.
func TestClientGeneratedKeyAccepted(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)

	_, pub, _ := wgkey.Generate()
	res, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "phone", PublicKey: pub})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if res.Device.PublicKey != pub {
		t.Fatal("client public key not stored verbatim")
	}
	if res.PrivateKeyOneTime != "" {
		t.Fatal("client-generated flow must NOT return a private key")
	}
}

// TestDevicesTableHasNoPrivateKeyColumn is the schema-level never-stored
// assertion: there is no column that could hold a peer private key.
func TestDevicesTableHasNoPrivateKeyColumn(t *testing.T) {
	ctx, tx := txOrSkip(t)
	rows, err := tx.Query(ctx, "SELECT column_name FROM information_schema.columns WHERE table_name='devices'")
	if err != nil {
		t.Fatalf("columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var col string
		if err := rows.Scan(&col); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(col), "private") || strings.Contains(strings.ToLower(col), "secret") {
			t.Fatalf("devices has a column that could store a private key: %q", col)
		}
	}
}

// TestPerUserDeviceLimit enforces the org cap.
func TestPerUserDeviceLimit(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 1)

	if _, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "d1"}); err != nil {
		t.Fatalf("first device: %v", err)
	}
	_, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "d2"})
	if code(err) != "device_limit" {
		t.Fatalf("want device_limit, got %v", err)
	}
}

// TestRevokeRemovesPeer: a revoked device drops from the node's peer set; a
// second revoke is a conflict, not a silent no-op.
func TestRevokeRemovesPeer(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)

	res, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "d"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := peerKeys(t, svc, node); len(got) != 1 || got[0] != res.Device.PublicKey {
		t.Fatalf("peer not present before revoke: %v", got)
	}
	if err := svc.Revoke(ctx, org, user, res.Device.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := peerKeys(t, svc, node); len(got) != 0 {
		t.Fatalf("peer still present after revoke: %v", got)
	}
	if code(svc.Revoke(ctx, org, user, res.Device.ID)) != "already_revoked" {
		t.Fatal("second revoke should conflict")
	}
}

// TestDeactivatedOwnerDropsPeers is the offboarding trace (watch-item c): a
// deactivated owner's peers leave every node's desired state; reactivation
// restores them (freeze, not delete).
func TestDeactivatedOwnerDropsPeers(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)
	q := svc.q

	if _, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "d"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(peerKeys(t, svc, node)) != 1 {
		t.Fatal("peer should be present for an active owner")
	}
	if err := q.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: user, Status: "deactivated"}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if got := peerKeys(t, svc, node); len(got) != 0 {
		t.Fatalf("deactivated owner's peer still in desired state: %v", got)
	}
	if err := q.SetUserStatus(ctx, sqlc.SetUserStatusParams{ID: user, Status: "active"}); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if len(peerKeys(t, svc, node)) != 1 {
		t.Fatal("reactivated owner's peer should return")
	}
}

// TestCrossOrgNodeAttachRejected: a device cannot attach to a node in another org.
func TestCrossOrgNodeAttachRejected(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, _, user, node := setup(t, tx, 10)
	otherOrg := uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", otherOrg, "O2", "s2-"+otherOrg.String()); err != nil {
		t.Fatalf("other org: %v", err)
	}
	// Make the owner a member of otherOrg so the create passes the membership
	// check and specifically exercises the cross-org NODE rejection.
	if _, err := tx.Exec(ctx, "INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'member')", otherOrg, user); err != nil {
		t.Fatalf("other membership: %v", err)
	}
	// node belongs to the first org; attaching it from otherOrg must fail.
	_, err := svc.Create(ctx, CreateInput{OrgID: otherOrg, ActorID: user, OwnerID: user, NodeID: node, Name: "d"})
	if code(err) != "node_not_found" {
		t.Fatalf("want node_not_found for cross-org node, got %v", err)
	}
}

// TestCreateRejectsNonMemberOwner: a device cannot be bound to a user who is not
// a member of the org (no cross-tenant / non-member owners, even for an admin).
func TestCreateRejectsNonMemberOwner(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, _, node := setup(t, tx, 10)
	stranger := uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO users (id,email,name,status) VALUES ($1,$2,$3,'active')",
		stranger, "x-"+stranger.String()+"@t", "X"); err != nil {
		t.Fatalf("stranger: %v", err)
	}
	// stranger exists globally but is NOT a member of org.
	_, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: stranger, OwnerID: stranger, NodeID: node, Name: "d"})
	if code(err) != "owner_not_member" {
		t.Fatalf("want owner_not_member for non-member owner, got %v", err)
	}
}

// TestCreatePushesGateway: creating a device notifies the gateway node's watcher.
func TestCreatePushesGateway(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)
	hub := nodepush.New()
	svc.hub = hub
	ch, unsub := hub.Subscribe(node)
	defer unsub()

	if _, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "d"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	select {
	case <-ch:
	default:
		t.Fatal("gateway was not pushed on device create")
	}
}
