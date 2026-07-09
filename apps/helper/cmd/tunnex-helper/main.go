// Command tunnex-helper is the Tunnex privileged tunnel helper (S6.3). It runs as
// a LaunchDaemon (macOS) / Windows service, listens on an owner-only local socket,
// authenticates each caller against the app's install dir, and serves the typed
// tunnel protocol. Fail-closed is enforced by kernel-resident state the backend
// arranges at Up (pf/WFP), NOT by this process staying alive.
package main

import (
	"log"
	"os"

	"github.com/tunnexio/tunnex/apps/helper"
)

func main() {
	// The install dir (for the interim executable-inside-install-dir caller check)
	// and socket path are provided by the launchd plist / service config.
	installDir := os.Getenv("TUNNEX_INSTALL_DIR")
	socketPath := os.Getenv("TUNNEX_HELPER_SOCKET")
	if socketPath == "" {
		socketPath = helper.DefaultSocketPath()
	}

	ln, err := helper.NewListener(socketPath)
	if err != nil {
		log.Fatalf("tunnex-helper: listen: %v", err)
	}

	sup := helper.NewSupervisor(helper.StubBackend{})
	verify := helper.PathCheckVerifier{InstallDir: installDir}
	srv := helper.NewServer(sup, verify, helper.NewPeerResolver())

	log.Printf("tunnex-helper: listening on %s (install dir %q)", socketPath, installDir)
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("tunnex-helper: serve: %v", err)
	}
}
