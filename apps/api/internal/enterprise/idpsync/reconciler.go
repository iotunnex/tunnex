//go:build enterprise

package idpsync

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Reconciler drives group_members(origin='idp_sync') to match a DirectoryProvider's authoritative
// view (S7.5.2 slice 3). It is provider-agnostic (talks only to DirectoryProvider) and
// storage-agnostic (talks only to Store + Deprovisioner) — the slice-4 adapter wires the concrete
// sqlc + policy push + DeactivateMember sweep behind these seams; the unit tests wire fakes.
//
// THE LOAD-BEARING GUARANTEE (D2 fail-static): a removal is only ever computed from a
// SUCCESSFULLY-FETCHED member set. This is structural, not a runtime check — reconcileGroup returns
// on a transient fetch error BEFORE converge (the only code that computes removals) is called, so a
// failed fetch has no code path that reaches a delete. A Graph outage can therefore never revoke a
// live grant; it can only trip the health alarm and leave last-known membership standing.
type Reconciler struct {
	provider DirectoryProvider
	store    Store
	deprov   Deprovisioner
	now      func() time.Time
}

// SyncGroup is one IdP→Tunnex group mapping to reconcile.
type SyncGroup struct {
	ID         uuid.UUID // the Tunnex user_groups row
	IdpGroupID string    // the external directory group id
}

// Store is the persistence surface the reconciler needs. The slice-4 adapter implements it over
// sqlc (Add/Remove write origin='idp_sync' rows + audit; PushOrg fires the org-wide <5s recompile).
type Store interface {
	ListIdpSyncGroups(ctx context.Context, orgID uuid.UUID, provider string) ([]SyncGroup, error)
	ListIdpGroupMemberIDs(ctx context.Context, orgID, groupID uuid.UUID) ([]uuid.UUID, error)
	// ResolveOrgUser maps a directory email to a Tunnex user that belongs to the org. found=false
	// (no matching org user) → the member is skipped: sync grants EXISTING users, it does not
	// JIT-provision (that's S2.5).
	ResolveOrgUser(ctx context.Context, orgID uuid.UUID, email string) (userID uuid.UUID, found bool, err error)
	AddIdpGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
	RemoveIdpGroupMember(ctx context.Context, orgID, groupID, userID uuid.UUID) error
	RecordResult(ctx context.Context, orgID uuid.UUID, provider string, ok, advanceClock bool, errMsg string, now time.Time) error
	PushOrg(ctx context.Context, orgID uuid.UUID)
}

// Deprovisioner cuts a user's access org-wide — the full DeactivateMember sweep (sessions + CLI
// creds + peers out of desired-state + org-wide push). Behind an interface so the reconciler only
// decides WHO to deprovision; the slice-4 adapter wires the real tenancy sweep + audit attribution.
type Deprovisioner interface {
	DeactivateForSync(ctx context.Context, orgID, userID uuid.UUID, provider string) error
}

// NewReconciler builds a reconciler. now defaults to time.Now.
func NewReconciler(p DirectoryProvider, s Store, d Deprovisioner, now func() time.Time) *Reconciler {
	if now == nil {
		now = time.Now
	}
	return &Reconciler{provider: p, store: s, deprov: d, now: now}
}

// ReconcileConfig reconciles every mapped group for one org+provider, then records the poll's
// two-tier health outcome. Per-group fail-static: one group's transient failure never blocks
// another group's authoritative reconcile — it only degrades the config's health.
func (r *Reconciler) ReconcileConfig(ctx context.Context, orgID uuid.UUID, provider string) error {
	groups, err := r.store.ListIdpSyncGroups(ctx, orgID, provider)
	if err != nil {
		// A DB read failure is not a directory outage; surface it without stamping health
		// (we didn't actually attempt a sync).
		return err
	}

	var transientErr error  // first transient fetch/apply failure → immediate + escalating tier
	var goneGroups []string // authoritatively-deleted mappings → immediate tier, non-escalating
	for _, g := range groups {
		gone, gErr := r.reconcileGroup(ctx, orgID, provider, g)
		if gone {
			goneGroups = append(goneGroups, g.IdpGroupID)
		}
		if gErr != nil && transientErr == nil {
			transientErr = gErr
		}
	}

	now := r.now()
	switch {
	case transientErr != nil:
		// FAIL-STATIC health: ok=false, clock FROZEN (advanceClock=false) so staleness accrues
		// toward the escalation tier. Membership was left untouched for the failed group(s).
		_ = r.store.RecordResult(ctx, orgID, provider, false, false, transientErr.Error(), now)
		return transientErr
	case len(goneGroups) > 0:
		// Authoritative but degraded: the fetch(es) succeeded (data is fresh → advanceClock=true,
		// immediate tier only), but a mapped group no longer exists upstream. Membership WAS
		// emptied for it; the operator must re-point or delete the dangling mapping.
		msg := "mapped idp group(s) no longer exist upstream: " + strings.Join(goneGroups, ", ")
		return r.store.RecordResult(ctx, orgID, provider, false, true, msg, now)
	default:
		return r.store.RecordResult(ctx, orgID, provider, true, true, "", now)
	}
}

// reconcileGroup fetches one group's authoritative membership and converges it. The transient-vs-
// authoritative fork lives HERE, and it is the whole fail-static guarantee:
//   - ErrGroupGone  → authoritative empty membership → converge on an empty set (returns gone=true)
//   - other error   → transient → return immediately; converge is NEVER reached, so no removal
//     can be computed from a failed fetch
//   - success       → converge on the fetched set
func (r *Reconciler) reconcileGroup(ctx context.Context, orgID uuid.UUID, provider string, g SyncGroup) (gone bool, err error) {
	members, ferr := r.provider.ListGroupMembers(ctx, g.IdpGroupID)
	if errors.Is(ferr, ErrGroupGone) {
		// Authoritative: the group is deleted upstream → desired membership is empty.
		return true, r.converge(ctx, orgID, provider, g, []DirectoryMember{})
	}
	if ferr != nil {
		// TRANSIENT. Return without ever constructing a desired/removal set. converge is unreachable.
		return false, fmt.Errorf("list members of idp group %s: %w", g.IdpGroupID, ferr)
	}
	return false, r.converge(ctx, orgID, provider, g, members)
}

// converge is the ONLY code that computes removals — and it is only ever called with an
// authoritative member set (a clean fetch, or the empty set of a deleted group). It:
//   - adds active members that resolve to an org user and aren't already in the group
//   - removes current members that are not in the desired-active set
//   - routes StatusDisabled members to the full DeactivateMember sweep (org-wide access cut)
//
// A single org-wide push fires at the end iff anything changed.
func (r *Reconciler) converge(ctx context.Context, orgID uuid.UUID, provider string, g SyncGroup, members []DirectoryMember) error {
	desired := map[uuid.UUID]bool{}
	var deprovision []uuid.UUID
	for _, m := range members {
		uid, found, err := r.store.ResolveOrgUser(ctx, orgID, m.Email)
		if err != nil {
			return err
		}
		if !found {
			continue // sync grants existing org users only; unmatched directory members are skipped
		}
		switch m.Status {
		case StatusActive:
			desired[uid] = true
		case StatusDisabled:
			// Disabled upstream → full sweep AND excluded from the desired set (also removed below).
			deprovision = append(deprovision, uid)
		case StatusGone:
			// A gone user is normally simply absent from a group listing; if a provider ever
			// returns one, treat it as not-desired (the absence path removes it from the group).
		}
	}

	current, err := r.store.ListIdpGroupMemberIDs(ctx, orgID, g.ID)
	if err != nil {
		return err
	}
	currentSet := map[uuid.UUID]bool{}
	for _, uid := range current {
		currentSet[uid] = true
	}

	changed := false
	// Adds — deterministic order for stable audit/logs.
	for _, uid := range sortedKeys(desired) {
		if !currentSet[uid] {
			if err := r.store.AddIdpGroupMember(ctx, orgID, g.ID, uid); err != nil {
				return err
			}
			changed = true
		}
	}
	// Removes — anything currently in the group that isn't a desired-active member. (Disabled
	// members are not in `desired`, so they are removed here too, in addition to the sweep.)
	for _, uid := range current {
		if !desired[uid] {
			if err := r.store.RemoveIdpGroupMember(ctx, orgID, g.ID, uid); err != nil {
				return err
			}
			changed = true
		}
	}
	// Deprovision disabled members (org-wide sweep). Deterministic order.
	sort.Slice(deprovision, func(i, j int) bool { return deprovision[i].String() < deprovision[j].String() })
	for _, uid := range deprovision {
		if err := r.deprov.DeactivateForSync(ctx, orgID, uid, provider); err != nil {
			return err
		}
		changed = true
	}

	if changed {
		r.store.PushOrg(ctx, orgID)
	}
	return nil
}

func sortedKeys(m map[uuid.UUID]bool) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
