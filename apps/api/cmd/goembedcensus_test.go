package cmd_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ⛔ EVERY `//go:embed` TARGET MUST BE GO-RELEVANT TO CI'S DIFF CLASSIFIER.
//
// THE SEAM THIS GUARDS, stated plainly because it is why the hole was invisible:
//
//	THE CLASSIFIER REASONS ABOUT FILE EXTENSIONS. THE COMPILER REASONS ABOUT COMPILE INPUTS.
//	`//go:embed` IS WHERE THOSE DISAGREE — it makes a file of ANY extension a build input to a Go package.
//
// S14.11 found `apps/api/cmd/seed-fixtures/fixtures.sql` matching NONE of the classifier's patterns, so a
// fixtures-only diff set `go=false` and skipped `build-editions` — the step that fails when an embed target
// is renamed or deleted, because a missing embed target is a COMPILE ERROR. In a classifier whose own header
// reads "fails closed — anything uncertain runs everything." Prose said one thing, behaviour did another, and
// nothing compared the two (docs/CUT-REGISTER.md → the prose-versus-behaviour class).
//
// ⛔ WHY A TEST AND NOT JUST A WIDER REGEX: a suffix list is a GUESS ABOUT THE FUTURE. Adding `\.sql$` closes
// today's hole and says nothing about tomorrow's `//go:embed banner.txt` or `//go:embed templates/*.tmpl`.
// This re-derives the embed set FROM SOURCE on every run, so the next embed either matches the pattern or
// breaks the build with an instruction.
//
// It lives in apps/api/cmd/ alongside `shipcensus_test.go`, which likewise reads outside the module (the api
// Dockerfile). `make test-editions` mounts the REPO ROOT precisely so these cross-surface guards can run.

var (
	reEmbed = regexp.MustCompile(`(?m)^\s*//go:embed\s+(.+)$`)
	// The classifier's own regex, transcribed. Kept as a literal on purpose: if someone edits ci.yml and not
	// this line, `TestClassifierPatternMatchesTheWorkflow` below fails and names the drift.
	classifierPattern = `(\.go$|go\.(mod|sum)$|\.sql$|Dockerfile|\.github/|openapi/|apps/api/db/)`
)

// repoRoot is three levels up from apps/api/cmd — the same hop shipcensus_test.go makes.
const repoRoot = "../../.."

func TestGoEmbedTargetsAreGoRelevantToCI(t *testing.T) {
	pat := regexp.MustCompile(classifierPattern)

	type embed struct{ dir, spec, path string }
	var found []embed

	err := filepath.Walk(repoRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // unreadable paths are not this guard's business
		}
		if info.IsDir() {
			switch info.Name() {
			case "node_modules", ".git", "vendor", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, m := range reEmbed.FindAllStringSubmatch(string(b), -1) {
			for _, spec := range strings.Fields(strings.TrimSpace(m[1])) {
				// `all:` / `//go:embed` prefixes are directives, not path text.
				spec = strings.TrimPrefix(spec, "all:")
				dir := filepath.Dir(p)
				// The path AS CI WOULD SEE IT: repo-relative, forward slashes, no leading "./".
				rel, rerr := filepath.Rel(repoRoot, filepath.Join(dir, spec))
				if rerr != nil {
					continue
				}
				found = append(found, embed{dir: dir, spec: spec, path: filepath.ToSlash(rel)})
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// ⛔ THE VACUITY FLOOR. Without it this passes the day the walk, the suffix filter, or the regex stops
	// matching — reporting "every embed is covered" about zero embeds. Measured at authorship: 2.
	if len(found) < 2 {
		t.Fatalf("found only %d //go:embed targets (expected >= 2) — the SCAN regressed, not the code", len(found))
	}

	for _, e := range found {
		if !pat.MatchString(e.path) {
			t.Errorf("//go:embed target is INVISIBLE to CI's diff classifier: %s\n"+
				"  (embedded by a Go package in %s, so it is a COMPILE INPUT — a diff touching only this\n"+
				"   file would set go=false and SKIP build-editions, which is the step a missing embed\n"+
				"   target would fail.)\n"+
				"  FIX: widen the Go-relevance regex in .github/workflows/ci.yml AND the transcription in\n"+
				"  this file, so both move together.", e.path, e.dir)
			continue
		}
		t.Logf("covered: %-52s (embedded by %s)", e.path, e.dir)
	}
}

// TestClassifierPatternMatchesTheWorkflow keeps the transcription above honest. Without it the two can drift
// and this guard would cheerfully validate embeds against a pattern CI no longer uses — a guard checking a
// copy of the thing instead of the thing.
func TestClassifierPatternMatchesTheWorkflow(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), classifierPattern) {
		t.Fatalf("the classifier regex in ci.yml no longer matches the copy in this file.\n"+
			"  this file expects: %s\n"+
			"  update BOTH, or the embed census validates against a pattern CI does not run.",
			classifierPattern)
	}
}
