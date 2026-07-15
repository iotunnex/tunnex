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

func ev(org uuid.UUID, seq int64, d Decision) Event {
	return Event{ID: uuid.New(), Seq: seq, OrgID: org, Decision: d, SrcIP: "10.99.0.10", DstIP: "10.0.5.5", Protocol: "tcp"}
}

// WriteBatch is DURABLE ON RETURN (open -> write -> fsync -> close): a fresh reader sees every
// line WITHOUT any flush/Close — the stateless writer has no buffered-in-memory tail (the
// reduce after the buffered writer's four durability-defect rounds).
func TestJSONLWriteBatchDurable(t *testing.T) {
	dir := t.TempDir()
	w, err := NewJSONLWriter(dir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	if err := w.WriteBatch([]Event{ev(org, 1, DecisionAllow), ev(org, 2, DecisionAllow), ev(org, 3, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ExportOrg(dir, org, &buf); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 3 {
		t.Fatalf("WriteBatch must be durable-on-return: got %d lines\n%s", n, buf.String())
	}
}

// Roll manifest consistency (amendment b): the segment is fsync'd on every WriteBatch BEFORE
// the manifest is written on roll, so the manifest can never claim MORE lines than are on disk
// — the false-TRUNCATED class is impossible by construction.
func TestJSONLRollManifestConsistent(t *testing.T) {
	dir := t.TempDir()
	w, err := NewJSONLWriter(dir, 200) // tiny cap → rolls after a few small batches
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	for i := int64(1); i <= 6; i++ {
		if err := w.WriteBatch([]Event{ev(org, i, DecisionDeny)}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	manifests, _ := filepath.Glob(filepath.Join(dir, "*.manifest"))
	if len(manifests) == 0 {
		t.Fatal("expected at least one sealed segment with a manifest")
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
		if got := len(readLines(t, seg)); m.Lines != got {
			t.Fatalf("manifest Lines=%d but %d on disk (a manifest must never over/under-count): %s", m.Lines, got, filepath.Base(seg))
		}
		if m.FirstSeq == 0 || m.LastSeq < m.FirstSeq {
			t.Fatalf("manifest seq range invalid: %+v", m)
		}
		total += m.Lines
	}
	if total != 6 {
		t.Fatalf("manifests account for %d lines, want 6", total)
	}
}

// Torn-tail rule, MID-RUN (amendment a+d): a prior batch that failed mid-write left a partial
// line; the next WriteBatch truncates it at a clean boundary before appending — so the torn
// fragment is dropped (its event is in PG → a legible seq gap) and NEVER merges with / corrupts
// the following line. No poisoned state: the next batch just works.
func TestJSONLTornTailTruncatedBeforeAppend(t *testing.T) {
	dir := t.TempDir()
	w, err := NewJSONLWriter(dir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	if err := w.WriteBatch([]Event{ev(org, 1, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	// Simulate a batch that failed mid-write: a torn partial appended, writer marked dirty.
	seg := filepath.Join(dir, "access-000001.jsonl")
	f, _ := os.OpenFile(seg, os.O_WRONLY|os.O_APPEND, 0o640)
	_, _ = f.WriteString(`{"org_id":"` + org.String() + `","seq":2,PARTIAL`) // no newline, invalid JSON
	_ = f.Close()

	// Every WriteBatch truncates a torn tail before appending — no dirty flag needed.
	if err := w.WriteBatch([]Event{ev(org, 3, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ExportOrg(dir, org, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "PARTIAL") {
		t.Fatalf("the torn partial must be truncated, not merged/exported: %s", out)
	}
	if !strings.Contains(out, `"seq":1`) || !strings.Contains(out, `"seq":3`) {
		t.Fatalf("seq 1 and 3 must both survive (the torn seq 2 dropped cleanly): %s", out)
	}
}

// Torn-tail rule, ON RESTART (amendment d): a crash leaves the active (unsealed) segment with a
// torn tail; resume() truncates it and re-derives the accounting, then continues the SAME
// segment. The torn fragment never survives and never corrupts the next append. (Also proves
// the writer does NOT O_TRUNC an existing segment on open — the old restart-truncate bug.)
func TestJSONLResumeAfterCrashTruncatesTorn(t *testing.T) {
	dir := t.TempDir()
	org := uuid.New()
	w1, err := NewJSONLWriter(dir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if err := w1.WriteBatch([]Event{ev(org, 1, DecisionAllow), ev(org, 2, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	// Crash: no Close(). A partially-written batch left a torn tail.
	seg := filepath.Join(dir, "access-000001.jsonl")
	f, _ := os.OpenFile(seg, os.O_WRONLY|os.O_APPEND, 0o640)
	_, _ = f.WriteString(`{"seq":3,TORN`)
	_ = f.Close()

	w2, err := NewJSONLWriter(dir, 1<<30) // restart → resume
	if err != nil {
		t.Fatal(err)
	}
	if w2.seg != 1 {
		t.Fatalf("resume must continue the active segment 1, got %d", w2.seg)
	}
	if err := w2.WriteBatch([]Event{ev(org, 4, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := ExportOrg(dir, org, &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "TORN") {
		t.Fatalf("torn tail must be truncated on resume: %s", out)
	}
	for _, s := range []string{`"seq":1`, `"seq":2`, `"seq":4`} {
		if !strings.Contains(out, s) {
			t.Fatalf("missing %s after resume (durable lines must survive): %s", s, out)
		}
	}
	// Seal + verify: the manifest is DERIVED from disk, so it counts exactly the 3 durable lines.
	if err := w2.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifySegment(filepath.Join(dir, "access-000001.jsonl")); err != nil {
		t.Fatalf("sealed segment must verify (manifest derived from disk == on-disk lines): %v", err)
	}
}

// Derive-from-disk (the fix for the re-earned review's root cause): complete lines that a
// failed batch left DURABLE on disk but that the writer never "accounted" are still counted at
// seal — the manifest is scanned from disk, so it can never UNDER-count and raise a false
// TRUNCATED. (The old design tracked in-memory counters that drifted from disk.)
func TestJSONLSealCountsAllDurableLinesNoFalseTruncate(t *testing.T) {
	dir := t.TempDir()
	org := uuid.New()
	w, err := NewJSONLWriter(dir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a batch whose WriteBatch failed at Sync/Close but whose 2 complete lines are
	// durable on disk (write succeeded), with NO writer-side accounting for them.
	seg := filepath.Join(dir, "access-000001.jsonl")
	b1, _ := json.Marshal(ev(org, 1, DecisionAllow))
	b2, _ := json.Marshal(ev(org, 2, DecisionAllow))
	if err := os.WriteFile(seg, append(append(append(b1, '\n'), b2...), '\n'), 0o640); err != nil {
		t.Fatal(err)
	}
	// A normal batch appends a 3rd line, then seal on Close.
	if err := w.WriteBatch([]Event{ev(org, 3, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := VerifySegment(seg); err != nil {
		t.Fatalf("derive-from-disk must count ALL durable lines (no false TRUNCATED): %v", err)
	}
	mb, _ := os.ReadFile(seg + ".manifest")
	var m Manifest
	_ = json.Unmarshal(mb, &m)
	if m.Lines != 3 {
		t.Fatalf("manifest must count all 3 on-disk lines, got %d", m.Lines)
	}
}

// A roll's manifest-write failure is a DURABLE batch with a deferred seal — Health notes it
// (jsonl_seal_deferred), NEVER as a write failure / lost batch; a later success clears it.
func TestHealthSealDeferredIsNotAFailure(t *testing.T) {
	h := NewHealth()
	h.jsonlSealDeferredSet()
	s := h.Snapshot()
	if !s.JSONLSealDeferred {
		t.Fatal("a deferred seal must be noted on Health")
	}
	if s.JSONLDegraded || s.JSONLFailures != 0 {
		t.Fatalf("a deferred seal must NOT read as a write failure / data loss: %+v", s)
	}
	h.jsonlRecovered()
	if h.Snapshot().JSONLSealDeferred {
		t.Fatal("a fully successful batch must clear the deferred-seal note")
	}
}

// After a cleanly SEALED segment (manifest present), resume starts a FRESH next segment and
// never touches the sealed one.
func TestJSONLResumeSealedStartsFresh(t *testing.T) {
	dir := t.TempDir()
	org := uuid.New()
	w1, err := NewJSONLWriter(dir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if err := w1.WriteBatch([]Event{ev(org, 1, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	if err := w1.Close(); err != nil { // seals access-000001 with a manifest
		t.Fatal(err)
	}
	w2, err := NewJSONLWriter(dir, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if w2.seg != 2 {
		t.Fatalf("after a sealed segment resume must start fresh at seg 2, got %d", w2.seg)
	}
	if err := w2.WriteBatch([]Event{ev(org, 2, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "access-000002.jsonl")); err != nil {
		t.Fatalf("the fresh segment access-000002 must exist: %v", err)
	}
	if err := VerifySegment(filepath.Join(dir, "access-000001.jsonl")); err != nil {
		t.Fatalf("the sealed segment 1 must remain valid (untouched): %v", err)
	}
}

// ExportOrg copies an org's lines VERBATIM (a reader, never a re-serializer): byte-identical to
// the org's stored lines, isolated (no foreign org leaks), per-line seq preserved.
func TestExportOrgIsVerbatimAndIsolated(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewJSONLWriter(dir, 1<<30)
	orgA, orgB := uuid.New(), uuid.New()
	if err := w.WriteBatch([]Event{ev(orgA, 1, DecisionDeny), ev(orgB, 1, DecisionAllow), ev(orgA, 2, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

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

// A gap event serializes with decision "gap" — unambiguous to a JSONL/SIEM parser.
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

// A truncated SEALED segment (a line chopped off after seal) is DETECTED against its manifest —
// tamper-evidence. (A sealed segment never contains a torn tail, so a mismatch is real tamper.)
func TestVerifySegmentCatchesTruncation(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewJSONLWriter(dir, 1<<30)
	org := uuid.New()
	if err := w.WriteBatch([]Event{ev(org, 1, DecisionAllow), ev(org, 2, DecisionAllow), ev(org, 3, DecisionAllow), ev(org, 4, DecisionAllow)}); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	seg := filepath.Join(dir, "access-000001.jsonl")
	if err := VerifySegment(seg); err != nil {
		t.Fatalf("pristine sealed segment must verify: %v", err)
	}
	lines := readLines(t, seg)
	if err := os.WriteFile(seg, []byte(lines[0]+"\n"+lines[1]+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := VerifySegment(seg); err == nil {
		t.Fatal("a truncated sealed segment MUST fail verification (tamper-evidence)")
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
