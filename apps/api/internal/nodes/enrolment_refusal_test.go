package nodes

import "testing"

// ⛔ THE RULE IS TESTED SEPARATELY FROM ITS ARMING, AND THAT SEPARATION IS THE POINT.
//
// While `enrolmentRefusalArmed` is false, every test that reaches the refusal through
// `RefuseUnownedEnrolment` asserts that `false` is `false`. It would pass with the rule inverted, deleted,
// or replaced by `return false` — and it would hand the commit that ARMS the refusal a test suite that had
// never once exercised what it is about to switch on.
//
// This is the tautological-guard law in its natural habitat: a guard whose test cannot fail is not a test.

func TestRefusalRuleBothDirections(t *testing.T) {
	// The rule itself, reachable without the constant.
	if !RefusalWouldFire(false) {
		t.Fatal("an enrolment with NO owner must fire the refusal")
	}
	// ⛔ THE SECOND HALF IS WHY THE FIRST MEANS ANYTHING. A rule that refused everything would pass above.
	if RefusalWouldFire(true) {
		t.Fatal("an enrolment WITH an owner must NOT fire the refusal")
	}
}

// TestRefusalIsUnarmedAndSaysSo pins the shipped state, so arming is a DELIBERATE act rather than a drift.
//
// ⚠ THIS TEST IS EXPECTED TO BE EDITED — that is its job. The commit that arms the refusal must change this
// file, which makes the arming visible in a diff and impossible to do by accident. A constant with no test
// can be flipped in a one-line commit nobody notices; this one cannot.
func TestRefusalIsUnarmedAndSaysSo(t *testing.T) {
	if EnrolmentRefusalArmed() {
		t.Fatal("the enrolment refusal must ship UNARMED until the D14 restore proof is discharged " +
			"(docs/S15.0-decisions.md §15): one credential, three states, on the wire — " +
			"refused → assigned → authenticates. Arming it before then ships a refusal whose cure has " +
			"never been watched working, on a data plane.")
	}
	// Unarmed means the gate returns false REGARDLESS of the rule — both inputs, so this cannot pass by
	// accidentally agreeing with the rule on one of them.
	if RefuseUnownedEnrolment(false) || RefuseUnownedEnrolment(true) {
		t.Fatal("while unarmed, no enrolment may be refused for want of an owner")
	}
	// ⛔ AND THE RULE UNDERNEATH IS STILL CORRECT WHILE UNARMED — the thing that will be switched on is
	// known-good, which is the entire reason for building it now rather than later.
	if !RefusalWouldFire(false) || RefusalWouldFire(true) {
		t.Fatal("the rule must remain correct while unarmed, or arming it later is a leap of faith")
	}
}
