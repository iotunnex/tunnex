package http

import (
	"testing"
	"time"

	"github.com/tunnexio/tunnex/apps/api/internal/licence"
)

// ⛔ THE EDITION IS A LICENCE READ, ASSERTED IN BOTH DIRECTIONS.
//
// From S12.1 (`34004a72`) until this fix, `/meta` reported "open" unconditionally — the build-tag split was
// removed and `const Name = "open"` was left as the only definition. Eleven web files gate on this value,
// so a fully licensed customer saw upsell cards on every enterprise surface.
//
// ⚠ ONE DIRECTION IS HALF A TEST, AND IT IS THE WORTHLESS HALF. A handler hardcoded to "enterprise" would
// pass "a paid licence reports enterprise" perfectly — and would be exactly the bug that just shipped,
// mirrored. The unlicensed case is what makes the paid case mean anything.
func TestMetaEditionFollowsTheLicence(t *testing.T) {
	for _, tc := range []struct {
		name string
		mgr  *licence.Manager
		want string
	}{
		// ⭐ The commonest deployment there is. Community is a product, not a degraded state.
		{"no licence at all", &licence.Manager{}, "open"},
		{"trial", licence.NewTestManager("trial", time.Now().Add(time.Hour)), "enterprise"},
		{"starter", licence.NewTestManager("starter", time.Now().Add(time.Hour)), "enterprise"},
		{"scale", licence.NewTestManager("scale", time.Now().Add(time.Hour)), "enterprise"},
		// ⭐ GRACE, AND IT NEEDS NO CASE IN THE HANDLER. Evaluate keeps the licensed tier for the whole
		// 90 days, so the UI keeps working for a customer who is one day late renewing — the ladder's
		// whole point, inherited rather than re-implemented.
		{"expired, inside grace", licence.NewTestManager("growth", time.Now().Add(-24*time.Hour)), "enterprise"},
		// ⛔ AND AFTER GRACE THE SURFACES CLOSE ON THEIR OWN, because the tier falls to Community.
		{"lapsed past grace", licence.NewTestManager("growth", time.Now().Add(-100*24*time.Hour)), "open"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (apiServer{licence: tc.mgr}).editionName()
			if got != tc.want {
				t.Errorf("edition = %q, want %q", got, tc.want)
			}
		})
	}
}

// ⛔ THE REGRESSION GUARD, NAMED FOR WHAT IT CATCHES. The defect was not a wrong branch — it was a
// CONSTANT. Anything that makes this function ignore its input reintroduces it exactly.
func TestMetaEditionIsNotAConstant(t *testing.T) {
	unlicensed := (apiServer{licence: &licence.Manager{}}).editionName()
	licensed := (apiServer{licence: licence.NewTestManager("growth", time.Now().Add(time.Hour))}).editionName()
	if unlicensed == licensed {
		t.Fatalf("⛔ /meta REPORTS %q REGARDLESS OF THE LICENCE. This is the S12.1 defect: eleven web "+
			"files gate on this value, so either every deployment sees upsell cards it has paid past, or "+
			"every deployment is shown surfaces it has not paid for", unlicensed)
	}
}
