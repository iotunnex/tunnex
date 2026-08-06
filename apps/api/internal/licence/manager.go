package licence

import (
	"crypto/ed25519"
	"sync"
	"time"
)

// Manager answers "what is this deployment entitled to" — offline, from a signed key, or from nothing.
//
// ⛔ AN ABSENT LICENCE IS COMMUNITY, NOT A REFUSAL (founder-ruled). A deployment that upgrades into this
// code must not lose anything it can keep under Community: one gateway, one org, the complete Zero Trust
// engine. What it loses is SSO, IdP sync, and the ability to enrol a SECOND gateway or org — and it must
// SAY so rather than fail silently.
//
// ⭐ THE ZERO VALUE IS USABLE AND MEANS COMMUNITY. That is the fail-open default, and it exists in the same
// commit as the reader on purpose: the moment a capability starts asking a real question there must never
// be a window where nothing answers.
type Manager struct {
	mu sync.RWMutex
	// ⚠ THE PARSE IS CACHED. THE VERDICT IS NOT. Settings change on write; a licence expires on TIME, so a
	// verdict computed at load is wrong from the first second after expiry. Every read re-evaluates the
	// clock against the cached claims.
	//
	// Cost on the hot path: two int64 comparisons and an RLock. The signature check — the expensive part —
	// happens once, in Install.
	claims *Claims
	clock  Clock
}

// State is where a deployment sits on the degradation ladder.
type State int

const (
	// StateUnlicensed — no key. Community. Not an error and not a failure.
	StateUnlicensed State = iota
	// StateValid — a key, not expired.
	StateValid
	// StateExpired — past expiry, inside grace. ⛔ NOTHING STOPS. A warning is shown.
	StateExpired
	// StateLapsed — past expiry + grace. Gated capabilities stop; the VPN does not.
	StateLapsed
)

// GracePeriod is the 90 days after expiry during which everything keeps working.
const GracePeriod = 90 * 24 * time.Hour

// Install verifies a wire key and caches its claims. An error leaves the previous state untouched — a bad
// paste must never downgrade a working deployment.
func (m *Manager) Install(keys map[string]ed25519.PublicKey, wire string) (Result, error) {
	res, err := Verify(keys, wire)
	if err != nil || !res.OK {
		return res, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c := res.Claims
	m.claims = &c
	return res, nil
}

// Status is the whole answer, evaluated against `now`.
type Status struct {
	State State
	Tier  Tier
	// ExpiresAt is zero when unlicensed.
	ExpiresAt time.Time
	// GraceEndsAt is when gated capabilities stop. Zero unless expired.
	GraceEndsAt time.Time
	// ClockWentBackwards is honest instrumentation, never a refusal.
	ClockWentBackwards bool
}

// Evaluate answers for a given instant. ⚠ PER READ — see the note on Manager.claims.
func (m *Manager) Evaluate(now time.Time) Status {
	obs := m.clock.Observe(now)

	m.mu.RLock()
	c := m.claims
	m.mu.RUnlock()

	if c == nil {
		return Status{State: StateUnlicensed, Tier: TierCommunity, ClockWentBackwards: obs.BackwardJump}
	}

	// ⚠ A TIER THIS BUILD DOES NOT KNOW IS COMMUNITY, not everything. A licence naming an unknown tier is
	// one this build cannot honour, and the safe reading is the free tier.
	tier := Tier(c.Tier)
	if !KnownTier(tier) {
		tier = TierCommunity
	}

	exp := time.Unix(c.ExpiresAt, 0)
	st := Status{Tier: tier, ExpiresAt: exp, ClockWentBackwards: obs.BackwardJump}
	switch {
	case now.Before(exp):
		st.State = StateValid
	case now.Before(exp.Add(GracePeriod)):
		// ⛔ EXPIRED IS NOT LAPSED. Nothing stops here — the entitlement is unchanged and a warning is the
		// only difference. That is the ruling: a running VPN never stops, and no human is ever blocked.
		st.State = StateExpired
		st.GraceEndsAt = exp.Add(GracePeriod)
	default:
		st.State = StateLapsed
		st.GraceEndsAt = exp.Add(GracePeriod)
		// ⛔ AFTER GRACE THE GATED CAPABILITIES STOP, which is expressed by falling back to Community —
		// NOT by refusing. Existing gateways and orgs keep running; only enrolment is affected.
		st.Tier = TierCommunity
	}
	return st
}

// Has is the entitlement question every gated capability asks.
//
// ⚠ NEVER `if edition == "enterprise"`. This reads the ONE map, so moving a feature between tiers stays a
// one-line change.
func (m *Manager) Has(f Feature, now time.Time) bool {
	return Has(m.Evaluate(now).Tier, f)
}

// GatewayCeilingNow is the number of gateways this deployment may ENROL. nil means unlimited.
//
// ⛔ AT ENROLMENT ONLY. Running gateways are never stopped, and an UPGRADE IS NOT AN ENROLMENT: a
// deployment already running three gateways keeps all three and cannot add a fourth.
func (m *Manager) GatewayCeilingNow(now time.Time) *int {
	c, _ := GatewayCeilingFor(m.Evaluate(now).Tier)
	return c
}

// OrgCeilingNow is the number of organizations this deployment may CREATE. nil means unlimited.
func (m *Manager) OrgCeilingNow(now time.Time) *int {
	c, _ := OrgCeilingFor(m.Evaluate(now).Tier)
	return c
}

// AllowsNewPrincipals reports whether a new principal — a device, an agent, a gateway — may be enrolled.
//
// ⛔ THIS IS THE GRACE LADDER'S TEETH, AND IT IS THE ONLY THING GRACE CHANGES.
//
//	valid    → yes
//	expired  → NO. Everything already enrolled keeps working; nothing stops. What stops is GROWTH.
//	lapsed   → NO.
//
// ⚠ THAT IS THE WHOLE DISTINCTION THE MODEL RESTS ON: a limit that blocks a new principal blocks GROWTH,
// and a limit that stops a running one blocks WORK. Grace refuses the first and never the second — a
// running VPN never stops, and no human already connected is ever disconnected by a licence state.
func (m *Manager) AllowsNewPrincipals(now time.Time) bool {
	switch m.Evaluate(now).State {
	case StateExpired, StateLapsed:
		return false
	default:
		return true
	}
}

// NewPrincipalRefusal is what an operator reads when grace has begun.
//
// ⭐ It says what still works before it says what does not, because the first thing an operator needs is
// to know their fleet is fine.
func (m *Manager) NewPrincipalRefusal(now time.Time) string {
	st := m.Evaluate(now)
	when := ""
	if !st.ExpiresAt.IsZero() {
		when = " on " + st.ExpiresAt.Format("2 January 2006")
	}
	return "This licence expired" + when + ". Everything already enrolled keeps working and nothing has " +
		"stopped — but new devices, agents and gateways cannot be enrolled until it is renewed."
}
