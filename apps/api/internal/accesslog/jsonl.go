package accesslog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// segRE bounds a real segment filename EXACTLY (access-<6 digits>.jsonl). resume()/export only
// consider files matching it, so a stray/malformed access-*.jsonl (which glob would match and
// sort last) can NEVER be taken as the active segment and make the writer overwrite a sealed one
// (review [0] — a tamper-evidence bug).
var segRE = regexp.MustCompile(`^access-\d{6}\.jsonl$`)

// syncDirFn fsyncs a directory so a newly-created file's/manifest's directory ENTRY is
// crash-durable (fsync of the file alone doesn't persist the dir entry — review [1]). Injectable
// for tests.
var syncDirFn = func(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close() //nolint:errcheck
	return d.Sync()
}

// JSONLWriter appends access events as JSON lines to a rotating stream on the customer's disk
// — the SIEM source-of-truth + retention record (D4/D5). Tamper-evidence MINIMUM (D4): every
// line carries the per-org monotonic `seq`, and every SEALED segment gets a sidecar MANIFEST
// (line count + seq range + close time), so a DELETED or TRUNCATED segment is detectable.
//
// DERIVE-FROM-DISK design (the completing reduce after the buffered/self-healing/counter
// designs kept letting in-memory state drift from the disk). The writer keeps essentially NO
// state: just the current segment NUMBER. It never holds an open handle or a bufio between
// batches, and it keeps NO in-memory line/byte accounting.
//   - WriteBatch: truncate any torn tail (cheap — checks the last byte), open O_APPEND, write
//     the whole batch in ONE Write, FSYNC, close. Durable on nil return.
//   - roll/Close SEAL a segment by SCANNING it from disk to build the manifest, so the manifest
//     and VerifySegment count the SAME on-disk lines by construction — they cannot disagree (no
//     parallel counter to under/over-claim; the whole false-TRUNCATED class is gone).
// A crash or failed write can leave a partial trailing line on the active segment; it is
// truncated at a clean boundary before the next append (mid-run or on restart via resume()),
// so a torn tail never merges a following line and never survives into a SEALED segment. A
// failed batch returns an error (the ingest marks Health + the per-line seq leaves a detectable
// hole — the events are in PG); the next batch starts fresh (no failure state to recover).
//
// Not safe for concurrent use — the ingest owns one writer, serialized by the Ingester mutex.
// SCAN BOUND: a seal scans one segment, which rotation bounds to maxBytes (default 64 MiB), so
// the scan is O(maxBytes) once per maxBytes written — negligible amortized (and only on a roll).
type JSONLWriter struct {
	dir      string
	maxBytes int64
	now      func() time.Time
	seg      int // the current (active) segment number
}

// ErrSealDeferred means a batch's events are DURABLE on disk but the segment's manifest write
// failed — the seal is retried on the next roll. The ingest treats it as a soft signal, NOT a
// lost batch. (Wrapped so callers use errors.Is.)
var ErrSealDeferred = errors.New("segment seal deferred: batch durable, manifest write failed (retried next roll)")

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
// prior run left (crash-safe — never O_TRUNCs existing data). maxBytes<=0 uses DefaultJSONLMaxBytes.
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

func (w *JSONLWriter) segPath(seg int) string { return filepath.Join(w.dir, segmentName(seg)) }

// segmentFiles lists the WELL-FORMED segment files under dir, sorted (chronological). A stray or
// malformed access-*.jsonl is excluded, so it can never hijack resume() or leak into an export.
func segmentFiles(dir string) ([]string, error) {
	all, err := filepath.Glob(filepath.Join(dir, "access-*.jsonl"))
	if err != nil {
		return nil, err
	}
	segs := all[:0]
	for _, p := range all {
		if segRE.MatchString(filepath.Base(p)) {
			segs = append(segs, p)
		}
	}
	sort.Strings(segs)
	return segs, nil
}

// resume picks the segment to continue: the highest-numbered segment WITHOUT a manifest is the
// still-open active one — truncate any torn tail a crash left, then continue it. A SEALED
// (has-manifest) highest segment means the last run closed cleanly → start fresh after it. No
// segments → start at 1. No accounting is rebuilt — it is derived from disk at seal time.
func (w *JSONLWriter) resume() error {
	segs, err := segmentFiles(w.dir)
	if err != nil {
		return err
	}
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
	w.seg = n
	return truncateTornTail(last) // clean the active segment; accounting is derived at seal
}

// WriteBatch appends a whole batch as JSON lines DURABLY: truncate any torn tail, open O_APPEND,
// write the batch in one Write, FSYNC, close. On nil return every line is on disk. On any
// failure it returns the error (a torn tail it may have left is truncated by the next batch).
// After a durable write it size-rolls: SEAL by scanning the segment from disk. A seal (manifest)
// failure does NOT lose the batch — it returns ErrSealDeferred and retries on the next roll.
func (w *JSONLWriter) WriteBatch(events []Event) error {
	if len(events) == 0 {
		return nil
	}
	path := w.segPath(w.seg)
	if err := truncateTornTail(path); err != nil {
		return err
	}
	// Is this the first write to a brand-new segment file? If so its directory ENTRY must be
	// fsync'd after creation, not just the file's data (review [1]).
	newSegment := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		newSegment = true
	}
	var buf bytes.Buffer
	for i := range events {
		b, err := json.Marshal(events[i])
		if err != nil {
			return err // nothing written yet — no torn file
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if newSegment {
		if err := syncDirFn(w.dir); err != nil { // make the new segment's dir entry crash-durable
			return err
		}
	}
	// Size-roll: the batch is ALREADY durable, so nothing past here may fail the batch.
	fi, err := os.Stat(path)
	if err != nil {
		return nil // a Stat error only skips the size check (durable data); the next batch re-checks (review [6])
	}
	if fi.Size() >= w.maxBytes {
		if err := w.sealSegment(w.seg); err != nil {
			// The batch is durable; only the manifest seal failed. ADVANCE to a fresh segment
			// anyway so a persistently-failing seal can't grow this segment unbounded and re-scan
			// it every batch under the mutex (review [2]). The old segment stays unsealed
			// (manifest-less, data intact) — a legible, Health-noted seal gap, not a loss.
			w.seg++
			return ErrSealDeferred
		}
		w.seg++
	}
	return nil
}

// Close seals the final partial segment on shutdown (truncate any torn tail first, then seal).
// No open handle to close — every batch already fsync'd.
func (w *JSONLWriter) Close() error {
	path := w.segPath(w.seg)
	if err := truncateTornTail(path); err != nil {
		return err
	}
	return w.sealSegment(w.seg)
}

// sealSegment writes a segment's manifest by SCANNING it from disk — the on-disk content is the
// single source of truth, so the manifest count can never drift from what VerifySegment will
// count (both count the same non-empty lines). No-op if the segment holds no lines / was never
// created. The manifest is itself fsync'd so a crash can't lose it and turn a durable segment
// into a "manifest missing" false alarm.
func (w *JSONLWriter) sealSegment(seg int) error {
	path := w.segPath(seg)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // never created (no batch landed here) — nothing to seal
		}
		return err
	}
	lines := 0
	var firstSeq, lastSeq, nbytes int64
	first := true
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		lines++
		nbytes += int64(len(line)) + 1
		var probe struct {
			Seq int64 `json:"seq"`
		}
		if json.Unmarshal(line, &probe) == nil {
			if first {
				firstSeq = probe.Seq
				first = false
			}
			lastSeq = probe.Seq
		}
	}
	serr := sc.Err()
	_ = f.Close()
	if serr != nil {
		return serr
	}
	if lines == 0 {
		return nil // empty segment — no manifest to write
	}
	m := Manifest{File: segmentName(seg), FirstSeq: firstSeq, LastSeq: lastSeq, Lines: lines, Bytes: nbytes, ClosedAt: w.now().UTC()}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeFileSync(path+".manifest", append(mb, '\n'))
}

// writeFileSync writes b to path, fsyncs it AND its parent directory (so the new manifest's
// directory entry is crash-durable, not just its data — review [1]).
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
	if err := f.Close(); err != nil {
		return err
	}
	return syncDirFn(filepath.Dir(path))
}

// truncateTornTail drops a trailing partial line (bytes after the last newline) — a crash or a
// failed write can leave one. CHEAP: the common case (file already ends on a newline) reads only
// the last byte; only a genuine torn tail triggers a bounded backward scan for the last newline.
// A file with no newline at all is reset to empty. Idempotent.
func truncateTornTail(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if fi.Size() == 0 {
		return nil
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0o640)
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck
	last := make([]byte, 1)
	if _, err := f.ReadAt(last, fi.Size()-1); err != nil {
		return err
	}
	if last[0] == '\n' {
		return nil // clean boundary — the common case, O(1)
	}
	pos, err := lastNewline(f, fi.Size())
	if err != nil {
		return err
	}
	return f.Truncate(pos + 1) // pos = index of the last '\n' (or -1 → truncate to 0)
}

// lastNewline scans backward in bounded chunks for the last '\n'; returns its index, or -1.
func lastNewline(f *os.File, size int64) (int64, error) {
	const chunk = 64 * 1024
	buf := make([]byte, chunk)
	for end := size; end > 0; {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		n := int(end - start)
		if _, err := f.ReadAt(buf[:n], start); err != nil && err != io.EOF {
			return 0, err
		}
		for i := n - 1; i >= 0; i-- {
			if buf[i] == '\n' {
				return start + int64(i), nil
			}
		}
		end = start
	}
	return -1, nil
}

// --- tamper-evidence read side ---

// VerifySegment re-counts a SEALED segment's non-empty lines against its manifest, catching
// truncation or line loss. A missing manifest means the segment is not sealed — either the
// single currently-OPEN active segment (legitimately manifest-less; a stream verifier skips the
// newest one) or a genuinely lost/deleted manifest. Sealed segments never contain a torn tail
// (it is truncated before the next append), and the manifest is DERIVED from the same on-disk
// lines this counts, so a mismatch on a sealed segment is a genuine tamper/loss signal.
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
// seq between the min and max observed — a hole in the audit trail must be legible as a hole.
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
// never a re-serializer, so the per-line seq tamper-evidence is preserved byte-for-byte. A
// partial/torn or non-JSON line is skipped (it never survives into a sealed segment; on the
// active segment it is a not-yet-clean tail, eventually-consistent — its event is in PG).
func ExportOrg(dir string, orgID uuid.UUID, w io.Writer) error {
	segs, err := segmentFiles(dir)
	if err != nil {
		return err
	}
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
			continue // not this org, or a torn/non-JSON tail — skip
		}
		if _, err := w.Write(line); err != nil {
			return err
		}
		if _, err := w.Write([]byte{'\n'}); err != nil {
			return err
		}
	}
	return sc.Err()
}
