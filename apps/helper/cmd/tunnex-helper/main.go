// Command tunnex-helper is the Tunnex privileged tunnel helper (S6.3). It runs as
// a LaunchDaemon (macOS) / Windows service, listens on an owner-only local socket,
// authenticates each caller against the app's install dir, and serves the typed
// tunnel protocol. Fail-closed is enforced by kernel-resident state the backend
// arranges at Up (pf/WFP), NOT by this process staying alive.
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/tunnexio/tunnex/apps/helper"
)

func main() {
	// --version prints the build + caller-auth mode (native|stub) so a stub build
	// is immediately visible (smoke step zero).
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("tunnex-helper %s caller-auth: %s\n", helper.HelperVersion, helper.CallerAuthKind())
		return
	}

	// The install dir(s) (for the interim executable-inside-install-dir caller check)
	// and socket path are provided by the launchd plist / service config.
	// TUNNEX_INSTALL_DIR may list several dirs, os.PathListSeparator-joined (a dev
	// install trusts BOTH /usr/local/tunnex and the Electron binary dir).
	installDir := os.Getenv("TUNNEX_INSTALL_DIR")
	socketPath := os.Getenv("TUNNEX_HELPER_SOCKET")
	if socketPath == "" {
		socketPath = helper.DefaultSocketPath()
	}

	ln, err := helper.NewListener(socketPath)
	if err != nil {
		log.Fatalf("tunnex-helper: listen: %v", err)
	}

	sup := helper.NewSupervisor(helper.NewBackend())

	// Startup self-heal: release any kill-switch stranded by a PRIOR helper that died
	// without a graceful Down (crash / kill -9). Runs BEFORE serving so a KeepAlive
	// restart un-strands the host instead of re-serving with the stale block.
	if err := sup.SelfHeal(); err != nil {
		log.Printf("tunnex-helper: startup self-heal: %v", err)
	}
	// Dead-man loop: bounds the fail-closed model. If the owning app stops
	// heartbeating past DeadManDefault (crashed/wedged), auto-release the block so an
	// unrecovered crash can't strand the host indefinitely.
	go func() {
		t := time.NewTicker(helper.DeadManDefault / 3)
		defer t.Stop()
		for range t.C {
			if sup.CheckDeadMan() {
				log.Printf("tunnex-helper: dead-man fired — kill-switch auto-released (owner gone > %s)", helper.DeadManDefault)
			}
		}
	}()

	verify := helper.PathCheckVerifier{InstallDirs: filepath.SplitList(installDir)}
	srv := helper.NewServer(sup, verify, helper.NewPeerResolver())

	log.Printf("tunnex-helper %s: listening on %s (install dirs %q, caller-auth: %s)",
		helper.HelperVersion, socketPath, installDir, helper.CallerAuthKind())
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("tunnex-helper: serve: %v", err)
	}
}
