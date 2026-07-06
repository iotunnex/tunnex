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
	"github.com/tunnexio/tunnex/apps/api/internal/ipalloc"
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

// TestAllocationSequentialAndReuse: allocation is deterministic lowest-free and
// respects existing allocations (no reassignment); a revoked device's address is
// reused (release-on-revocation).
func TestAllocationSequentialAndReuse(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)

	d1, _ := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "a"})
	d2, _ := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "b"})
	if *d1.Device.AssignedIp != "10.99.0.2" || *d2.Device.AssignedIp != "10.99.0.3" {
		t.Fatalf("want .2 then .3, got %v then %v", *d1.Device.AssignedIp, *d2.Device.AssignedIp)
	}
	// Revoke .2 -> its address is released and reused by the next allocation.
	if err := svc.Revoke(ctx, org, user, d1.Device.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	d3, _ := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "c"})
	if *d3.Device.AssignedIp != "10.99.0.2" {
		t.Fatalf("revoked address should be reused (want .2), got %v", *d3.Device.AssignedIp)
	}
}

// TestResizePoolShrinkRefusesOrphans: growing is fine; a shrink that would strand
// a live allocation is refused (never silently orphaned).
func TestResizePoolShrinkRefusesOrphans(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)

	// Seed a device whose address is in /24 but outside /25 (0-127).
	if _, err := tx.Exec(ctx, "INSERT INTO devices (org_id,user_id,node_id,name,public_key,assigned_ip) VALUES ($1,$2,$3,'d','k',$4)",
		org, user, node, "10.99.0.200"); err != nil {
		t.Fatalf("seed device: %v", err)
	}
	// Grow to /23 — safe (superset; .200 is inside and not a new reserved addr).
	if err := svc.ResizePool(ctx, user, org, "10.99.0.0/23"); err != nil {
		t.Fatalf("grow should succeed: %v", err)
	}
	// Shrink to /25 — would strand 10.99.0.200; must refuse with the typed orphan
	// error carrying the offender.
	var orphErr *ShrinkOrphansError
	if err := svc.ResizePool(ctx, user, org, "10.99.0.0/25"); !errors.As(err, &orphErr) {
		t.Fatalf("shrink should refuse with *ShrinkOrphansError, got %v", err)
	} else if len(orphErr.Orphans) != 1 || orphErr.Orphans[0].Addr != "10.99.0.200" || orphErr.Orphans[0].Reason != ipalloc.ReasonOutOfRange {
		t.Fatalf("orphans = %+v, want [{10.99.0.200 out_of_range}]", orphErr.Orphans)
	}
	// A bad CIDR is rejected.
	if err := svc.ResizePool(ctx, user, org, "not-a-cidr"); code(err) != "invalid_cidr" {
		t.Fatalf("bad cidr: want invalid_cidr, got %v", err)
	}
	// Idempotent: resizing to the current CIDR is a no-op success (200), not an error.
	if err := svc.ResizePool(ctx, user, org, "10.99.0.0/23"); err != nil {
		t.Fatalf("idempotent resize to current CIDR should succeed, got %v", err)
	}
	// Illegal shape: a disjoint /24 (neither superset nor subset) is refused.
	if err := svc.ResizePool(ctx, user, org, "10.88.0.0/24"); code(err) != "illegal_resize" {
		t.Fatalf("disjoint resize: want illegal_resize, got %v", err)
	}
	// Too small: a /31 can't hold the reserved addresses + a host.
	if err := svc.ResizePool(ctx, user, org, "10.99.0.0/31"); code(err) != "cidr_too_small" {
		t.Fatalf("tiny cidr: want cidr_too_small, got %v", err)
	}
}

// TestGetDeviceCrossOrgNotFound: a device id from another org reads as not-found
// (org-scoped read path — no cross-tenant leak).
func TestGetDeviceCrossOrgNotFound(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)
	res, err := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "d"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	otherOrg := uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,'O2',$2)", otherOrg, "s2-"+otherOrg.String()); err != nil {
		t.Fatalf("other org: %v", err)
	}
	if _, err := svc.Get(ctx, otherOrg, res.Device.ID); code(err) != "device_not_found" {
		t.Fatalf("cross-org Get: want device_not_found, got %v", err)
	}
}

// TestListDoesNotLeakCrossOrg: listing one org never returns another org's
// devices (the LEFT JOIN on device_status must not widen the org scope).
func TestListDoesNotLeakCrossOrg(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svcA, orgA, userA, nodeA := setup(t, tx, 10)
	if _, err := svcA.Create(ctx, CreateInput{OrgID: orgA, ActorID: userA, OwnerID: userA, NodeID: nodeA, Name: "a"}); err != nil {
		t.Fatalf("create A: %v", err)
	}
	// A separate org B with its own device.
	svcB, orgB, userB, nodeB := setup(t, tx, 10)
	if _, err := svcB.Create(ctx, CreateInput{OrgID: orgB, ActorID: userB, OwnerID: userB, NodeID: nodeB, Name: "b"}); err != nil {
		t.Fatalf("create B: %v", err)
	}
	// Org A's list (user + org views) must contain only A's device.
	if l, _ := svcA.ListForOrg(ctx, orgA); len(l) != 1 || l[0].Device.OrgID != orgA {
		t.Fatalf("ListForOrg leaked cross-org: %+v", l)
	}
	if l, _ := svcA.ListForUser(ctx, orgA, userA); len(l) != 1 || l[0].Device.OrgID != orgA {
		t.Fatalf("ListForUser leaked cross-org: %+v", l)
	}
}

// TestListSurfacesStatus: ListForUser joins and returns live status.
func TestListSurfacesStatus(t *testing.T) {
	ctx, tx := txOrSkip(t)
	svc, org, user, node := setup(t, tx, 10)
	res, _ := svc.Create(ctx, CreateInput{OrgID: org, ActorID: user, OwnerID: user, NodeID: node, Name: "d"})
	// Before any report: status fields are nil.
	list, _ := svc.ListForUser(ctx, org, user)
	if len(list) != 1 || list[0].LastHandshakeAt != nil || list[0].RxBytes != nil {
		t.Fatalf("pre-report status should be nil: %+v", list)
	}
	// Seed a status row and confirm the list surfaces it.
	if _, err := tx.Exec(ctx, "INSERT INTO device_status (device_id,last_handshake_at,rx_bytes,tx_bytes) VALUES ($1,now(),$2,$3)",
		res.Device.ID, int64(4096), int64(8192)); err != nil {
		t.Fatalf("seed status: %v", err)
	}
	list, _ = svc.ListForUser(ctx, org, user)
	if len(list) != 1 || list[0].LastHandshakeAt == nil || list[0].RxBytes == nil || *list[0].RxBytes != 4096 {
		t.Fatalf("list did not surface status: %+v", list[0])
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
