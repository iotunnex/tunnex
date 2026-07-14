package accesslog

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func ev(org uuid.UUID, seq int64, d Decision) Event {
	return Event{ID: uuid.New(), Seq: seq, OrgID: org, Decision: d, SrcIP: "10.99.0.10", DstIP: "10.0.5.5", Protocol: "tcp"}
}

// Append writes decodable JSON lines that preserve seq/decision, and rotation at maxBytes
// closes the full segment with a manifest recording its line count + seq range.
func TestJSONLAppendAndRotate(t *testing.T) {
	dir := t.TempDir()
	// A tiny maxBytes so a few appends force a rotation.
	w, err := NewJSONLWriter(dir, 200)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	for i := int64(1); i <= 6; i++ {
		if err := w.Append(ev(org, i, DecisionDeny)); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// At least one segment rotated (a manifest exists) and the first segment's manifest
	// re-counts correctly against the file (VerifySegment green).
	manifests, _ := filepath.Glob(filepath.Join(dir, "*.manifest"))
	if len(manifests) == 0 {
		t.Fatal("expected at least one rotated segment with a manifest")
	}
	total := 0
	for _, mpath := range manifests {
		seg := mpath[:len(mpath)-len(".manifest")]
		if err := VerifySegment(seg); err != nil {
			t.Fatalf("verify %s: %v", filepath.Base(seg), err)
		}
		mb, _ := os.ReadFile(mpath)
		var m Manifest
		if err := json.Unmarshal(mb, &m); err != nil {
			t.Fatal(err)
		}
		total += m.Lines
		if m.FirstSeq == 0 || m.LastSeq < m.FirstSeq {
			t.Fatalf("manifest seq range invalid: %+v", m)
		}
	}
	// The final still-open segment was closed by Close(); its manifest is included above.
	if total != 6 {
		t.Fatalf("manifests account for %d lines, want 6", total)
	}
}

// A truncated segment (a line chopped off after close) is DETECTED against its manifest.
func TestVerifySegmentCatchesTruncation(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewJSONLWriter(dir, 1<<30) // no rotation
	org := uuid.New()
	for i := int64(1); i <= 4; i++ {
		_ = w.Append(ev(org, i, DecisionAllow))
	}
	_ = w.Close()

	seg := filepath.Join(dir, "access-000001.jsonl")
	if err := VerifySegment(seg); err != nil {
		t.Fatalf("pristine segment must verify: %v", err)
	}
	// Truncate to 2 lines and re-verify → must fail.
	lines := readLines(t, seg)
	if err := os.WriteFile(seg, []byte(lines[0]+"\n"+lines[1]+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := VerifySegment(seg); err == nil {
		t.Fatal("a truncated segment MUST fail verification (tamper-evidence)")
	}
}

// A hole in the per-org seq run is reported (a gap in the audit trail is legible).
func TestScanSeqGaps(t *testing.T) {
	orgA, orgB := uuid.New(), uuid.New()
	events := []Event{
		ev(orgA, 1, DecisionAllow), ev(orgA, 2, DecisionAllow), ev(orgA, 5, DecisionDeny), // A: missing 3,4
		ev(orgB, 10, DecisionAllow), ev(orgB, 11, DecisionAllow), // B: contiguous
	}
	gaps := ScanSeqGaps(events)
	if got := gaps[orgA.String()]; len(got) != 2 || got[0] != 3 || got[1] != 4 {
		t.Fatalf("orgA gaps = %v, want [3 4]", got)
	}
	if got := gaps[orgB.String()]; len(got) != 0 {
		t.Fatalf("orgB must have no gaps, got %v", got)
	}
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}
