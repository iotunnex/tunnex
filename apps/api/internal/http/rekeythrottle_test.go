package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestThrottleRefusesBeyondTheWindowBudget — the throttle actually throttles. Trivial to assert and the whole reason
// the file exists, since the endpoints it protects are unauthenticated and one of them performs RSA verification.
func TestThrottleRefusesBeyondTheWindowBudget(t *testing.T) {
	th := newRekeyThrottle(3)
	for i := 1; i <= 3; i++ {
		if !th.allow("203.0.113.7:44321") {
			t.Fatalf("attempt %d must be allowed within a budget of 3", i)
		}
	}
	if th.allow("203.0.113.7:44321") {
		t.Error("the fourth attempt in one window must be refused — an unauthenticated endpoint that performs " +
			"RSA verification is a CPU-amplification surface without this")
	}
	// A DIFFERENT caller is unaffected: throttling one address must not deny the fleet.
	if !th.allow("198.51.100.9:1234") {
		t.Error("a different client IP must have its own budget — otherwise one attacker denies every recovering gateway")
	}
}

// TestThrottleIgnoresCallerControlledHeaders — the identity must not be spoofable.
//
// X-Forwarded-For is set by the caller unless a trusted proxy overwrites it. Keying on it would let one attacker
// present as a million distinct clients, which is worse than having no throttle: it would look protected.
func TestThrottleIgnoresCallerControlledHeaders(t *testing.T) {
	th := newRekeyThrottle(2)
	h := th.throttled(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	codes := []int{}
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest("POST", "/api/v1/agent/rekey", nil)
		req.RemoteAddr = "203.0.113.7:44321"
		// A rotating forged forwarding header, which must buy the caller nothing.
		req.Header.Set("X-Forwarded-For", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"}[i])
		rec := httptest.NewRecorder()
		h(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[2] != http.StatusTooManyRequests || codes[3] != http.StatusTooManyRequests {
		t.Errorf("a rotating X-Forwarded-For must not extend the budget — a header the caller controls is not an "+
			"identity. Got codes %v", codes)
	}
}

// TestThrottleRefusalLeaksNothing — same discipline as the re-key refusal itself: 429 with no detail. A throttle
// that explained itself would answer questions the endpoint deliberately refuses to answer.
func TestThrottleRefusalLeaksNothing(t *testing.T) {
	th := newRekeyThrottle(1)
	h := th.throttled(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest("POST", "/api/v1/agent/rekey", nil)
		req.RemoteAddr = "203.0.113.7:1"
		rec := httptest.NewRecorder()
		h(rec, req)
		if i == 1 {
			if rec.Code != http.StatusTooManyRequests {
				t.Fatalf("want 429, got %d", rec.Code)
			}
			if rec.Header().Get("Retry-After") == "" {
				t.Error("a refusal should tell a legitimate agent when to come back — that is not a leak, it is the " +
					"one thing the caller needs")
			}
			if body := rec.Body.String(); len(body) > 32 {
				t.Errorf("the refusal body must carry no detail; got %q", body)
			}
		}
	}
}
