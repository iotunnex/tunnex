package accesslog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeSink is an injectable segment file. failWrite makes Write (and Sync) fail — the bufio
// POISONED mode; failSync makes ONLY Sync fail (Write ok) — the SYNC-DEGRADED mode. name lets
// a test give two sinks distinct segment files.
type fakeSink struct {
	written   bytes.Buffer
	failWrite bool
	failSync  bool
	name      string
}

func (f *fakeSink) Write(p []byte) (int, error) {
	if f.failWrite {
		return 0, errors.New("sink write failed")
	}
	return f.written.Write(p)
}
func (f *fakeSink) Sync() error {
	if f.failWrite || f.failSync {
		return errors.New("sink sync failed")
	}
	return nil
}
func (f *fakeSink) Close() error { return nil }
func (f *fakeSink) Name() string {
	if f.name != "" {
		return f.name
	}
	return "fake-segment.jsonl"
}

// SYNC-DEGRADED (fold-3 #1): a Sync-ONLY failure must NOT poison the writer — the lines are on
// disk (bufio flushed), only the fsync failed. The segment is KEPT (not abandoned/reopened, so
// its tamper-evidence survives) and the next Flush retries the Sync; the error still surfaces
// so Health degrades.
func TestJSONLSyncFailureKeepsSegmentNoReopen(t *testing.T) {
	dir := t.TempDir()
	degraded := &fakeSink{failSync: true, name: "seg-a.jsonl"}
	fresh := &fakeSink{name: "seg-b.jsonl"}
	jw := &JSONLWriter{
		dir: dir, maxBytes: 1 << 30, now: time.Now,
		openFn: func(string) (segmentSink, error) { return fresh, nil }, // a reopen would switch to `fresh`
	}
	jw.f, jw.w = degraded, bufio.NewWriter(degraded)

	if err := jw.Append(Event{Seq: 1, OrgID: uuid.New(), Decision: DecisionAllow}); err != nil {
		t.Fatal(err)
	}
	if err := jw.Flush(); err == nil {
		t.Fatal("a Sync failure must surface an error so Health degrades")
	}
	if jw.broken {
		t.Fatal("a Sync-ONLY failure must NOT poison the writer (segment must be kept, not abandoned)")
	}
	if degraded.written.Len() == 0 {
		t.Fatal("the line must be on disk (bufio flushed) despite the Sync failure")
	}
	if jw.f != segmentSink(degraded) {
		t.Fatal("Sync failure must NOT reopen a new segment (must keep the same one)")
	}
	if fresh.written.Len() != 0 {
		t.Fatal("no reopen — the fresh segment must be untouched")
	}
}

// POISONED on the ROTATION path (fold-3 #2): a bufio.Flush failure during rotate()/closeSegment
// must set `broken` so the next Append self-heals — else it would write into the still-poisoned
// bufio and drop a committed event before healing.
func TestJSONLRotateFlushFailurePoisons(t *testing.T) {
	dir := t.TempDir()
	fresh := &fakeSink{name: "seg-b.jsonl"}
	poison := &fakeSink{failWrite: true, name: "seg-a.jsonl"}
	jw := &JSONLWriter{
		dir: dir, maxBytes: 1, now: time.Now, // maxBytes=1 → the first append triggers rotate
		openFn: func(string) (segmentSink, error) { return fresh, nil },
	}
	jw.f, jw.w = poison, bufio.NewWriter(poison)

	// Append buffers a line (> maxBytes) → rotate() → closeSegment() → bufio.Flush → poison.Write fails.
	if err := jw.Append(Event{Seq: 1, OrgID: uuid.New(), Decision: DecisionAllow}); err == nil {
		t.Fatal("a rotate/closeSegment flush to a failing sink must error")
	}
	if !jw.broken {
		t.Fatal("a rotate/closeSegment flush failure must POISON the writer (fold-3 #2)")
	}
	// The next Append must reopen (self-heal), not ride the poisoned bufio.
	if err := jw.Append(Event{Seq: 2, OrgID: uuid.New(), Decision: DecisionAllow}); err != nil {
		t.Fatalf("next Append must reopen after a rotate-flush poison, got %v", err)
	}
	if jw.broken {
		t.Fatal("writer must self-heal after reopen")
	}
	if fresh.written.Len() == 0 {
		_ = jw.Flush()
		if fresh.written.Len() == 0 {
			t.Fatal("recovered write must land in the fresh segment")
		}
	}
	// The poisoned segment must be marked ABANDONED (legible, distinct from tampered).
	if _, err := os.Stat(filepath.Join(dir, "seg-a.jsonl"+abandonedExt)); err != nil {
		t.Fatalf("poisoned segment must be marked abandoned: %v", err)
	}
}

// A TRANSIENT flush failure must not kill the stream for the process lifetime (fold-2 #1):
// the bufio error is sticky, so the writer must REOPEN a fresh segment on the next Append and
// resume recording — the "cleared on next success" recovery must actually be reachable, not a
// permanently-degraded-green lie.
func TestJSONLSelfHealsAfterTransientFlushError(t *testing.T) {
	dir := t.TempDir()
	good := &fakeSink{}
	jw := &JSONLWriter{
		dir: dir, maxBytes: 1 << 30, now: time.Now,
		openFn: func(string) (segmentSink, error) { return good, nil },
	}
	// Start on a POISONED segment: a sink whose write/flush fails.
	bad := &fakeSink{failWrite: true}
	jw.f, jw.w = bad, bufio.NewWriter(bad)

	_ = jw.Append(Event{Seq: 1, OrgID: uuid.New(), Decision: DecisionAllow}) // buffers
	if err := jw.Flush(); err == nil {
		t.Fatal("flush to a failing sink must error (poisons the bufio writer)")
	}
	if !jw.broken {
		t.Fatal("a flush failure must mark the writer broken")
	}

	// RECOVERY: the next Append must reopen a fresh (good) segment and record durably —
	// NOT stay dead on the sticky bufio error.
	if err := jw.Append(Event{Seq: 2, OrgID: uuid.New(), Decision: DecisionAllow}); err != nil {
		t.Fatalf("Append after a transient failure must self-heal, got %v", err)
	}
	if err := jw.Flush(); err != nil {
		t.Fatalf("Flush after self-heal must succeed, got %v", err)
	}
	if jw.broken {
		t.Fatal("writer must no longer be broken after a successful reopen+write")
	}
	if good.written.Len() == 0 {
		t.Fatal("after self-heal the fresh segment must receive the recovered write (stream resumed)")
	}
}

// The abandoned-segment RULE (fold-3): an abandoned segment (poisoned, marker present, no
// manifest) verifies LOUD as ErrSegmentAbandoned — DISTINCT from a tampered/truncated closed
// segment — and the export EXCLUDES it (its committed events stay in PG), never silently
// riding along as unverifiable.
func TestAbandonedSegmentIsUnverifiableAndExcludedFromExport(t *testing.T) {
	dir := t.TempDir()
	org := uuid.New()
	jw, err := NewJSONLWriter(dir, 1<<30) // real writer → access-000001.jsonl (the open segment)
	if err != nil {
		t.Fatal(err)
	}
	if err := jw.Append(Event{Seq: 1, OrgID: org, Decision: DecisionAllow}); err != nil {
		t.Fatal(err)
	}
	if err := jw.Flush(); err != nil {
		t.Fatal(err)
	}
	// An abandoned segment: a real line + the .abandoned marker, no manifest.
	abPath := filepath.Join(dir, "access-000099.jsonl")
	if err := os.WriteFile(abPath, []byte(`{"org_id":"`+org.String()+`","seq":2}`+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abPath+abandonedExt, []byte("abandoned\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := VerifySegment(abPath); !errors.Is(err, ErrSegmentAbandoned) {
		t.Fatalf("an abandoned segment must verify as ErrSegmentAbandoned (≠ tampered), got %v", err)
	}
	var buf bytes.Buffer
	if err := ExportOrg(dir, org, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"seq":2`) {
		t.Fatalf("export must EXCLUDE the abandoned segment's lines; got:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), `"seq":1`) {
		t.Fatalf("export must include the clean segment's line; got:\n%s", buf.String())
	}
}

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
