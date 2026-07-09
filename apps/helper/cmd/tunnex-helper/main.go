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

	"github.com/tunnexio/tunnex/apps/helper"
)

func main() {
	// --version prints the build + caller-auth mode (native|stub) so a stub build
	// is immediately visible (smoke step zero).
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Printf("tunnex-helper %s caller-auth: %s\n", helper.HelperVersion, helper.CallerAuthKind())
		return
	}

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

	sup := helper.NewSupervisor(helper.NewBackend())
	verify := helper.PathCheckVerifier{InstallDir: installDir}
	srv := helper.NewServer(sup, verify, helper.NewPeerResolver())

	log.Printf("tunnex-helper %s: listening on %s (install dir %q, caller-auth: %s)",
		helper.HelperVersion, socketPath, installDir, helper.CallerAuthKind())
	if err := srv.Serve(ln); err != nil {
		log.Fatalf("tunnex-helper: serve: %v", err)
	}
}
