package ovpnserver

import (
	"context"
	"os"
	"testing"
)

// TestSupervisorSpawnsIfNotAliveElseNoop (4d) locks the self-heal core: spawn when no process / a
// dead process; leave a live one untouched. Deterministic — spawn + isAlive injected.
func TestSupervisorSpawnsIfNotAliveElseNoop(t *testing.T) {
	spawns := 0
	alive := false
	sup := &Supervisor{
		spawn:   func(string) (*os.Process, error) { spawns++; return &os.Process{}, nil },
		isAlive: func(*os.Process) bool { return alive },
	}
	ctx := context.Background()

	// no process yet → spawn.
	if err := sup.Ensure(ctx, "server.conf"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if spawns != 1 {
		t.Fatalf("first ensure must spawn; spawns=%d", spawns)
	}
	// process alive → NO respawn.
	alive = true
	_ = sup.Ensure(ctx, "server.conf")
	if spawns != 1 {
		t.Fatalf("a live process must not be respawned; spawns=%d", spawns)
	}
	// process died → respawn (self-heal).
	alive = false
	_ = sup.Ensure(ctx, "server.conf")
	if spawns != 2 {
		t.Fatalf("a dead process must be respawned; spawns=%d", spawns)
	}
}
