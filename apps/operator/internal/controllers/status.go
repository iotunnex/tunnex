// Package controllers holds the three Tunnex reconcilers (S10.2 Slice 3). Each watches one CRD and reconciles
// it against the control-plane HTTP API — CR spec -> CP verb -> CR status. THE HARD RULE holds: they call the
// CP as a machine principal (a *cp.Client), never a DB handle.
//
// Two laws shape every reconcile:
//   - HONEST STATUS: a CR is Ready=True ONLY when the CP ACCEPTED it. A CP 4xx (bad spec, edition_required,
//     a subject that doesn't resolve) becomes Ready=False naming the CP's own code + message verbatim — never
//     a silent success, never a guess.
//   - KEEP-LAST / FAIL-STATIC: a transport failure or CP 5xx does NOT touch status. The last-good status
//     stands and the controller requeues with backoff. A blip never flips a healthy CR to a scary state.
package controllers

import (
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/tunnexio/tunnex/apps/operator/internal/cp"
)

const (
	condReady = "Ready"

	// managedByLabel marks a CR the Tunnex operator reconciles (k8s-side visibility for kubectl + the
	// dashboard ownership surface, Slice 4). The AUTHORITATIVE ownership record is CP-side: managed_by_machine,
	// which the machine principal stamps on every create (Slice 3a) because the operator authenticates with
	// its `tnxm_` credential. This label is the k8s mirror of that fact.
	managedByLabel = "tunnex.io/managed-by"
	managedByValue = "operator"

	// requeueDependency paces an ordering wait — a service whose cluster is not yet Ready, a grant whose
	// service is not yet Ready. Short: the dependency is reconciling concurrently.
	requeueDependency = 5 * time.Second
	// clientErrRequeue paces a slow retry after a CP 4xx (edition_required flips when the org upgrades; a
	// subject appears once created) WITHOUT hot-spinning on a genuinely bad spec. A spec edit re-triggers
	// immediately regardless.
	clientErrRequeue = 60 * time.Second
)

// setReady flips the CR's Ready condition, pinning it to the spec generation the reconcile acted on.
func setReady(conds *[]metav1.Condition, status metav1.ConditionStatus, reason, msg string, gen int64) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type: condReady, Status: status, Reason: reason, Message: msg, ObservedGeneration: gen,
	})
}

// reasonFor turns a CP error code into a valid condition reason (CP codes are snake_case — already valid);
// empty falls back to Rejected.
func reasonFor(code string) string {
	if code == "" {
		return "Rejected"
	}
	return code
}

// onCPError branches a CP call's error onto the two laws. Returns handled=true when the reconcile is done for
// this pass:
//   - err == nil                → handled=false; the caller proceeds to its success path.
//   - *APIError (CP 4xx)        → HONEST: writes Ready=False(code,message) into conds; result requeues slow,
//     returned error is nil (a bad spec is not a controller error). The caller must persist status.
//   - transport / CP 5xx        → KEEP-LAST: conds UNTOUCHED; returns the error so controller-runtime backs
//     off. The caller must NOT persist status.
//
// persistStatus reports whether the caller should write status before returning (true only on the 4xx branch).
func onCPError(conds *[]metav1.Condition, err error, gen int64) (res ctrl.Result, retErr error, handled, persistStatus bool) {
	if err == nil {
		return ctrl.Result{}, nil, false, false
	}
	if e := cp.AsAPIError(err); e != nil {
		setReady(conds, metav1.ConditionFalse, reasonFor(e.Code), e.Message, gen)
		return ctrl.Result{RequeueAfter: clientErrRequeue}, nil, true, true
	}
	return ctrl.Result{}, err, true, false // keep-last: status stays as-is
}
