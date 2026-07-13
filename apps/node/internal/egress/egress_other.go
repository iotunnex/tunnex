//go:build !linux

package egress

import (
	"context"
	"time"

	"github.com/tunnexio/tunnex/apps/node/internal/nodepolicy"
)

// Manager is a no-op off Linux (the gateway data plane is Linux-only). Reconcile always
// reports NOT egress-capable so a non-Linux build never claims full-tunnel egress.
type Manager struct{}

func New(_ string) *Manager { return &Manager{} }

func (m *Manager) Reconcile(_ context.Context) (bool, error) { return false, nil }

func (m *Manager) Teardown(_ context.Context) {}

// SetPolicy is a no-op off Linux (no forward chain to program).
func (m *Manager) SetPolicy(_ *nodepolicy.Compiled) {}

// AppliedStatus off Linux reports "nothing applied" (version 0, no hash, no error) —
// a non-Linux agent never claims a policy is in force.
func (m *Manager) AppliedStatus() (int, string, time.Time, error) { return 0, "", time.Time{}, nil }
