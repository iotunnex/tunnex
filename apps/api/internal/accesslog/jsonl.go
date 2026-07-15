package accesslog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// JSONLWriter appends access events as JSON lines to a rotating stream on the customer's disk
// — the SIEM source-of-truth + retention record (D4/D5). Tamper-evidence MINIMUM (D4): every
// line carries the per-org monotonic `seq` (assigned at ingest), and every rotated segment
// gets a sidecar MANIFEST recording its line count + seq range + close time, so a DELETED or
// TRUNCATED segment is detectable.
//
// STATELESS-PER-BATCH design (the reduce after four review rounds on the old buffered/
// self-healing/abandoned-marker writer kept trading one durability defect for another): the
// writer holds NO open file handle and NO bufio between batches. WriteBatch opens the current
// segment O_APPEND, writes the WHOLE batch in one Write, fsyncs, and closes — so every recorded
// batch is durable on return and there is no long-lived mutable failure state to corrupt. A
// failed batch simply returns an error (the ingest marks Health + the per-line seq leaves a
// detectable hole — the events are still in PG); the NEXT batch starts fresh. A crash or a
// failed write can leave a partial trailing line; it is truncated at a clean line boundary
// before the next append (on restart via resume(), or mid-run via the `dirty` flag), so a torn
// tail never corrupts a following line and never survives into a SEALED segment.
//
// Not safe for concurrent use — the ingest owns one writer, serialized by the Ingester mutex.
type JSONLWriter struct {
	dir      string
	maxBytes int64
	now      func() time.Time
	// Accounting for the CURRENT (open, not-yet-rolled) segment — used to write its manifest on
	// roll/Close. Advanced ONLY after a batch's durable write succeeds, so a manifest can never
	// claim more lines than are fsync'd on disk (no false TRUNCATED).
	seg      int
	lines    int
	bytes    int64
	firstSeq int64
	lastSeq  int64
	// dirty = the previous WriteBatch failed mid-write and may have left a torn partial line;
	// the next WriteBatch truncates it before appending (clean boundary). Self-clears on success.
	dirty bool
}

// Manifest is the sidecar written next to each SEALED segment (segment.jsonl.manifest). A
// verifier re-counts the segment's lines against Lines to catch truncation.
type Manifest struct {
	File     string    `json:"file"`
	FirstSeq int64     `json:"first_seq"`
	LastSeq  int64     `json:"last_seq"`
	Lines    int       `json:"lines"`
	Bytes    int64     `json:"bytes"`
	ClosedAt time.Time `json:"closed_at"`
}

// NewJSONLWriter opens a stream under dir (created if absent), RESUMING the active segment a
// prior run left (crash-safe — no O_TRUNC of existing data). maxBytes<=0 uses DefaultJSONLMaxBytes.
func NewJSONLWriter(dir string, maxBytes int64) (*JSONLWriter, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultJSONLMaxBytes
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	w := &JSONLWriter{dir: dir, maxBytes: maxBytes, now: time.Now}
	if err := w.resume(); err != nil {
		return nil, err
	}
	return w, nil
}

func segmentName(seg int) string { return fmt.Sprintf("access-%06d.jsonl", seg) }

func segNumber(path string) int {
	b := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "access-"), ".jsonl")
	n, _ := strconv.Atoi(b)
	return n
}

// resume picks the segment to continue: the highest-numbered segment WITHOUT a manifest is the
// still-open active one — truncate any torn tail a crash left, then re-derive its line/seq/byte
// accounting by scanning it. A SEALED (has-manifest) highest segment means the last run closed
// cleanly → start a fresh segment after it. No segments → start at 1.
func (w *JSONLWriter) resume() error {
	segs, err := filepath.Glob(filepath.Join(w.dir, "access-*.jsonl"))
	if err != nil {
		return err
	}
	sort.Strings(segs)
	if len(segs) == 0 {
		w.seg = 1
		return nil
	}
	last := segs[len(segs)-1]
	n := segNumber(last)
	if _, err := os.Stat(last + ".manifest"); err == nil {
		w.seg = n + 1 // sealed → continue on a fresh segment
		return nil
	}
	w.seg = n // active, unsealed → truncate any torn tail + re-derive accounting
	if err := truncateTornTail(last); err != nil {
		return err
	}
	return w.deriveAccounting(last)
}

// deriveAccounting rebuilds the current-segment counters from a (torn-tail-truncated) file.
func (w *JSONLWriter) deriveAccounting(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		if w.lines == 0 {
			w.firstSeq = probe.Seq
		}
		w.lines++
		w.lastSeq = probe.Seq
		w.bytes += int64(len(line)) + 1
	}
	return sc.Err()
}

// WriteBatch appends a whole batch as JSON lines: build the bytes, open O_APPEND, write, FSYNC,
// close — so on a nil return every line is durable on disk. On any failure it returns the error
// WITHOUT advancing the accounting and marks the segment dirty (the next batch truncates the
// torn tail). The caller (ingest) treats a failure as best-effort: PG keeps the events and the
// per-line seq leaves a detectable hole. Rolls to the next segment when the size cap is reached.
func (w *JSONLWriter) WriteBatch(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	path := filepath.Join(w.dir, segmentName(w.seg))
	if w.dirty {
		// A prior batch failed mid-write; drop any torn partial so this batch appends at a clean
		// line boundary. The failed batch's events are in PG (a legible seq gap); the in-memory
		// accounting already excludes them, so no re-derive is needed.
		if err := truncateTornTail(path); err != nil {
			return err
		}
		w.dirty = false
	}
	var buf bytes.Buffer
	for i := range events {
		b, err := json.Marshal(events[i])
		if err != nil {
			return err // marshal can't leave a torn file (nothing written yet)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		w.dirty = true
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		w.dirty = true
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		w.dirty = true
		return err
	}
	// Durable — advance the accounting (never before the fsync, so a manifest can't over-claim).
	if w.lines == 0 {
		w.firstSeq = events[0].Seq
	}
	w.lines += len(events)
	w.lastSeq = events[len(events)-1].Seq
	w.bytes += int64(buf.Len())
	if w.bytes >= w.maxBytes {
		return w.roll()
	}
	return nil
}

// roll seals the current segment (writes its manifest — the segment is already fully fsync'd)
// and advances to the next segment number.
func (w *JSONLWriter) roll() error {
	if err := w.writeManifest(w.seg); err != nil {
		return err
	}
	w.seg++
	w.lines, w.bytes, w.firstSeq, w.lastSeq = 0, 0, 0, 0
	return nil
}

// Close seals the final open segment (writes its manifest) on shutdown. No open handle to close
// — every batch already fsync'd — so this only records the manifest for the partial last segment.
func (w *JSONLWriter) Close() error {
	if w.lines == 0 {
		return nil
	}
	return w.writeManifest(w.seg)
}

// writeManifest records + fsyncs a sealed segment's manifest. FSYNC-FIRST is satisfied by
// construction: the segment file was fsync'd on every WriteBatch, so the manifest can never
// claim more than the durable content (no false TRUNCATED). The manifest is itself fsync'd so a
// crash can't lose it and turn a durable segment into a "manifest missing" false tamper alarm.
func (w *JSONLWriter) writeManifest(seg int) error {
	name := segmentName(seg)
	m := Manifest{File: name, FirstSeq: w.firstSeq, LastSeq: w.lastSeq, Lines: w.lines, Bytes: w.bytes, ClosedAt: w.now().UTC()}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileSync(filepath.Join(w.dir, name+".manifest"), append(mb, '\n'))
}

// writeFileSync writes b to path and fsyncs it (durable manifest).
func writeFileSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// truncateTornTail drops a trailing partial line (bytes after the last newline) — a crash or a
// failed write can leave one. A file with no newline at all is reset to empty. Idempotent.
func truncateTornTail(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	i := bytes.LastIndexByte(data, '\n')
	if i+1 == len(data) {
		return nil // already ends on a clean line boundary
	}
	return os.Truncate(path, int64(i+1)) // i==-1 -> truncate to 0 (whole file was a torn fragment)
}

// --- tamper-evidence read side ---

// VerifySegment re-counts a SEALED segment's lines against its manifest, catching truncation or
// line loss. A missing manifest means the segment is not sealed — either the single currently-
// OPEN active segment (legitimately manifest-less; a stream verifier skips the newest one) or a
// genuinely lost/deleted manifest. TORN-TAIL RULE: sealed segments never contain a torn tail (a
// crash's partial line lives only on the active segment and is truncated before the next append),
// so a count mismatch on a sealed segment is a genuine tamper/loss signal, not a crash artifact.
func VerifySegment(segmentPath string) error {
	mb, err := os.ReadFile(segmentPath + ".manifest")
	if err != nil {
		return fmt.Errorf("manifest missing for %s (unsealed/active or deleted): %w", filepath.Base(segmentPath), err)
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return fmt.Errorf("manifest corrupt for %s: %w", filepath.Base(segmentPath), err)
	}
	f, err := os.Open(segmentPath)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	lines := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			lines++
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if lines != m.Lines {
		return fmt.Errorf("segment %s TRUNCATED: manifest says %d lines, found %d", m.File, m.Lines, lines)
	}
	return nil
}

// ScanSeqGaps reads events (any order, possibly multi-org) and reports, per org, any MISSING
// seq between the min and max observed — a hole in the audit trail must be legible as a hole. It
// reports gaps, it does not repair.
func ScanSeqGaps(events []Event) map[string][]int64 {
	byOrg := map[string][]int64{}
	for _, e := range events {
		byOrg[e.OrgID.String()] = append(byOrg[e.OrgID.String()], e.Seq)
	}
	gaps := map[string][]int64{}
	for org, seqs := range byOrg {
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for i := 1; i < len(seqs); i++ {
			for s := seqs[i-1] + 1; s < seqs[i]; s++ {
				gaps[org] = append(gaps[org], s)
			}
		}
	}
	return gaps
}

// ExportOrg streams an org's lines VERBATIM from the JSONL segments under dir to w — a READER,
// never a re-serializer, so the per-line seq tamper-evidence is preserved byte-for-byte.
// Segments are read in name order (chronological, matching seq order); a line is emitted iff its
// org_id matches. A partial/torn or non-JSON line is skipped (it never survives into a sealed
// segment; on the active segment it is a not-yet-clean tail, eventually-consistent — its event
// is in PG). Decoding reads ONLY org_id; the ORIGINAL bytes are written.
func ExportOrg(dir string, orgID uuid.UUID, w io.Writer) error {
	segs, err := filepath.Glob(filepath.Join(dir, "access-*.jsonl"))
	if err != nil {
		return err
	}
	sort.Strings(segs)
	for _, seg := range segs {
		if err := exportSegment(seg, orgID, w); err != nil {
			return err
		}
	}
	return nil
}

func exportSegment(path string, orgID uuid.UUID, w io.Writer) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			OrgID uuid.UUID `json:"org_id"`
		}
		if err := json.Unmarshal(line, &probe); err != nil || probe.OrgID != orgID {
			continue // not this org, or a torn/non-JSON tail — skip; never emit a foreign/partial line
		}
		if _, err := w.Write(line); err != nil { // VERBATIM original bytes, not a re-marshal
			return err
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return sc.Err()
}
