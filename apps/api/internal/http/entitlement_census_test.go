package http

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ⛔ THE ENTITLEMENT CENSUS — the replacement for what `test-editions` guarantees today.
//
// WHAT IS BEING LOST, STATED FIRST, BECAUSE IT IS THE REASON THIS FILE EXISTS:
//
//	`make test-editions` builds and tests the API with and without `-tags enterprise`, and what it proves is
//	a COMPILE-TIME property — paid code is ABSENT from the open binary. Not "the endpoint refuses"; the
//	function is not in the artifact. That is the strongest form of gating there is, and it is free: the
//	compiler enforces it and no test has to remember to.
//
//	⛔ S12.1 RETIRES IT PERMANENTLY. One binary, one artifact, every paid capability PRESENT and gated by a
//	runtime boolean. A boolean someone forgets to check is a capability given away silently, and the
//	compiler will never mention it.
//
// > **A COMPILE-TIME GUARANTEE FAILS LOUDLY AND FOR FREE. A RUNTIME ONE FAILS SILENTLY UNLESS SOMETHING
// > COUNTS IT. THIS IS THE SOMETHING.**
//
// ⛔ DERIVED FROM SOURCE, NEVER A HARDCODED LIST — and the reason is written in the sibling guard
// (edition_gate_order_test.go), which harvests its helper names for exactly this reason: a hardcoded list
// "silently stops covering a helper someone adds". A census whose INPUT is a literal cannot discover
// anything; it re-reports what its author already knew, forever, while the tree moves underneath it.
//
// So the INPUT is the tree. The DISPOSITION is the literal. Every capability seam found in source must
// appear in TIERS with a ruling, and every ruling must correspond to a seam that still exists. Set
// EQUALITY, both directions — a `>=` is satisfied forever by a lazy floor.
//
// PROVEN TO FIRE: adding a capability seam with no TIERS entry makes this test red, naming the file. See
// TestEntitlementCensusRejectsAnUndispositionedCapability, which plants one and asserts the census finds it.

// Tier is the licensing tier a capability belongs to under the S12.1 model
// (docs/S12.1-licensing-decisions.md, founder-ruled and locked 2026-08-06).
type Tier int

const (
	// Community — shipped to everyone, no licence key. ⭐ ADOPTION IS THE FIRST PRIORITY: the moat is
	// Community's generosity, not Enterprise's length. A capability here is a deliberate non-gate.
	Community Tier = iota
	// Enterprise — requires a valid licence key. FOUR, and only four.
	Enterprise
)

// disposition is a capability's tier plus the reason, so a future reader arguing to move it has to argue
// with the reason rather than rediscover it.
type disposition struct {
	tier   Tier
	reason string
}

// ⛔ TIERS IS THE ONE MAP — tier boundaries as DATA, not control flow.
//
// This is the shape S12.1's `license.Has(FeatSSO)` reads from, and the property that matters is that
// MOVING A FEATURE BETWEEN TIERS IS ONE LINE HERE. Today's boundary is control flow — 25 build-tagged
// files and 40 web `isEnterprise` call sites — which is why the current boundary cannot be revised, only
// rewritten.
//
// ⚠ THE KEYS ARE CAPABILITY SEAMS AS THEY EXIST TODAY (the `*_wire_open.go` split and the `enterprise.*`
// const reads). When S12.1 lands, the harvest re-points at `license.Has(Feat*)` call sites and this map
// keeps its meaning unchanged — the census asks the same question of a different seam.
var TIERS = map[string]disposition{
	// ── ENTERPRISE. Four gates, matching §2.1 of the paper exactly.
	"sso": {Enterprise, "SSO/OIDC (Google + Microsoft Entra). Shipped: internal/enterprise/sso/"},
	"idp_sync": {Enterprise, "IdP directory sync (Entra only; Google is roadmap, never sold). " +
		"⛔ ITS DOWNGRADE RELEASE IS UNDECIDED AND BLOCKS THE BUILD — docs/S12.1-D1-idpsync-release.md"},
	"organizations": {Enterprise, "1 org in Community. The mechanism exists (tenancy/service.go:73-82) but " +
		"reads a compile-time const; S12.1 makes it a runtime read. Enrolment-path only, never retroactive"},

	// ── COMMUNITY. Each of these is gated TODAY and must be UN-gated by S12.1.
	//
	// ⛔ THIS HALF OF THE MAP IS THE REAL FINDING. Six capabilities the model puts in Community are
	// `nil`/`false` in the open build right now, so Community is a NEW PRODUCT, not the open edition plus
	// a limit. Each line below is work, not a description.
	"policy": {Community, "The complete Zero Trust engine. Today policy_wire_open.go returns nil — the open " +
		"build has NO default-deny engine at all. ⛔ Un-gating relicenses 1,784 lines (41% of the " +
		"proprietary tree) from source-available to Apache-2.0, and that is a ONE-WAY DOOR"},
	"node_policy": {Community, "Policy push to gateways. Today nil — an engine that compiles and never " +
		"reaches a gateway would be worse than no engine, so this moves WITH policy or not at all"},
	"access_log":     {Community, "Access Events / flow logs. Zero-Trust visibility is not a paid add-on"},
	"device_health":  {Community, "Device posture. ⭐ Security does not sit behind a paywall"},
	"device_approval": {Community, "Device approval. ⚠ Its org-level on/off toggle does NOT EXIST " +
		"(DEFERRAL-REGISTER.md:105) — Community ships approve/reject/list and no switch"},
	"mfa_enforce": {Community, "Org-wide MFA enforcement AND admin reset. ⭐ Founder-reversed: security " +
		"does not sit behind a paywall. Its downgrade-release seam becomes dead code by design"},
}

var (
	// The capability seam as it exists today: one `<capability>_wire_open.go` per gated capability, whose
	// open-build stub returns nil/false. The basename IS the capability name — no second registry to drift.
	reWireOpen = regexp.MustCompile(`^(\w+)_wire_open\.go$`)
	// The org cap is the one gate with no wire file: it reads a build-tagged const directly.
	reEditionConst = regexp.MustCompile(`enterprise\.(Unlimited|MaxOrganizations)`)
)

// capabilitySeams walks the api tree and returns capability -> the file:line that proves it is a seam.
// The INPUT is the tree, so a capability nobody has written yet is still in scope.
func capabilitySeams(t *testing.T) map[string]string {
	t.Helper()
	root, err := filepath.Abs("..") // apps/api/internal
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if m := reWireOpen.FindStringSubmatch(d.Name()); m != nil {
			found[m[1]] = rel + ":1"
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for i, line := range strings.Split(string(body), "\n") {
			// A COMMENT MENTIONING THE CONST IS NOT A READ OF IT. Without this, prose about the edition
			// boundary would register as a capability seam and the census would report a gate nobody wrote.
			if code, _, _ := strings.Cut(line, "//"); reEditionConst.MatchString(code) {
				found["organizations"] = fmt.Sprintf("%s:%d", rel, i+1)
				return nil
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// TestEveryPaidCapabilityHasATierRuling — the census.
//
// ⛔ SET EQUALITY, BOTH DIRECTIONS. A one-sided check ("every seam is dispositioned") lets TIERS accumulate
// rulings for code that was deleted, and a stale ruling reads exactly like a live one. The other side
// ("every ruling has a seam") is what makes the map decay loudly.
func TestEveryPaidCapabilityHasATierRuling(t *testing.T) {
	seams := capabilitySeams(t)

	var undispositioned []string
	for cap, where := range seams {
		if _, ok := TIERS[cap]; !ok {
			undispositioned = append(undispositioned, fmt.Sprintf("  %s\t(%s)", cap, where))
		}
	}
	sort.Strings(undispositioned)
	if len(undispositioned) > 0 {
		t.Errorf("⛔ CAPABILITY SEAM WITH NO TIER RULING — a gate exists in code that the licensing model "+
			"does not mention, so nobody has decided whether customers get it:\n%s\n\n"+
			"Add it to TIERS with a reason, or remove the seam. A capability that is gated by accident is "+
			"given away or withheld by accident.", strings.Join(undispositioned, "\n"))
	}

	var stale []string
	for cap := range TIERS {
		if _, ok := seams[cap]; !ok {
			stale = append(stale, "  "+cap)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("⚠ TIER RULING FOR A SEAM THAT NO LONGER EXISTS — the map is describing code that is gone, "+
			"and a stale ruling is indistinguishable from a live one:\n%s", strings.Join(stale, "\n"))
	}
}

// TestTheFourGatesAreExactlyFour — the model's own headline, asserted rather than trusted.
//
// ⛔ THE PAPER SAYS "GATED — FOUR, AND ONLY FOUR". That sentence is the product decision; this is the only
// thing that makes it true tomorrow. Gateways is the fourth and has NO seam yet (it is new enforcement), so
// the count here is THREE until S12.1 builds it — stated as an exact expectation, not a floor, so the day
// the gateway gate lands this test fails and forces the number to be re-read rather than drifting.
func TestTheFourGatesAreExactlyFour(t *testing.T) {
	var enterprise []string
	for cap, d := range TIERS {
		if d.tier == Enterprise {
			enterprise = append(enterprise, cap)
		}
	}
	sort.Strings(enterprise)
	want := []string{"idp_sync", "organizations", "sso"}
	if strings.Join(enterprise, ",") != strings.Join(want, ",") {
		t.Errorf("the Enterprise tier is %v; expected %v.\n\n"+
			"THREE, not four: 'gateways' is the fourth gate and has no seam in code yet. When it lands, add "+
			"it here deliberately. If this failed because a capability MOVED tiers, that is a product "+
			"decision and docs/S12.1-licensing-decisions.md must move with it.", enterprise, want)
	}
}

// TestEntitlementCensusRejectsAnUndispositionedCapability — ⛔ THE CENSUS IS CENSUSED.
//
// > **A GUARD THAT HAS ONLY EVER PASSED IS INDISTINGUISHABLE FROM ONE THAT DOES NOTHING.**
//
// This plants a capability seam with no TIERS ruling, asserts the census NAMES IT BY FILE, and removes it.
// Without this, a walk() that silently returned early — a wrong root, a bad suffix test, a typo in the
// regex — would report a clean census over an empty input set forever, which is the failure mode this
// repo has already paid for three times (the vacuous-check class in docs/CUT-REGISTER.md).
func TestEntitlementCensusRejectsAnUndispositionedCapability(t *testing.T) {
	// A REAL FILE IN THE REAL TREE, because the thing under test is the walk. A fake in-memory input would
	// exercise the matcher and skip the traversal — and the traversal is where "silently covers nothing"
	// lives.
	planted := filepath.Join("..", "http", "zzcensusprobe_wire_open.go")
	body := "//go:build !enterprise\n\npackage http\n\n// Planted by the entitlement census self-test.\n"
	if err := os.WriteFile(planted, []byte(body), 0o600); err != nil {
		t.Fatalf("plant: %v", err)
	}
	defer os.Remove(planted)

	seams := capabilitySeams(t)
	where, seen := seams["zzcensusprobe"]
	if !seen {
		t.Fatal("⛔ THE CENSUS DID NOT SEE A PLANTED CAPABILITY SEAM. Every pass it has ever reported is " +
			"therefore worthless: it is not reading the tree it claims to read.")
	}
	if _, dispositioned := TIERS["zzcensusprobe"]; dispositioned {
		t.Fatal("the probe must NOT be in TIERS — it exists to be the undispositioned case")
	}
	if !strings.Contains(where, "zzcensusprobe_wire_open.go") {
		t.Errorf("the census must name the offender by FILE so it is actionable; got %q", where)
	}

	// And the negative half: with the plant removed, the census is clean again — so the failure above was
	// caused BY the plant and not by a pre-existing red that would have fired regardless.
	if err := os.Remove(planted); err != nil {
		t.Fatalf("unplant: %v", err)
	}
	if _, stillSeen := capabilitySeams(t)["zzcensusprobe"]; stillSeen {
		t.Error("the census reports a seam that no longer exists — it is not re-reading the tree")
	}
}
