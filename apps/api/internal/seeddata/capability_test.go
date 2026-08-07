package seeddata

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔ THE SEED MUST STATE `cp_admin` FOR EVERY ACCOUNT IT CREATES — and this test reads the SEED,
// not a database.
//
// ⚠ THAT CHOICE IS THE WHOLE POINT, AND IT IS WHY THE DEFECT SURVIVED. Migration 0073 backfills the
// capability for existing owners. On a developer's rig — which already had data when the migration ran —
// the demo owner is granted retroactively and everything works. On a FRESH install the order is
// migrate → seed: the backfill matches no rows (there are no users yet), the seed inserts the demo owner
// at the column DEFAULT of false, and that owner cannot create an organization, sees no "+ New" in the
// switcher, and cannot run the lifecycle walk.
//
// > ## ⛔ **A MIGRATION THAT GRANDFATHERS EXISTING ROWS MAKES THE SEED'S OMISSION INVISIBLE ON EVERY RIG
// > ## THAT ALREADY HAD DATA. THE DEVELOPER'S PASSES; EVERY FRESH ONE FAILS.**
//
// A test that queried a database would have passed on the machine where this was written — which is
// exactly what happened when a human checked. Reading the source is the only version that answers for a
// deployment nobody has run yet.

func seedSources(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range []string{"seed", "seed-enterprise", "walk-bootstrap"} {
		p := filepath.Join("..", "..", "cmd", rel, "main.go")
		b, err := os.ReadFile(p)
		if err != nil {
			continue // not every tree carries every seeder
		}
		out[rel] = string(b)
	}
	// ⛔ VACUITY FLOOR. A census over zero seeders reports a clean bill of health forever.
	if len(out) == 0 {
		t.Fatal("⛔ no seeder sources found — this guard is checking nothing")
	}
	return out
}

var upsertUser = regexp.MustCompile(`UpsertUserParams\{`)

// ⛔ EVERY UpsertUser CALL STATES THE CAPABILITY. Silence means the column default, and the default is
// invisible on any rig where the migration already ran.
func TestSeedStatesOrgCreationCapability(t *testing.T) {
	for name, src := range seedSources(t) {
		calls := strings.Count(src, "UpsertUserParams{")
		if calls == 0 {
			continue
		}
		stated := strings.Count(src, "CpAdmin:")
		if stated < calls {
			t.Errorf("⛔ cmd/%s creates %d users and states cp_admin for only %d.\n\n"+
				"The unstated ones take the column DEFAULT (false). On YOUR rig migration 0073 has "+
				"already backfilled the capability for existing owners, so this looks fine — on a FRESH "+
				"install the backfill matches nothing and that account cannot create an organization.\n\n"+
				"State it explicitly for every seeded user, including the ones that must NOT have it.",
				name, calls, stated)
		}
	}
}

// ⭐ THE FIXTURE MUST STAY REPRESENTATIVE: one account that may create, several that may not.
//
// ⚠ A seed where everybody holds the capability tests nothing — the sixth leg of the signup boundary
// (a member of an existing org is REFUSED) has no fixture to demonstrate it against, and a reviewer
// clicking through sees an affordance every account has, which is not the shape a real deployment has.
func TestSeedGrantsTheCapabilityToExactlyOneAccount(t *testing.T) {
	src, ok := seedSources(t)["seed"]
	if !ok {
		t.Skip("cmd/seed not present")
	}
	granted := strings.Count(src, "CpAdmin: true")
	if granted != 1 {
		t.Errorf("⛔ cmd/seed grants org creation to %d accounts, want exactly 1 (the demo owner).\n\n"+
			"Every account holding it makes the fixture unrepresentative: signup creates an account and "+
			"never an organization, so the MAJORITY case a real deployment produces is an account that "+
			"may NOT create — and the seed has to model that or the boundary has nothing to be seen "+
			"against.", granted)
	}
	// ⛔ AND THE NO-ORG FIXTURE MUST NOT HOLD IT. DemoNoOrgEmail exists to model an account that has not
	// been admitted — which is what onboarding.spec.ts drives to the invitation card. Granting it would
	// turn the funnel fixture into a second creator and delete the state it exists to represent.
	i := strings.Index(src, "DemoNoOrgEmail")
	if i < 0 {
		t.Skip("no no-org fixture in this seeder")
	}
	window := src[i:min(i+900, len(src))]
	if strings.Contains(window, "CpAdmin: true") {
		t.Error("⛔ the no-org onboarding fixture was granted org creation — it exists to model an " +
			"account that has NOT been admitted, and that is the state signup now produces for everyone")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
