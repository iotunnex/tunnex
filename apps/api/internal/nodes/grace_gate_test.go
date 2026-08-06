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
