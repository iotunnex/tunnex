//go:build enterprise

package devices

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/internal/nodepush"
)

// restoreFixture is a committed org + owner + gateway plus helpers that mutate device rows directly, so each test
// can construct the exact revocation SHAPE it needs — including the pre-0059 NULL-cause shape that production code
// can no longer produce. A fixture that cannot express the failing case cannot catch it.
type restoreFixture struct {
	ctx              context.Context
	pool             *pgxpool.Pool
	svc              *Service
	org, owner, node uuid.UUID
}

func seedRestoreFixture(t *testing.T) *restoreFixture {
	t.Helper()
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)

	f := &restoreFixture{ctx: ctx, pool: pool, org: uuid.New(), owner: uuid.New(), node: uuid.New()}
	ex := func(sql string, args ...any) {
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %q: %v", sql, err)
		}
	}
	ex("INSERT INTO organizations (id,name,slug,pool_cidr,max_devices_per_user) VALUES ($1,'O',$2,'10.99.0.0/24',0)",
		f.org, "rest-"+f.org.String())
	ex("INSERT INTO users (id,email,name,status) VALUES ($1,$2,'U','active')", f.owner, f.owner.String()+"@t.local")
	ex("INSERT INTO memberships (org_id,user_id,role) VALUES ($1,$2,'owner')", f.org, f.owner)
	ex("INSERT INTO nodes (id,org_id,name,cert_serial,wg_public_key,endpoint) VALUES ($1,$2,'gw',$3,$4,'gw.example.com:51820')",
		f.node, f.org, "serial-"+f.node.String(), "c2VydmVycHVia2V5MDAwMDAwMDAwMDAwMDAwMDAwMD0=")
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DELETE FROM organizations WHERE id=$1", f.org) })

	f.svc = NewService(pool, nodepush.New(), nil)
	return f
}

func (f *restoreFixture) addDevice(t *testing.T, name, ip string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := f.pool.Exec(f.ctx,
		`INSERT INTO devices (id,org_id,user_id,node_id,name,platform,public_key,assigned_ip,status,transport)
		 VALUES ($1,$2,$3,$4,$5,'linux',$6,$7,'active','wireguard')`,
		id, f.org, f.owner, f.node, name, "pk-"+id.String(), ip); err != nil {
		t.Fatalf("addDevice %s: %v", name, err)
	}
	return id
}

// revokeDeliberately mirrors what RevokeDevice writes: cause='deliberate', address PRESERVED.
func (f *restoreFixture) revokeDeliberately(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		"UPDATE devices SET status='revoked', revoked_at=now(), revoked_cause='deliberate' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
}

// revokeGatewayCascade mirrors RevokeDevicesForNode: it sweeps whatever is still active/pending, exactly as a
// gateway revocation does, so a deliberately-revoked row is already out of range.
func (f *restoreFixture) revokeGatewayCascade(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		`UPDATE devices SET status='revoked', revoked_at=now(), revoked_cause='cascade'
		 WHERE node_id=$1 AND status IN ('active','pending') AND deleted_at IS NULL`, f.node); err != nil {
		t.Fatal(err)
	}
}

// revokeWithNoCause reproduces the PRE-0059 row shape, which production code can no longer write.
func (f *restoreFixture) revokeWithNoCause(t *testing.T, id uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx,
		"UPDATE devices SET status='revoked', revoked_at=now(), revoked_cause=NULL WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
}

func (f *restoreFixture) statusOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var s string
	if err := f.pool.QueryRow(f.ctx, "SELECT status FROM devices WHERE id=$1", id).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestRestoreNeverRevivesADeliberatelyRevokedDevice — THE load-bearing property of D5, and the reason the cause
// column exists at all.
//
// Wall 6 could not be closed without it: un-revoking the whole cascade set would have resurrected a device an
// operator deliberately killed (a lost laptop), and refusing to un-revoke anything left every user of a rebuilt
// gateway needing a re-issued one-time config. "Its gateway went away" and "the user lost the laptop" rendered
// IDENTICALLY before this column, so the two outcomes were indistinguishable to any code that tried.
//
// A lost laptop coming back online because someone rebuilt a gateway is a security failure, not an inconvenience.
func TestRestoreNeverRevivesADeliberatelyRevokedDevice(t *testing.T) {
	f := seedRestoreFixture(t)

	// Two devices on the same gateway: one revoked BY THE CASCADE, one revoked ON PURPOSE.
	cascade := f.addDevice(t, "laptop-cascade", "10.99.0.10")
	deliberate := f.addDevice(t, "laptop-lost", "10.99.0.11")

	f.revokeDeliberately(t, deliberate)
	f.revokeGatewayCascade(t) // sweeps whatever is still active — i.e. only `cascade`

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node, f.node, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	restored := map[uuid.UUID]bool{}
	for _, r := range got {
		restored[r.DeviceID] = true
	}
	if !restored[cascade] {
		t.Error("a cascade-revoked device MUST be restored — leaving it revoked is Wall 6: a recovered gateway with " +
			"zero users, each needing a re-issued one-time config")
	}
	if restored[deliberate] {
		t.Fatal("a DELIBERATELY revoked device must NEVER be revived by a gateway coming back. An operator decided " +
			"this device must stop; a gateway rebuild is not a decision about it, and reviving a lost laptop " +
			"because someone rebuilt a gateway is a security failure")
	}
	if s := f.statusOf(t, deliberate); s != "revoked" {
		t.Errorf("the deliberately revoked device must still be revoked, got %q", s)
	}
}

// TestRestoreReclaimsTheOriginalAddressWhenFree — the ruled fork's common case, which must cost users nothing.
//
// A gateway rebuilt within minutes with nothing else having taken the address: the user's existing WireGuard config
// keeps working, because the interface address it embeds is still theirs. Unconditionally allocating fresh would
// impose a fleet-wide re-import for a contention that usually did not happen.
func TestRestoreReclaimsTheOriginalAddressWhenFree(t *testing.T) {
	f := seedRestoreFixture(t)
	dev := f.addDevice(t, "laptop", "10.99.0.20")
	f.revokeGatewayCascade(t)

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node, f.node, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 restored, got %d", len(got))
	}
	if !got[0].KeptAddress || got[0].NewIP != "10.99.0.20" {
		t.Errorf("the original address must be reclaimed when nothing took it (kept=%v ip=%q) — the common case is a "+
			"gateway rebuilt within minutes, and that case must not invalidate every user's config",
			got[0].KeptAddress, got[0].NewIP)
	}
	if f.statusOf(t, dev) != "active" {
		t.Error("the restored device must be active")
	}
}

// TestRestoreAllocatesFreshWhenTheAddressWasTaken — the fallback, and the case that must be visibly different.
//
// The address may be genuinely gone: reallocated to a live device now using it. Restore cannot take it back, so the
// device returns on a NEW address — which means its exported profile embeds the wrong one and will not connect until
// re-imported. That is why this case is reported distinctly rather than silently.
func TestRestoreAllocatesFreshWhenTheAddressWasTaken(t *testing.T) {
	f := seedRestoreFixture(t)
	f.addDevice(t, "laptop", "10.99.0.30")
	f.revokeGatewayCascade(t)
	// Someone else takes the freed address while the gateway is down — which is legitimate: the moment status left
	// ('active','pending') the pool considered it free, and both the unique index and the allocation oracle agree.
	squatter := f.addDevice(t, "someone-else", "10.99.0.30")

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node, f.node, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 restored, got %d", len(got))
	}
	if got[0].KeptAddress {
		t.Fatal("restore must NOT reclaim an address a live device now holds — that would hand one address to two " +
			"devices and the pool's own oracle says it is taken")
	}
	if got[0].NewIP == "10.99.0.30" {
		t.Fatalf("the fresh address must differ from the held one, got %q", got[0].NewIP)
	}
	if got[0].OldIP != "10.99.0.30" {
		t.Errorf("the previous address must be reported so the change is auditable, got %q", got[0].OldIP)
	}
	if f.statusOf(t, squatter) != "active" {
		t.Error("the device that legitimately took the address must be untouched")
	}
}

// TestRestoreSkipsRowsWithNoRecordedCause — rows revoked before 0059 carry NULL, which is honestly unknown.
// Reviving one would be exactly the deliberate-revocation risk the column exists to avoid.
func TestRestoreSkipsRowsWithNoRecordedCause(t *testing.T) {
	f := seedRestoreFixture(t)
	legacy := f.addDevice(t, "legacy", "10.99.0.40")
	f.revokeWithNoCause(t, legacy) // pre-0059 shape: revoked, cause NULL

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node, f.node, nil)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	for _, r := range got {
		if r.DeviceID == legacy {
			t.Fatal("a device revoked before the cause column existed must NOT be restored: nobody recorded why, " +
				"and 'I cannot tell' must never resolve to 'it is safe to revive'")
		}
	}
}

// addNode seeds a second gateway — the REPLACEMENT an operator restores onto.
func (f *restoreFixture) addNode(t *testing.T, name string, revoked bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	status, rev := "active", "NULL"
	if revoked {
		status, rev = "revoked", "now()"
	}
	if _, err := f.pool.Exec(f.ctx, `
		INSERT INTO nodes (id,org_id,name,cert_serial,wg_public_key,endpoint,status,revoked_at)
		VALUES ($1,$2,$3,$4,$5,'gw.example.com:51820',$6,`+rev+`)`,
		id, f.org, name, "serial-"+id.String(), "c2VydmVycHVia2V5MDAwMDAwMDAwMDAwMDAwMDAwMD0=", status); err != nil {
		t.Fatalf("addNode %s: %v", name, err)
	}
	return id
}

func (f *restoreFixture) nodeOf(t *testing.T, deviceID uuid.UUID) uuid.UUID {
	t.Helper()
	var n uuid.UUID
	if err := f.pool.QueryRow(f.ctx, "SELECT node_id FROM devices WHERE id=$1", deviceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestOperatorRestoreIsREACHABLE — the Slice 7 red, and it is written to prove REACHABILITY, not behaviour.
//
// THE DEFECT IT CLOSES. Every earlier restore red called RestoreCascadeRevokedDevices directly and passed. They
// proved the restore does the right thing WHEN CALLED. Nothing proved it is ever called — and it was not:
// devices are cascade-revoked only by nodes.Revoke, and re-key REFUSES a revoked node (D3), so the only trigger
// that created the work put the node into the one state that could never reach the code which undoes it.
//
// So this red drives the OPERATOR'S ENTRY POINT — the same function the HTTP handler calls, with the same
// arguments a request produces — and asserts devices come back THROUGH it. A test that called the inner function
// would re-commit the original error with more code around it.
func TestOperatorRestoreIsREACHABLE(t *testing.T) {
	f := seedRestoreFixture(t)
	actor := f.owner
	replacement := f.addNode(t, "gw-replacement-"+uuid.NewString()[:8], false)

	keeps := f.addDevice(t, "keeps-address", "10.99.0.11")
	f.revokeGatewayCascade(t) // the gateway was revoked; every device homed on it went with it

	res, err := f.svc.RestoreCascadedDevicesByOperator(t.Context(), actor, f.org, f.node, replacement)
	if err != nil {
		t.Fatalf("the operator entry point must restore: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 device restored through the operator trigger, got %d", len(res))
	}
	if got := f.statusOf(t, keeps); got != "active" {
		t.Fatalf("the device must be active again; got %q", got)
	}

	// RE-HOMED, and this is why the trigger takes a target at all: the source gateway is revoked and never
	// coming back, so restoring onto it would produce an `active` device pointing at something that will never
	// serve it — a row that reads healthy on every surface and works nowhere.
	if got := f.nodeOf(t, keeps); got != replacement {
		t.Fatalf("the restored device must be homed to the REPLACEMENT gateway; got %v want %v", got, replacement)
	}

	// The act is on the record, with the HUMAN who did it — the authorization story of Slice 7 in one row.
	var actorID uuid.UUID
	var meta []byte
	if err := f.pool.QueryRow(f.ctx,
		`SELECT actor_user_id, metadata FROM audit_logs WHERE org_id=$1 AND action='node.devices_restored'
		 ORDER BY created_at DESC LIMIT 1`, f.org).Scan(&actorID, &meta); err != nil {
		t.Fatalf("the operator restore must be audited: %v", err)
	}
	if actorID != actor {
		t.Fatalf("the audit row must name the human who asked; got %v", actorID)
	}
	if !strings.Contains(string(meta), replacement.String()) {
		t.Fatalf("the audit must record which gateway the devices were restored onto; got %s", meta)
	}
}

// TestOperatorRestoreRefusesADEADTarget — the obvious operator mistake, refused.
//
// The natural thing to type is the gateway you just revoked. That node can never serve these devices again, so
// accepting it would hand back rows that are `active` and unreachable — worse than leaving them revoked, because
// revoked is honest and this is not.
func TestOperatorRestoreRefusesADEADTarget(t *testing.T) {
	f := seedRestoreFixture(t)
	dead := f.addNode(t, "gw-dead-"+uuid.NewString()[:8], true)
	d := f.addDevice(t, "stays-dead", "10.99.0.21")
	f.revokeGatewayCascade(t)

	if _, err := f.svc.RestoreCascadedDevicesByOperator(t.Context(), f.owner, f.org, f.node, dead); !errors.Is(err, ErrRestoreTargetUnusable) {
		t.Fatalf("a REVOKED target must be refused; got %v", err)
	}
	if got := f.statusOf(t, d); got != "revoked" {
		t.Fatalf("a refused restore must change nothing; device is %q", got)
	}

	// A target in ANOTHER org is the same refusal — the source is org-scoped and so is the target, or an admin
	// could rehome another tenant's devices onto their own gateway.
	if _, err := f.svc.RestoreCascadedDevicesByOperator(t.Context(), f.owner, f.org, f.node, uuid.New()); !errors.Is(err, ErrRestoreTargetUnusable) {
		t.Fatalf("an unknown/foreign target must be refused; got %v", err)
	}
	if _, err := f.svc.RestoreCascadedDevicesByOperator(t.Context(), f.owner, f.org, uuid.New(), f.addNode(t, "gw-ok-"+uuid.NewString()[:8], false)); !errors.Is(err, ErrRestoreSourceUnknown) {
		t.Fatalf("an unknown source must be refused distinctly; got %v", err)
	}
}

// TestOperatorRestoreIsAuditedEVENWhenNothingComesBack — the swallowed-audit law, one step earlier.
//
// "An admin restored a gateway's devices and nothing came back" is exactly the event someone needs to find later.
// An audit that only fires when work happened cannot answer "did anyone try?".
func TestOperatorRestoreIsAuditedEVENWhenNothingComesBack(t *testing.T) {
	f := seedRestoreFixture(t)
	target := f.addNode(t, "gw-empty-"+uuid.NewString()[:8], false)

	res, err := f.svc.RestoreCascadedDevicesByOperator(t.Context(), f.owner, f.org, f.node, target)
	if err != nil {
		t.Fatalf("restoring nothing is a normal answer, not an error: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("no candidates existed; got %d", len(res))
	}
	var n int
	if err := f.pool.QueryRow(f.ctx,
		`SELECT count(*) FROM audit_logs WHERE org_id=$1 AND action='node.devices_restored'`, f.org).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the attempt must be audited even with zero candidates; got %d rows", n)
	}
}
