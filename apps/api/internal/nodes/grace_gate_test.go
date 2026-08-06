package nodes

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/licence"
)

// ⛔ THE GATE PROVEN WHERE IT FIRES, NOT WHERE IT WAS WRITTEN. licence's own tests prove the ladder
// computes the right state; this proves the enrolment path ASKS. The two are independent failures and the
// second one is the one that ships an unenforced licence.
func TestEnrolmentGateFollowsTheLadder(t *testing.T) {
	exp := time.Now().Add(-time.Hour)

	if (&Service{}).checkNewPrincipalAllowed() != nil {
		t.Fatal("⛔ an unwired manager refused an enrolment — every open-source deployment just lost the " +
			"ability to enrol a gateway")
	}
	if (&Service{licence: licence.NewTestManager("growth", time.Now().Add(time.Hour))}).checkNewPrincipalAllowed() != nil {
		t.Fatal("a valid licence refused an enrolment")
	}

	err := (&Service{licence: licence.NewTestManager("growth", exp)}).checkNewPrincipalAllowed()
	if err == nil {
		t.Fatal("⛔ AN EXPIRED LICENCE ENROLLED A NEW GATEWAY. Growth is the one thing grace stops")
	}
	var ae *apierr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("the refusal is not an API error, so the agent sees a 500: %v", err)
	}
	if ae.Status != 403 || ae.Code != "licence_expired" {
		t.Errorf("refusal = %d/%s, want 403/licence_expired", ae.Status, ae.Code)
	}
	// ⭐ The agent operator reads this in a log with no UI around it.
	if !strings.Contains(ae.Message, "keeps working") {
		t.Errorf("the refusal does not say the existing fleet is unaffected: %s", ae.Message)
	}
}

// ⛔ THE SPENT-TOKEN SENTENCE, GUARDED — because it is the only thing standing between an operator and a
// second, unrelated error.
//
// The band refusal fires after ConsumeJoinToken, so the token is gone. An operator who upgrades and retries
// with it meets `invalid_join_token`, which describes a token problem rather than a licence one and sends
// them looking in the wrong place entirely. The burn is registered, not fixed; this sentence is what makes
// it survivable, so it is asserted rather than left to whoever next edits the wording.
func TestBandRefusalWarnsTheTokenIsSpent(t *testing.T) {
	msg := (&Service{}).ceilingRefusal(licence.TierTrial, 2, 2)
	// ⚠ THE COUNT PLURALISES SEPARATELY FROM THE CEILING. "allows 1 gateway, and 1 are already enrolled"
	// reached a real screen, because only the ceiling was pluralised.
	if one := (&Service{}).ceilingRefusal(licence.TierCommunity, 1, 1); !strings.Contains(one, "1 is already enrolled") {
		t.Errorf("singular count reads wrong:\n%s", one)
	}
	if two := (&Service{}).ceilingRefusal(licence.TierTrial, 2, 2); !strings.Contains(two, "2 are already enrolled") {
		t.Errorf("plural count reads wrong:\n%s", two)
	}
	for _, want := range []string{"used up", "mint a new one", "keep working", "upgrade the licence"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the band refusal never says %q:\n%s", want, msg)
		}
	}
}
