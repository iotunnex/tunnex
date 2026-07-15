package accesslog

import (
	"sync"
	"time"
)

// Health is the LEGIBLE operational surface for the access-log subsystem (D3 + the JSONL
// write-failure legibility). Both the retention sweep and a JSONL write failure record here,
// so an operator has ONE place that shows "the audit stream is degraded" / "events were
// dropped for disk". Read by a health endpoint / admin surface; a getter returns a snapshot.
type Health struct {
	mu sync.Mutex

	// JSONL write-failure legibility: the SIEM source-of-truth diverging from PG (JSONL
	// failed while PG kept ingesting) must be visible. JSONLDegradedSince is set on the
	// first failure and cleared on the next success; the per-line seq means the missing
	// lines are ALSO a durable, detectable hole in the stream itself (ScanSeqGaps).
	jsonlDegradedSince time.Time
	jsonlFailures      int64

	// A roll's manifest write can fail while the segment's DATA is already durable on disk — the
	// tamper-evidence SEAL is deferred (retried on the next roll), which is NOT a write failure /
	// data-at-risk, so it is a SEPARATE, softer signal that must not read as a lost batch.
	jsonlSealDeferred bool

	// Retention: the drop-oldest sweep must alarm somewhere a human looks.
	retentionLastSweep time.Time
	retentionDropped   int64 // rows deleted by the last sweep (age + cap)
}

// Snapshot is a read-only view of Health for a health/admin endpoint.
type Snapshot struct {
	JSONLDegraded      bool      `json:"jsonl_degraded"`
	JSONLDegradedSince time.Time `json:"jsonl_degraded_since,omitempty"`
	JSONLFailures      int64     `json:"jsonl_failures"`
	JSONLSealDeferred  bool      `json:"jsonl_seal_deferred"`
	RetentionLastSweep time.Time `json:"retention_last_sweep,omitempty"`
	RetentionDropped   int64     `json:"retention_dropped"`
}

// NewHealth returns a zero-value Health (nothing degraded).
func NewHealth() *Health { return &Health{} }

// jsonlFailed records a JSONL write failure (idempotent onset — the since is preserved
// across repeated failures until a success clears it).
func (h *Health) jsonlFailed(now time.Time) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.jsonlDegradedSince.IsZero() {
		h.jsonlDegradedSince = now
	}
	h.jsonlFailures++
}

// jsonlRecovered clears the degraded + seal-deferred state after a fully successful batch.
func (h *Health) jsonlRecovered() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.jsonlDegradedSince = time.Time{}
	h.jsonlSealDeferred = false
}

// jsonlSealDeferred records that a batch is DURABLE on disk but its segment's manifest could not
// be written (the seal retries on the next roll). A soft signal — NOT a write failure / data loss.
func (h *Health) jsonlSealDeferredSet() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.jsonlSealDeferred = true
}

// recordSweep records a retention sweep's result.
func (h *Health) recordSweep(now time.Time, dropped int64) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.retentionLastSweep = now
	h.retentionDropped = dropped
}

// Snapshot returns the current health for display.
func (h *Health) Snapshot() Snapshot {
	if h == nil {
		return Snapshot{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return Snapshot{
		JSONLDegraded:      !h.jsonlDegradedSince.IsZero(),
		JSONLDegradedSince: h.jsonlDegradedSince,
		JSONLFailures:      h.jsonlFailures,
		JSONLSealDeferred:  h.jsonlSealDeferred,
		RetentionLastSweep: h.retentionLastSweep,
		RetentionDropped:   h.retentionDropped,
	}
}
