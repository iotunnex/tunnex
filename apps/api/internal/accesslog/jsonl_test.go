package accesslog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A batch's lines must be DURABLE + readable after Flush, WITHOUT waiting for rotation or
// Close. The box-walk found the writer buffered lines in a bufio.Writer and flushed only on
// rotation/Close — so a reader/export (and a graceful SIGTERM shutdown) saw an empty segment
// while PG already held the committed rows (the source-of-truth silently diverging). Flush
// (bufio -> OS -> fsync) makes the open segment durable + readable immediately.
func TestJSONLFlushMakesOpenSegmentDurable(t *testing.T) {
	dir := t.TempDir()
	jw, err := NewJSONLWriter(dir, 1<<30) // huge maxBytes: no rotation, so ONLY Flush can persist
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	for i := 0; i < 3; i++ {
		if err := jw.Append(Event{OrgID: org, Seq: int64(i + 1), Decision: DecisionAllow}); err != nil {
			t.Fatal(err)
		}
	}
	if err := jw.Flush(); err != nil {
		t.Fatal(err)
	}
	// Read the OPEN segment back from disk via a fresh reader — no Close/rotation happened.
	var out bytes.Buffer
	if err := ExportOrg(dir, org, &out); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(out.String(), "\n"); n != 3 {
		t.Fatalf("after Flush the open segment must expose all 3 committed lines on disk, got %d:\n%s", n, out.String())
	}
}

// ExportOrg copies an org's lines VERBATIM (a reader, never a re-serializer): the export is
// byte-identical to the org's stored lines, isolated (no foreign org leaks), and the
// per-line seq tamper-evidence survives.
func TestExportOrgIsVerbatimAndIsolated(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewJSONLWriter(dir, 1<<30) // one segment
	orgA, orgB := uuid.New(), uuid.New()
	for _, e := range []Event{ev(orgA, 1, DecisionDeny), ev(orgB, 1, DecisionAllow), ev(orgA, 2, DecisionAllow)} {
		if err := w.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	// The ORIGINAL orgA lines, byte-for-byte, filtered from the raw segment.
	raw, _ := os.ReadFile(filepath.Join(dir, "access-000001.jsonl"))
	var wantLines []string
	for _, ln := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var p struct {
			OrgID uuid.UUID `json:"org_id"`
		}
		_ = json.Unmarshal([]byte(ln), &p)
		if p.OrgID == orgA {
			wantLines = append(wantLines, ln)
		}
	}
	want := strings.Join(wantLines, "\n") + "\n"

	var buf bytes.Buffer
	if err := ExportOrg(dir, orgA, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != want {
		t.Fatalf("export not byte-identical:\n got  %q\n want %q", buf.String(), want)
	}
	if strings.Contains(buf.String(), orgB.String()) {
		t.Fatal("export leaked a foreign org's line (isolation broken)")
	}
	if !strings.Contains(buf.String(), `"seq":1`) || !strings.Contains(buf.String(), `"seq":2`) {
		t.Fatalf("export must preserve per-line seq (tamper-evidence): %s", buf.String())
	}
}

// A gap event serializes with decision "gap" — unambiguous to a JSONL/SIEM parser, never a
// deny lookalike (report line b). A parser keying on `decision` recovers it as a gap
// carrying the dropped count.
func TestGapEventJSONLIsUnambiguous(t *testing.T) {
	gap := Event{ID: uuid.New(), Seq: 9, OrgID: uuid.New(), Decision: DecisionGap, DenyCount: 7, OccurredAt: time.Unix(0, 0).UTC()}
	b, err := json.Marshal(gap)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	if !strings.Contains(s, `"decision":"gap"`) {
		t.Fatalf("gap line must carry decision=gap: %s", s)
	}
	if strings.Contains(s, `"decision":"deny"`) {
		t.Fatalf("a gap must not look like a deny: %s", s)
	}
	var back Event
	if err := json.Unmarshal(b, &back); err != nil || back.Decision != DecisionGap || back.DenyCount != 7 {
		t.Fatalf("gap round-trip: %+v err=%v", back, err)
	}
}

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
