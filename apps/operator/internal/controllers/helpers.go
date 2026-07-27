package controllers

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/tunnexio/tunnex/apps/operator/internal/cp"
)

// finalizerName holds a CR back from k8s garbage-collection until the operator has deleted its control-plane
// object through the AUDITED API verb (D2 cond 2) — never a dangling CP object, never a DB delete.
const finalizerName = "tunnex.io/finalizer"

// crRef is the audit cause the operator names on a delete: the CR that drove it (e.g.
// "tunnexcluster:default/prod") — so a cascade delete is traceable to the git declaration, not just the
// credential (D2 cond 2, the S10.3 H2 lesson: governance must not vanish untraceably).
func crRef(kind, namespace, name string) string {
	return kind + ":" + namespace + "/" + name
}

// ignoreCPNotFound treats a CP 404 on delete as success — the object is already gone (idempotent teardown,
// so a retried finalizer or a cascade that already removed it converges instead of wedging).
func ignoreCPNotFound(err error) error {
	if e := cp.AsAPIError(err); e != nil && e.Status == http.StatusNotFound {
		return nil
	}
	return err
}

// ensureMeta stamps the ownership label AND the finalizer, reporting whether either changed. Called only on a
// live (non-deleting) object — the finalizer must be present BEFORE the first CP create so a delete can never
// race ahead of teardown.
func ensureMeta(obj client.Object) bool {
	changed := false
	labels := obj.GetLabels()
	if labels[managedByLabel] != managedByValue {
		if labels == nil {
			labels = map[string]string{}
		}
		labels[managedByLabel] = managedByValue
		obj.SetLabels(labels)
		changed = true
	}
	if !controllerutil.ContainsFinalizer(obj, finalizerName) {
		controllerutil.AddFinalizer(obj, finalizerName)
		changed = true
	}
	return changed
}

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
