package reconcile

import (
	"context"
	"sync"
)

// MemBackend is an in-memory WGBackend used by the S3.1 agent (the real wgctrl
// adapter arrives with the WireGuard device lifecycle in S3.2). It lets the full
// enroll->reconcile loop run end-to-end without NET_ADMIN or a WG device.
type MemBackend struct {
	mu    sync.Mutex
	peers []Peer
}

// NewMemBackend returns an empty in-memory backend.
func NewMemBackend() *MemBackend { return &MemBackend{} }

func (m *MemBackend) Peers(context.Context) ([]Peer, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Peer(nil), m.peers...), nil
}

func (m *MemBackend) ApplyPeers(_ context.Context, peers []Peer) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peers = append([]Peer(nil), peers...)
	return nil
}
