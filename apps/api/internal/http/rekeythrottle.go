package http

import (
	"net"
	"net/http"
	"sync"
	"time"
)

// rekeyThrottle is THE RE-KEY ENDPOINTS' OWN throttle. It is deliberately not called `RateLimiter`, not exported,
// and not in a shared middleware package — the name and the placement are the guard.
//
// WHY THAT MATTERS. A well-placed forty-line limiter is exactly what a future story reaches for when it needs
// general rate limiting, and this one is sized for a narrow surface and would be wrong as the general mechanism: it
// keys on remote IP only, keeps unbounded per-IP state until swept, and has no per-account or per-route
// configuration. Rate limiting for login, enrolment, the agent channel and the wider API remains OWED
// (docs/S13.1-decisions.md records it honestly). If you are here because you need that, do not import this.
//
// WHY IT EXISTS AT ALL. The re-key routes are UNAUTHENTICATED by construction — the caller's certificate is the
// thing that has failed — and one of them performs RSA verification, which is CPU-amplifying, while the other
// writes a database row per call.
//
// AND WHY IT IS SMALL. The gate runs before any cryptographic work, so the cheap path is the common one: a random
// certificate serial is refused by a field comparison, and reaching RSA verification requires knowing a REAL serial
// for a genuinely expired node. So this defends a narrow surface and is sized for that rather than over-built.
type rekeyThrottle struct {
	mu      sync.Mutex
	seen    map[string]*bucket
	perMin  int
	sweepAt time.Time
}

type bucket struct {
	count int
	reset time.Time
}

func newRekeyThrottle(perMin int) *rekeyThrottle {
	return &rekeyThrottle{seen: map[string]*bucket{}, perMin: perMin}
}

// allow reports whether this caller may proceed, and counts the attempt.
//
// Fixed window rather than a token bucket: the quantity being protected is CPU and rows per minute, and a fixed
// window is trivially auditable by reading it. A burst at a window boundary is worth twice the budget, which for
// this surface is not a meaningful difference.
func (t *rekeyThrottle) allow(remoteAddr string) bool {
	key := clientIP(remoteAddr)
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()

	// Sweep occasionally so an unauthenticated endpoint cannot grow this map without bound. Done inline rather
	// than on a goroutine: no lifecycle to own, and the cost is proportional to what was actually admitted.
	if now.After(t.sweepAt) {
		for k, b := range t.seen {
			if now.After(b.reset) {
				delete(t.seen, k)
			}
		}
		t.sweepAt = now.Add(time.Minute)
	}

	b, ok := t.seen[key]
	if !ok || now.After(b.reset) {
		t.seen[key] = &bucket{count: 1, reset: now.Add(time.Minute)}
		return true
	}
	if b.count >= t.perMin {
		return false
	}
	b.count++
	return true
}

// clientIP strips the port. It reads RemoteAddr ONLY and deliberately ignores X-Forwarded-For: a header a caller
// controls is not an identity, and trusting it here would let one attacker present as a million. A deployment behind
// a proxy therefore throttles per-proxy, which is a real limitation and the honest one — resolving it needs a
// trusted-proxy configuration, which belongs to the general mechanism rather than to this.
func clientIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}

// throttled wraps a handler. On refusal it answers 429 with no detail — the same uniform-response discipline as the
// re-key refusal itself, so the throttle cannot be used to learn anything the endpoint would not otherwise tell.
func (t *rekeyThrottle) throttled(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !t.allow(r.RemoteAddr) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many requests", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// rekeyAttemptsPerMinute is generous for a real recovering gateway (which needs two requests, once) and
// uninteresting to an attacker, who gains nothing from a handful of guesses against a gate that requires knowing a
// real certificate serial for a genuinely expired node.
const rekeyAttemptsPerMinute = 10

// rekeyOnly applies the throttle to the re-key paths and leaves every other route untouched.
//
// Scoped by PATH deliberately. A global limiter is what the general rate-limiting story will need, and quietly
// becoming that story is the failure mode this file is written to avoid — so this one refuses to cover anything it
// was not reasoned about.
func rekeyOnly(t *rekeyThrottle) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/agent/rekey", "/api/v1/agent/rekey/challenge":
				t.throttled(next.ServeHTTP)(w, r)
			default:
				next.ServeHTTP(w, r)
			}
		})
	}
}
