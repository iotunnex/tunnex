package accesslog

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"
)

// JSONLWriter appends access events as JSON lines to a rotating stream on the customer's
// disk — the SIEM source-of-truth + retention record (D4/D5). Tamper-evidence MINIMUM
// (D4): every line carries the per-org monotonic `seq` (assigned at ingest), and every
// rotated segment gets a sidecar MANIFEST recording its line count + seq range + close
// time, so a DELETED or TRUNCATED segment is detectable. Hash-chaining (each line hashing
// the prior) is NAMED-DEFERRED (Tier-2, trigger = a compliance ask for cryptographic
// non-repudiation). Not safe for concurrent use — the ingest owns one writer, serialized.
// segmentSink is the current segment's underlying file — an *os.File in production, or a
// fake in tests (so a transient write/flush failure + the self-heal path are unit-provable).
type segmentSink interface {
	io.Writer
	Sync() error
	Close() error
	Name() string
}

type JSONLWriter struct {
	dir      string
	maxBytes int64
	seg      int
	f        segmentSink
	w        *bufio.Writer
	bytes    int64
	lines    int
	firstSeq int64
	lastSeq  int64
	now      func() time.Time
	// broken marks that a Write/Flush/Sync error poisoned the current bufio writer (its error
	// is STICKY — every later call would keep returning it, so the stream would silently stop
	// for the process lifetime). The next Append REOPENS a fresh segment to self-heal (fold-2
	// #1: the "cleared on next success" recovery must actually be reachable).
	broken bool
	// openFn creates a segment file; injectable so tests can drive write/flush failures.
	openFn func(path string) (segmentSink, error)
}

func osOpenSegment(path string) (segmentSink, error) {
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
}

// Manifest is the sidecar written next to each CLOSED segment (segment.jsonl.manifest).
// A verifier re-counts the segment's lines against Lines to catch truncation.
type Manifest struct {
	File     string    `json:"file"`
	FirstSeq int64     `json:"first_seq"`
	LastSeq  int64     `json:"last_seq"`
	Lines    int       `json:"lines"`
	Bytes    int64     `json:"bytes"`
	ClosedAt time.Time `json:"closed_at"`
}

// NewJSONLWriter opens a fresh stream under dir (created if absent). maxBytes<=0 uses
// DefaultJSONLMaxBytes.
func NewJSONLWriter(dir string, maxBytes int64) (*JSONLWriter, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultJSONLMaxBytes
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	jw := &JSONLWriter{dir: dir, maxBytes: maxBytes, now: time.Now, openFn: osOpenSegment}
	if err := jw.openSegment(); err != nil {
		return nil, err
	}
	return jw, nil
}

func segmentName(seg int) string { return fmt.Sprintf("access-%06d.jsonl", seg) }

func (w *JSONLWriter) openSegment() error {
	w.seg++
	p := filepath.Join(w.dir, segmentName(w.seg))
	f, err := w.openFn(p)
	if err != nil {
		return err
	}
	w.f, w.w = f, bufio.NewWriter(f)
	w.bytes, w.lines, w.firstSeq, w.lastSeq = 0, 0, 0, 0
	w.broken = false
	return nil
}

// abandonedExt marks a POISONED segment that was abandoned mid-write (see the writer state
// model). Distinct from ".manifest" (a cleanly-closed segment) — it names an incomplete,
// UNVERIFIABLE segment so VerifySegment/export can tell "abandoned" from "tampered".
const abandonedExt = ".abandoned"

// WRITER STATE MODEL (fold-3 — one rule per failure mode; the single `broken` flag used to
// conflate three modes):
//   - HEALTHY: append/flush normally.
//   - SYNC-DEGRADED (Flush: bufio.Flush ok, f.Sync fails): the lines ARE on disk (OS page
//     cache — readable + manifest-able), only the fsync failed. Durability-degraded, NOT
//     corruption → KEEP the segment (do NOT poison/reopen; that would abandon a valid segment
//     and destroy its tamper-evidence on a recoverable hiccup). Surfaced via the returned
//     error on Health; the next Flush retries the Sync.
//   - POISONED (bufio.Write / bufio.Flush errors, on Append, Flush, OR closeSegment/rotate):
//     the bufio's error is sticky and the segment is genuinely incomplete → set `broken`; the
//     next Append reopens a fresh segment and ABANDONS this one (marker, no manifest).
//
// reopen self-heals a POISONED writer: it abandons the poisoned segment (best-effort marker so
// the loss is legible + distinguishable from tampering) and opens a fresh one.
func (w *JSONLWriter) reopen() error {
	if w.f != nil {
		name := filepath.Base(w.f.Name())
		_ = w.f.Close() // best-effort — the old file is already poisoned
		w.f = nil
		// Mark the poisoned segment abandoned (best-effort — the disk may still be failing). Its
		// content is genuinely incomplete (appended-but-unflushed lines are lost); the marker
		// lets VerifySegment report it as abandoned/unverifiable (≠ tampered) and lets the export
		// skip it. The lost events are still queryable in the PG hot-window.
		_ = os.WriteFile(filepath.Join(w.dir, name+abandonedExt), []byte("abandoned\n"), 0o640)
	}
	return w.openSegment()
}

// Append writes one event as a JSON line, then rotates if the segment reached maxBytes.
func (w *JSONLWriter) Append(e Event) error {
	if w.broken {
		// A prior write/flush poisoned the bufio writer. Reopen a fresh segment so the stream
		// self-heals (fold-2 #1) — else the sticky error would make every later write fail and
		// the source-of-truth would silently stop for the process lifetime. If reopen ALSO
		// fails (e.g. disk still full), stay broken and surface the error; retry next call.
		if err := w.reopen(); err != nil {
			return err
		}
	}
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	n, err := w.w.Write(b)
	if err != nil {
		w.broken = true
		return err
	}
	if w.lines == 0 {
		w.firstSeq = e.Seq
	}
	w.lines++
	w.lastSeq = e.Seq
	w.bytes += int64(n)
	if w.bytes >= w.maxBytes {
		return w.rotate()
	}
	return nil
}

// Flush pushes buffered lines out of the bufio buffer to the OS and fsyncs the open
// segment. The ingest calls this after each batch so committed events are DURABLE on disk
// (and visible to a reader/ExportOrg) before the process can exit. Without it, Append only
// fills a bufio buffer that is flushed solely on rotation/Close — so a graceful shutdown
// (SIGTERM) or an export would see an empty/short segment while PG already holds the rows,
// the source-of-truth silently diverging (box-walk finding). No-op if no segment is open.
func (w *JSONLWriter) Flush() error {
	if w.f == nil {
		return nil
	}
	if err := w.w.Flush(); err != nil {
		w.broken = true // POISONED (sticky bufio) — the next Append reopens a fresh segment
		return err
	}
	if err := w.f.Sync(); err != nil {
		// SYNC-DEGRADED, NOT poisoned: bufio.Flush succeeded, so every line is on disk (OS page
		// cache) — only the fsync failed. KEEP the segment (do NOT set broken/reopen; that would
		// abandon a valid segment and drop its tamper-evidence on a recoverable hiccup, fold-3
		// #1). The returned error surfaces on Health; the next Flush retries the Sync.
		return err
	}
	return nil
}

// rotate flushes + closes the current segment, writes its manifest, and opens the next.
func (w *JSONLWriter) rotate() error {
	if err := w.closeSegment(); err != nil {
		return err
	}
	return w.openSegment()
}

func (w *JSONLWriter) closeSegment() error {
	if w.f == nil {
		return nil
	}
	if err := w.w.Flush(); err != nil {
		w.broken = true // POISONED on the rotation/close path too (fold-3 #2) — else the next
		return err      // Append would write into the still-poisoned bufio and drop an event before healing
	}
	name := filepath.Base(w.f.Name())
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	// A manifest is written ONLY for a segment that actually holds lines — an empty
	// trailing segment (rotate-then-close) needs no record.
	if w.lines == 0 {
		return os.Remove(filepath.Join(w.dir, name)) // drop the empty segment
	}
	m := Manifest{File: name, FirstSeq: w.firstSeq, LastSeq: w.lastSeq, Lines: w.lines, Bytes: w.bytes, ClosedAt: w.now().UTC()}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(w.dir, name+".manifest"), append(mb, '\n'), 0o640)
}

// Close flushes + closes the final open segment (writing its manifest).
func (w *JSONLWriter) Close() error { return w.closeSegment() }

// --- tamper-evidence read side ---

// ErrSegmentAbandoned reports a segment POISONED mid-write and abandoned (marker present, no
// manifest): UNVERIFIABLE + incomplete, which is DISTINCT from a tampered/truncated closed
// segment. An auditor reads it as a legible gap (a disk hiccup abandoned it), NOT as evidence
// of tampering; the lost events remain queryable in the PG hot-window.
var ErrSegmentAbandoned = errors.New("segment abandoned mid-write (unverifiable, incomplete — not tampered)")

// VerifySegment re-counts a closed segment's lines against its manifest, catching truncation
// or line loss. It fails LOUD on an abandoned segment (distinct error) and on a manifest-less
// segment — the tamper-evidence guarantee covers cleanly-closed segments only; the single
// currently-OPEN segment is legitimately manifest-less and is not verified (a stream verifier
// skips the newest open segment).
func VerifySegment(segmentPath string) error {
	if _, err := os.Stat(segmentPath + abandonedExt); err == nil {
		return fmt.Errorf("%s: %w", filepath.Base(segmentPath), ErrSegmentAbandoned)
	}
	mb, err := os.ReadFile(segmentPath + ".manifest")
	if err != nil {
		return fmt.Errorf("manifest missing for %s: %w", filepath.Base(segmentPath), err)
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

// ScanSeqGaps reads events (any order, possibly multi-org) and reports, per org, any
// MISSING seq between the min and max observed — a hole in the audit trail must be
// legible as a hole. It reports gaps, it does not repair.
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

// ExportOrg streams an org's lines VERBATIM from the JSONL segments under dir to w — a
// READER, never a re-serializer, so the per-line seq tamper-evidence is preserved
// byte-for-byte (timestamps ride as facts inside the line, not as integrity anchors).
// Segments are read in name order (chronological, matching seq order); a line is emitted
// iff its org_id matches. Decoding reads ONLY org_id; the ORIGINAL bytes are written.
//
// EVENTUALLY-CONSISTENT TAIL (fold-2 #4, accepted): the export runs WITHOUT the ingest lock,
// so if a batch is mid-flush on the active segment the scanner may read a partial final line,
// fail to decode it, and skip it — the export can lag the very newest committed event(s) by
// one flush. Those events are durable in PG and appear on the next export. We deliberately do
// NOT hold the ingest mutex across a potentially large export (it would stall all ingest); a
// point-in-time SIEM export being a hair behind the live tail is the correct trade.
func ExportOrg(dir string, orgID uuid.UUID, w io.Writer) error {
	segs, err := filepath.Glob(filepath.Join(dir, "access-*.jsonl"))
	if err != nil {
		return err
	}
	sort.Strings(segs)
	for _, seg := range segs {
		if _, err := os.Stat(seg + abandonedExt); err == nil {
			continue // ABANDONED/unverifiable segment — excluded from the verbatim export (not
			// silently ridden along); its committed events remain queryable in the PG hot-window.
		}
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
			continue // not this org (or unparseable) — skip; never emit a foreign line
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
