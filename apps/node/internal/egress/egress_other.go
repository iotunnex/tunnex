//go:build !linux

package egress

import "context"

// Manager is a no-op off Linux (the gateway data plane is Linux-only). Reconcile always
// reports NOT egress-capable so a non-Linux build never claims full-tunnel egress.
type Manager struct{}

func New(_ string) *Manager { return &Manager{} }

func (m *Manager) Reconcile(_ context.Context) (bool, error) { return false, nil }

func (m *Manager) Teardown(_ context.Context) {}
