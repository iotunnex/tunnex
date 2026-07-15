package accesslog

import (
	"bufio"
	"encoding/json"
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
type JSONLWriter struct {
	dir      string
	maxBytes int64
	seg      int
	f        *os.File
	w        *bufio.Writer
	bytes    int64
	lines    int
	firstSeq int64
	lastSeq  int64
	now      func() time.Time
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
	jw := &JSONLWriter{dir: dir, maxBytes: maxBytes, now: time.Now}
	if err := jw.openSegment(); err != nil {
		return nil, err
	}
	return jw, nil
}

func segmentName(seg int) string { return fmt.Sprintf("access-%06d.jsonl", seg) }

func (w *JSONLWriter) openSegment() error {
	w.seg++
	p := filepath.Join(w.dir, segmentName(w.seg))
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o640)
	if err != nil {
		return err
	}
	w.f, w.w = f, bufio.NewWriter(f)
	w.bytes, w.lines, w.firstSeq, w.lastSeq = 0, 0, 0, 0
	return nil
}

// Append writes one event as a JSON line, then rotates if the segment reached maxBytes.
func (w *JSONLWriter) Append(e Event) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	n, err := w.w.Write(b)
	if err != nil {
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
		return err
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

// VerifySegment re-counts a closed segment's lines against its manifest, catching
// truncation or line loss. Returns an error naming the mismatch.
func VerifySegment(segmentPath string) error {
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
