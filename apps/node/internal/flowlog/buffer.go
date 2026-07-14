package flowlog

import "sync"

// DefaultBufferCap bounds the in-memory flow-event ring — the EXPLICIT buffer bound
// (16384 events). On overflow the OLDEST event is dropped and a drop counter increments;
// Drain surfaces that count so a hole in the audit trail is LEGIBLE (the CP writes an
// explicit "N events dropped" gap marker into the stream on reconnect). The buffer NEVER
// blocks a producer — enforcement/observation must never wait on the reporter.
const DefaultBufferCap = 16384

// Buffer is a bounded, concurrency-safe, non-blocking ring of flow events.
type Buffer struct {
	mu      sync.Mutex
	cap     int
	events  []Event
	dropped int64
}

// NewBuffer returns a ring bounded to capacity (DefaultBufferCap if capacity<=0).
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultBufferCap
	}
	return &Buffer{cap: capacity, events: make([]Event, 0, capacity)}
}

// Add appends an event; at capacity it drops the OLDEST and counts the drop. Non-blocking.
func (b *Buffer) Add(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.events) >= b.cap {
		// Drop-oldest: shift down by one. The count is the legibility signal.
		copy(b.events, b.events[1:])
		b.events = b.events[:b.cap-1]
		b.dropped++
	}
	b.events = append(b.events, e)
}

// Drain removes and returns the buffered events plus the number DROPPED since the last
// drain (both reset). The caller ships the events and records the drop-count as a gap.
func (b *Buffer) Drain() (events []Event, dropped int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	events, dropped = b.events, b.dropped
	b.events = make([]Event, 0, b.cap)
	b.dropped = 0
	return events, dropped
}

// Len reports the current buffered count (for readiness/metrics; not load-bearing).
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.events)
}
