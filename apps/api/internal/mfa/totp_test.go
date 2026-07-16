package mfa

import "testing"

// codeAt computes the valid code for a secret at a given unix time (test helper via the
// same hotp path Validate uses).
func codeAt(t *testing.T, secret string, unix int64) string {
	t.Helper()
	key, err := base32NoPad.DecodeString(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	return hotp(key, Timestep(unix))
}

func TestValidateAcceptsCurrentCode(t *testing.T) {
	secret, _ := GenerateSecret()
	now := int64(1_700_000_000)
	code := codeAt(t, secret, now)
	if ts, ok := Validate(secret, code, now, -1); !ok || ts != Timestep(now) {
		t.Fatalf("current code must validate at its timestep, got ts=%d ok=%v", ts, ok)
	}
}

// TestValidateReplayGuard is the deliberate RED: a code accepted once must NEVER be accepted
// again — Validate rejects any timestep <= lastUsed.
func TestValidateReplayGuard(t *testing.T) {
	secret, _ := GenerateSecret()
	now := int64(1_700_000_000)
	code := codeAt(t, secret, now)
	ts, ok := Validate(secret, code, now, -1)
	if !ok {
		t.Fatal("first use must accept")
	}
	// Replay the SAME code with lastUsed advanced to its timestep -> refused.
	if _, ok := Validate(secret, code, now, ts); ok {
		t.Fatal("REPLAY: a code at a timestep <= lastUsed must be refused")
	}
}

// TestValidateWindow accepts the ±1 neighbour (clock skew) but the replay guard still binds.
func TestValidateWindow(t *testing.T) {
	secret, _ := GenerateSecret()
	now := int64(1_700_000_000)
	prev := now - totpPeriod // one step earlier
	code := codeAt(t, secret, prev)
	if _, ok := Validate(secret, code, now, -1); !ok {
		t.Fatal("a code from the previous step must validate within the ±1 window")
	}
	// But once that step is used, its code can't be replayed.
	if _, ok := Validate(secret, code, now, Timestep(prev)); ok {
		t.Fatal("REPLAY within window must be refused")
	}
}

func TestValidateRejectsWrongCode(t *testing.T) {
	secret, _ := GenerateSecret()
	now := int64(1_700_000_000)
	if _, ok := Validate(secret, "000000", now, -1); ok {
		// astronomically unlikely to be the real code; guards the happy path isn't a no-op
		t.Skip("000000 happened to be valid — rerun")
	}
	if _, ok := Validate(secret, "abc", now, -1); ok {
		t.Fatal("a malformed code must be refused")
	}
}
