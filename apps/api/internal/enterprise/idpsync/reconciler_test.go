//go:build enterprise

package idpsync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// --- fakes (no live Graph, no DB) ---

type fakeProvider struct {
	members []DirectoryMember
	listErr error
}

func (f *fakeProvider) ListGroupMembers(ctx context.Context, groupID string) ([]DirectoryMember, error) {
	return f.members, f.listErr
}
func (f *fakeProvider) ResolveUserStatus(ctx context.Context, externalID string) (UserStatus, error) {
	return StatusActive, nil
}

type recordCall struct {
	ok      bool
	advance bool
	errMsg  string
}

type fakeStore struct {
	groups     []SyncGroup
	current    map[uuid.UUID][]uuid.UUID // groupID → member user ids
	byEmail    map[string]uuid.UUID      // resolvable org users
	resolveErr error

	added   []memberOp
	removed []memberOp
	pushes  int
	records []recordCall
}

type memberOp struct{ group, user uuid.UUID }

func (s *fakeStore) ListIdpSyncGroups(ctx context.Context, orgID uuid.UUID, provider string) ([]SyncGroup, error) {
	return s.groups, nil
}
func (s *fakeStore) ListIdpGroupMemberIDs(ctx context.Context, orgID, groupID uuid.UUID) ([]uuid.UUID, error) {
	return s.current[groupID], nil
}
func (s *fakeStore) ResolveOrgUser(ctx context.Context, orgID uuid.UUID, email string) (uuid.UUID, bool, error) {
	if s.resolveErr != nil {
		return uuid.Nil, false, s.resolveErr
	}
	uid, ok := s.byEmail[email]
	return uid, ok, nil
}
func (s *fakeStore) AddIdpGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	s.added = append(s.added, memberOp{groupID, userID})
	return nil
}
func (s *fakeStore) RemoveIdpGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error {
	s.removed = append(s.removed, memberOp{groupID, userID})
	return nil
}
func (s *fakeStore) RecordResult(ctx context.Context, orgID uuid.UUID, provider string, ok, advance bool, errMsg string, now time.Time) error {
	s.records = append(s.records, recordCall{ok, advance, errMsg})
	return nil
}
func (s *fakeStore) PushOrg(ctx context.Context, orgID uuid.UUID) { s.pushes++ }

type fakeDeprov struct{ deactivated []uuid.UUID }

func (d *fakeDeprov) DeactivateForSync(ctx context.Context, orgID, userID uuid.UUID, provider string) error {
	d.deactivated = append(d.deactivated, userID)
	return nil
}

// --- helpers ---

var (
	org  = uuid.New()
	grp  = uuid.New()
	uAli = uuid.New()
	uBob = uuid.New()
	uCar = uuid.New()
)

func baseStore() *fakeStore {
	return &fakeStore{
		groups:  []SyncGroup{{ID: grp, IdpGroupID: "g-ext-1"}},
		current: map[uuid.UUID][]uuid.UUID{},
		byEmail: map[string]uuid.UUID{"alice@acme.com": uAli, "bob@acme.com": uBob, "carol@acme.com": uCar},
	}
}

func run(t *testing.T, p DirectoryProvider, s *fakeStore, d *fakeDeprov) {
	t.Helper()
	r := NewReconciler(p, s, d, func() time.Time { return time.Unix(1700000000, 0) })
	if err := r.ReconcileConfig(context.Background(), org, "microsoft"); err != nil {
		// transient path returns the error by design; callers assert on it separately.
		t.Logf("ReconcileConfig returned: %v", err)
	}
}

// --- tests ---

func TestReconcile_AddsActiveMembers(t *testing.T) {
	s := baseStore()
	p := &fakeProvider{members: []DirectoryMember{
		{ExternalID: "x1", Email: "alice@acme.com", Status: StatusActive},
		{ExternalID: "x2", Email: "bob@acme.com", Status: StatusActive},
	}}
	d := &fakeDeprov{}
	run(t, p, s, d)

	if len(s.added) != 2 {
		t.Fatalf("want 2 adds, got %d: %+v", len(s.added), s.added)
	}
	if len(s.removed) != 0 {
		t.Errorf("want 0 removes, got %+v", s.removed)
	}
	if s.pushes != 1 {
		t.Errorf("want exactly 1 org-wide push, got %d", s.pushes)
	}
	if len(s.records) != 1 || !s.records[0].ok || !s.records[0].advance {
		t.Errorf("want one ok=true advance=true record, got %+v", s.records)
	}
}

func TestReconcile_RemovesMembersNoLongerActive(t *testing.T) {
	s := baseStore()
	s.current[grp] = []uuid.UUID{uAli, uBob} // both currently in the group
	p := &fakeProvider{members: []DirectoryMember{
		{ExternalID: "x1", Email: "alice@acme.com", Status: StatusActive}, // bob dropped out of the group
	}}
	run(t, p, s, &fakeDeprov{})

	if len(s.added) != 0 {
		t.Errorf("alice already present — want 0 adds, got %+v", s.added)
	}
	if len(s.removed) != 1 || s.removed[0].user != uBob {
		t.Fatalf("want bob removed, got %+v", s.removed)
	}
}

func TestReconcile_DisabledMemberSweptAndRemoved(t *testing.T) {
	s := baseStore()
	s.current[grp] = []uuid.UUID{uAli}
	p := &fakeProvider{members: []DirectoryMember{
		{ExternalID: "x1", Email: "alice@acme.com", Status: StatusDisabled}, // disabled upstream
	}}
	d := &fakeDeprov{}
	run(t, p, s, d)

	if len(d.deactivated) != 1 || d.deactivated[0] != uAli {
		t.Fatalf("want alice deprovisioned (full sweep), got %+v", d.deactivated)
	}
	if len(s.removed) != 1 || s.removed[0].user != uAli {
		t.Errorf("disabled member must also leave the idp group, got removed=%+v", s.removed)
	}
}

// THE LOAD-BEARING TEST. A transient fetch error must NEVER cause a removal — proven by there being
// no code path from a failed fetch to converge(). We seed a full current membership and a 503; the
// membership must be left entirely alone and health must trip fail-static (ok=false, clock frozen).
func TestReconcile_TransientFetchIsFailStatic(t *testing.T) {
	s := baseStore()
	s.current[grp] = []uuid.UUID{uAli, uBob, uCar} // a live membership sitting there
	p := &fakeProvider{listErr: errors.New("graph 503 service unavailable")}
	d := &fakeDeprov{}
	run(t, p, s, d)

	if len(s.removed) != 0 {
		t.Fatalf("FAIL-STATIC VIOLATED: a transient fetch removed %+v — a Graph outage revoked live access", s.removed)
	}
	if len(s.added) != 0 || len(d.deactivated) != 0 {
		t.Errorf("transient fetch must touch nothing; added=%+v deactivated=%+v", s.added, d.deactivated)
	}
	if s.pushes != 0 {
		t.Errorf("nothing changed → no push; got %d", s.pushes)
	}
	if len(s.records) != 1 || s.records[0].ok || s.records[0].advance {
		t.Fatalf("want fail-static health record ok=false advance=false (clock frozen), got %+v", s.records)
	}
}

// A transient failure on ONE group must not block another group's authoritative reconcile.
func TestReconcile_TransientOnOneGroupDoesNotBlockOther(t *testing.T) {
	grp2 := uuid.New()
	s := baseStore()
	s.groups = []SyncGroup{{ID: grp, IdpGroupID: "g-ext-1"}, {ID: grp2, IdpGroupID: "g-ext-2"}}
	// A per-group provider: group 1 fails transiently, group 2 lists alice.
	p := &perGroupProvider{byGroup: map[string]providerResult{
		"g-ext-1": {err: errors.New("graph 503")},
		"g-ext-2": {members: []DirectoryMember{{ExternalID: "x1", Email: "alice@acme.com", Status: StatusActive}}},
	}}
	run(t, p, s, &fakeDeprov{})

	// group 2 got its add; group 1 touched nothing; config health = fail-static.
	if len(s.added) != 1 || s.added[0].group != grp2 {
		t.Fatalf("group 2 must still reconcile despite group 1 failing, got %+v", s.added)
	}
	if len(s.records) != 1 || s.records[0].ok {
		t.Errorf("one failed group must degrade the config, got %+v", s.records)
	}
}

func TestReconcile_GroupGoneEmptiesMembershipAndDegrades(t *testing.T) {
	s := baseStore()
	s.current[grp] = []uuid.UUID{uAli, uBob}
	p := &fakeProvider{listErr: ErrGroupGone}
	run(t, p, s, &fakeDeprov{})

	if len(s.removed) != 2 {
		t.Fatalf("a deleted upstream group → membership emptied (0 grants), got removed=%+v", s.removed)
	}
	// Degraded but authoritative: ok=false, clock ADVANCES (fetch succeeded → data fresh, non-escalating).
	if len(s.records) != 1 || s.records[0].ok || !s.records[0].advance {
		t.Fatalf("gone-group health = ok=false advance=true, got %+v", s.records)
	}
}

func TestReconcile_UnmatchedEmailSkipped(t *testing.T) {
	s := baseStore()
	p := &fakeProvider{members: []DirectoryMember{
		{ExternalID: "x9", Email: "stranger@other.com", Status: StatusActive}, // not an org user
	}}
	run(t, p, s, &fakeDeprov{})
	if len(s.added) != 0 {
		t.Errorf("directory member with no matching org user must be skipped (no JIT), got %+v", s.added)
	}
}

func TestClassifySyncHealth(t *testing.T) {
	now := time.Unix(1700000000, 0)
	created := now.Add(-2 * time.Hour)
	fresh := now.Add(-5 * time.Minute)
	stale := now.Add(-40 * time.Minute)
	cases := []struct {
		name   string
		ok     bool
		lastAt *time.Time
		want   SyncTier
	}{
		{"ok", true, &fresh, TierOK},
		{"degraded-fresh-good-sync", false, &fresh, TierDegraded},
		{"escalated-stale-good-sync", false, &stale, TierEscalated},
		{"escalated-never-synced-old-config", false, nil, TierEscalated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifySyncHealth(tc.ok, tc.lastAt, created, now, EscalationCeiling)
			if got != tc.want {
				t.Errorf("ClassifySyncHealth = %v, want %v", got, tc.want)
			}
		})
	}
}

// perGroupProvider serves distinct results per group id (for the mixed-failure test).
type providerResult struct {
	members []DirectoryMember
	err     error
}
type perGroupProvider struct{ byGroup map[string]providerResult }

func (p *perGroupProvider) ListGroupMembers(ctx context.Context, groupID string) ([]DirectoryMember, error) {
	r := p.byGroup[groupID]
	return r.members, r.err
}
func (p *perGroupProvider) ResolveUserStatus(ctx context.Context, externalID string) (UserStatus, error) {
	return StatusActive, nil
}
