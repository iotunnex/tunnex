package controllers

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	tunnexv1 "github.com/tunnexio/tunnex/apps/operator/api/v1alpha1"
	"github.com/tunnexio/tunnex/apps/operator/internal/cp"
)

// TunnexClusterReconciler registers a TunnexCluster on the fabric through the CP RegisterCluster verb. It is
// the ROOT of the ordering chain: a TunnexExposedService waits on its cluster's status.ClusterID, and a
// TunnexGrant waits on the service — so cluster-before-service-before-grant falls out of each reconciler
// requeueing until its dependency's status is populated (no topological sort).
type TunnexClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	CP     *cp.Client
}

// +kubebuilder:rbac:groups=tunnex.io,resources=tunnexclusters,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=tunnex.io,resources=tunnexclusters/status,verbs=get;update;patch

func (r *TunnexClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr tunnexv1.TunnexCluster
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err) // delete is the finalizer's job (Slice 4)
	}
	if labels, changed := ensureManagedLabel(cr.Labels); changed {
		cr.Labels = labels
		if err := r.Update(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil // next pass runs against the labeled object
	}
	gen := cr.Generation

	// Resolve the fronting site NAME -> id (the CP verb wants a UUID).
	siteID, found, err := resolveSite(ctx, r.CP, cr.Spec.Site)
	if err != nil {
		return ctrl.Result{}, err // transport/5xx -> keep-last
	}
	if !found {
		setReady(&cr.Status.Conditions, metav1.ConditionFalse, "site_not_found",
			"no site named "+cr.Spec.Site+" in this org", gen)
		if e := r.Status().Update(ctx, &cr); e != nil {
			return ctrl.Result{}, e
		}
		return ctrl.Result{RequeueAfter: clientErrRequeue}, nil
	}

	// Idempotent: find-by-name before create (reconcile runs repeatedly; never double-register).
	clusters, err := r.CP.ListClusters(ctx)
	if err != nil {
		return ctrl.Result{}, err // keep-last
	}
	var reg *cp.Cluster
	for i := range clusters {
		if clusters[i].Name == cr.Spec.Name {
			reg = &clusters[i]
			break
		}
	}
	if reg == nil {
		c, err := r.CP.RegisterCluster(ctx, cp.RegisterClusterRequest{
			SiteID: siteID, Name: cr.Spec.Name, VipRange: cr.Spec.VIPRange,
			ServiceCidr: cr.Spec.ServiceCIDR, DnsZone: cr.Spec.DNSZone,
		})
		if res, e, handled, persist := onCPError(&cr.Status.Conditions, err, gen); handled {
			if persist {
				if u := r.Status().Update(ctx, &cr); u != nil {
					return ctrl.Result{}, u
				}
			}
			return res, e
		}
		reg = &c
	}

	// Accepted — mirror the CP's DERIVED truth into status.
	cr.Status.ClusterID = reg.ID
	cr.Status.DNSVIP = reg.DnsVip
	setReady(&cr.Status.Conditions, metav1.ConditionTrue, "Accepted", "control plane registered the cluster", gen)
	return ctrl.Result{}, r.Status().Update(ctx, &cr)
}

func (r *TunnexClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&tunnexv1.TunnexCluster{}).Complete(r)
}
