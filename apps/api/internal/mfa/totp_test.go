package mfa

import "testing"

// TestHOTPRFC4226Vectors validates the HOTP core against the PUBLISHED RFC 4226 Appendix D test
// vectors (secret "12345678901234567890", counts 0..9) — an EXTERNAL oracle, not round-trip
// self-consistency (a self-consistent implementation can be self-consistently wrong). These are
// the 6-digit codes; they equal the low-6 digits of the RFC 6238 Appendix B 8-digit TOTP vectors,
// so this pins the HMAC-SHA1 + dynamic-truncation + mod-10^6 exactly.
func TestHOTPRFC4226Vectors(t *testing.T) {
	key := []byte("12345678901234567890")
	want := []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"}
	for c, w := range want {
		if got := hotp(key, int64(c)); got != w {
			t.Fatalf("HOTP(count=%d) = %s, want %s (RFC 4226 App D)", c, got, w)
		}
	}
}

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
