// Command operator is the Tunnex GitOps operator (S10.2). It watches TunnexCluster / TunnexExposedService /
// TunnexGrant custom resources and reconciles them against the control-plane HTTP API — the SAME handlers
// the dashboard calls — so a platform team can declare Tunnex state in git.
//
// THE HARD RULE: this is an API CLIENT, never a DB writer. It holds no data-plane privilege and reaches
// Tunnex only over HTTPS with a machine credential (S10.2 Slice 1); every invariant (Collect/OrgRanges,
// identity-binding, edition gate, audit cascade) is inherited through the CP handlers.
//
// Slice 2 is the SKELETON: the manager + scheme + the CRD types. The reconcilers (CR -> CP API call ->
// status) land in Slice 3; delete + drift + the dashboard ownership surface in Slice 4.
package main

import (
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	tunnexv1 "github.com/tunnexio/tunnex/apps/operator/api/v1alpha1"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(tunnexv1.AddToScheme(scheme))
}

func main() {
	ctrl.SetLogger(zap.New())
	log := ctrl.Log.WithName("setup")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{Scheme: scheme})
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Slice 3: the reconcilers are registered here —
	//   (&controllers.TunnexClusterReconciler{...}).SetupWithManager(mgr)
	//   (&controllers.TunnexExposedServiceReconciler{...}).SetupWithManager(mgr)
	//   (&controllers.TunnexGrantReconciler{...}).SetupWithManager(mgr)
	// each holds a CP HTTP client authed with the operator's machine credential (never a DB handle).

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "manager exited non-zero")
		os.Exit(1)
	}
}
