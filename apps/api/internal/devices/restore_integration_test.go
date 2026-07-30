//go:build enterprise

package devices

import (
	"context"
	"os"
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

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node)
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

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node)
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

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node)
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

	got, err := f.svc.RestoreCascadeRevokedDevices(f.ctx, f.org, f.node)
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
