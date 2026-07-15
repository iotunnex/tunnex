package accesslog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

// TestIngestConcurrentSameOrgNoLossNoTear is the armed-guard for the concurrent-ingest class
// (review finding #1/#2 — the class the single-gateway box-walk could not exercise). Many
// gateways of ONE org report simultaneously; net/http runs each POST /agent/flow-events in
// its own goroutine, so IngestBatch is called concurrently on the SAME Ingester + shared
// JSONLWriter.
//
// RED on the pre-fold code (run this against HEAD before the fix): the per-batch seq was
// derived from MAX(seq) under READ COMMITTED with no serialization, so concurrent same-org
// batches computed the same base and collided on (org_id, seq); InsertAccessEvent's
// `ON CONFLICT DO NOTHING` + the discarded RowsAffected SILENTLY dropped the loser's rows
// (audit loss, no gap marker), and the un-serialized bufio JSONLWriter interleaved/tore the
// source-of-truth lines. This test MUST fail there.
//
// GREEN after the fold: a process-level Ingester mutex encloses the whole batch (seq-alloc +
// insert + JSONL append are ONE critical section) and the per-org DB counter allocates
// unique, monotonic, sweep-proof seq. So all events persist, seq is contiguous+unique, and
// every JSONL line is intact.
func TestIngestConcurrentSameOrgNoLossNoTear(t *testing.T) {
	q, pool, org := ingestPool(t) // skips without TUNNEX_TEST_DATABASE_URL
	ctx := context.Background()
	dir := t.TempDir()
	jw, err := NewJSONLWriter(dir, 1<<30) // no rotation: exercise the shared open segment
	if err != nil {
		t.Fatal(err)
	}
	ing := NewIngester(pool, jw, stubGrants{}, NewHealth(), nil)

	const goroutines, perBatch = 8, 25
	want := goroutines * perBatch

	var start sync.WaitGroup
	start.Add(1) // release all goroutines at once to maximize the race
	var done sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		done.Add(1)
		go func(g int) {
			defer done.Done()
			batch := make([]WireEvent, perBatch)
			for i := range batch {
				batch[i] = WireEvent{
					OccurredAt: time.Now().UTC(), Verdict: "allow",
					SrcIP: fmt.Sprintf("10.99.%d.%d", g, i+1), DstIP: "10.0.0.1", Protocol: "tcp",
				}
			}
			start.Wait()
			if err := ing.IngestBatch(ctx, org, uuid.New(), batch, 0); err != nil {
				errs <- err
			}
		}(g)
	}
	start.Done()
	done.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent ingest returned an error: %v", e)
	}

	// PG: NO silent loss — every event of every batch persisted, seq unique + contiguous.
	rows, err := q.ListAccessEvents(ctx, sqlc.ListAccessEventsParams{
		OrgID: org, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: maxUUID, PageLimit: int32(want + 100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != want {
		t.Fatalf("silent audit loss under concurrent same-org ingest: want %d events in PG, got %d", want, len(rows))
	}
	seen := map[int64]bool{}
	for _, r := range rows {
		if seen[r.Seq] {
			t.Fatalf("duplicate seq %d (collision not serialized)", r.Seq)
		}
		seen[r.Seq] = true
	}
	for s := int64(1); s <= int64(want); s++ {
		if !seen[s] {
			t.Fatalf("seq not contiguous 1..%d: missing %d", want, s)
		}
	}

	// JSONL: UNTORN — exactly `want` lines, every one valid JSON (no interleaved writes).
	if err := jw.Flush(); err != nil {
		t.Fatal(err)
	}
	segs, _ := filepath.Glob(filepath.Join(dir, "access-*.jsonl"))
	lines := 0
	for _, seg := range segs {
		f, err := os.Open(seg)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for sc.Scan() {
			if len(sc.Bytes()) == 0 {
				continue
			}
			var e Event
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				_ = f.Close()
				t.Fatalf("torn JSONL line (concurrent writer race): %q: %v", sc.Text(), err)
			}
			lines++
		}
		_ = f.Close()
	}
	if lines != want {
		t.Fatalf("JSONL source-of-truth line count %d != %d (loss or tear)", lines, want)
	}
}
