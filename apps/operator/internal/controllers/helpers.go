package controllers

import (
	"context"

	"github.com/tunnexio/tunnex/apps/operator/internal/cp"
)

// ── friendly-name → UUID resolution (read-only CP lookups; a not-found is an HONEST non-Ready, not an error) ──
//
// found=false means the CP has no such site/user/group — a spec problem the reconciler renders as Ready=False.
// A non-nil err is a transport/CP failure — keep-last (the reconciler holds status and requeues).

func resolveSite(ctx context.Context, c *cp.Client, name string) (id string, found bool, err error) {
	sites, err := c.ListSites(ctx)
	if err != nil {
		return "", false, err
	}
	for _, s := range sites {
		if s.Name == name {
			return s.ID, true, nil
		}
	}
	return "", false, nil
}

func resolveMember(ctx context.Context, c *cp.Client, email string) (id string, found bool, err error) {
	members, err := c.ListMembers(ctx)
	if err != nil {
		return "", false, err
	}
	for _, m := range members {
		if m.Email == email {
			return m.UserID, true, nil
		}
	}
	return "", false, nil
}

func resolveGroup(ctx context.Context, c *cp.Client, name string) (id string, found bool, err error) {
	groups, err := c.ListGroups(ctx)
	if err != nil {
		return "", false, err
	}
	for _, g := range groups {
		if g.Name == name {
			return g.ID, true, nil
		}
	}
	return "", false, nil
}

// ensureManagedLabel adds the operator ownership label if absent, returning the (possibly new) label map and
// whether it changed. The reconciler persists a change with a metadata Update, then requeues so the next pass
// runs against the labeled object.
func ensureManagedLabel(labels map[string]string) (map[string]string, bool) {
	if labels[managedByLabel] == managedByValue {
		return labels, false
	}
	if labels == nil {
		labels = map[string]string{}
	}
	labels[managedByLabel] = managedByValue
	return labels, true
}
