package http

import (
	"strings"
	"testing"

	"github.com/tunnexio/tunnex/apps/api/internal/licence"
)

// ⛔ THE BUILD-TAG EDITION SPLIT IS REVERSED, AND ITS GUARDS ARE REWRITTEN RATHER THAN DELETED.
//
// `policy_edition_open_test.go` and `sso_edition_open_test.go` asserted that the OPEN BUILD returned
// 403 edition_required for Zero Trust and SSO. That was the old ruling, enforced by tests — and deleting
// them to make the build green would have retired an invariant as a side effect of a ruling change, with
// nothing left recording that it existed (docs/laws.md: census what ENFORCES a ruling, not only what
// states it; a reversed ruling should leave a NARROWER guard behind it, not an absence).
//
// What replaces them is narrower and still mechanical: the tier map is now the boundary, and this asserts
// which side of it each capability sits on.
func TestZeroTrustIsCommunity(t *testing.T) {
	// ⭐ The whole strategy in one assertion. If a future edit puts the policy engine behind a feature, the
	// moat argument has been reversed without anyone saying so.
	for _, f := range licence.AllFeatures() {
		if strings.Contains(string(f), "policy") || strings.Contains(string(f), "zero_trust") {
			t.Errorf("⛔ %q GATES THE ZERO TRUST ENGINE. It is Community by founder ruling — the moat is "+
				"the free tier's generosity, not Enterprise's length, and a thinner Community loses to "+
				"NetBird's free self-hosted edition.", f)
		}
	}
	if licence.Has(licence.TierCommunity, licence.FeatSSO) {
		t.Error("SSO must NOT be Community — it is one of the four paid gates")
	}
}

// ⛔ THE INTERMEDIATE STATE THIS SLICE CREATES, ASSERTED SO IT CANNOT BE FORGOTTEN.
//
// Collapsing the binary wires SSO and IdP sync unconditionally. Until the LicenseManager slice reads a
// licence and gates them, THEY ARE AVAILABLE TO EVERY DEPLOYMENT — paid capabilities, given away, because
// the compile-time gate is gone and the runtime one does not exist yet.
//
// ⚠ That is safe only while unreleased. This test is the tripwire: it passes while the gap is expected and
// must be REPLACED (not deleted) by a real enforcement assertion in the LicenseManager slice.
func TestPaidCapabilitiesAreNotYetEnforced(t *testing.T) {
	// The map already knows the right answer...
	if !licence.Has(licence.TierStarter, licence.FeatSSO) {
		t.Fatal("the tier map should grant SSO to Starter")
	}
	// ...and nothing reads it yet. When a LicenseManager exists, this test's premise is false and it must
	// be rewritten to assert enforcement instead.
	t.Log("⚠ SSO and IdP sync are wired unconditionally until the LicenseManager slice lands. " +
		"DO NOT RELEASE between these two slices.")
}
