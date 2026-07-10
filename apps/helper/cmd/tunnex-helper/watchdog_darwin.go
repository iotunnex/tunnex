//go:build darwin

package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tunnexio/tunnex/apps/helper"
)

const (
	daemonPlist  = "/Library/LaunchDaemons/io.tunnex.helper.plist"
	installDest  = "/usr/local/tunnex"
	runtimeDir   = "/var/run/tunnex"
	pfConfBackup = "/etc/pf.conf.tunnex-bak"
	// watchdogInterval × watchdogMissThreshold ≈ how long the app must be gone before
	// self-uninstall. ~90s tolerates an app-update that briefly replaces the bundle.
	watchdogInterval      = 30 * time.Second
	watchdogMissThreshold = 3
)

// startUninstallWatchdog makes "drag the app to Trash" fully clean on unsigned S6.5a
// (the macOS-blessed auto-removal, SMAppService, needs signing → S6.5b). The helper is
// a root LaunchDaemon OUTSIDE the .app, so trashing the bundle alone leaves it behind.
// This watches the owning /Applications/Tunnex.app; once it's been gone past the
// debounce, the helper releases its kill-switch, restores pf, removes its own files, and
// unloads itself. Only runs for a real single-dir app-bundle install (the packaged
// pkg); the dev multi-dir TUNNEX_INSTALL_DIR is skipped.
func startUninstallWatchdog(installDir string, sup *helper.Supervisor) {
	if installDir == "" || containsPathListSep(installDir) {
		return // dev / multi-dir install — no watchdog
	}
	// installDir = …/Tunnex.app/Contents/MacOS → app bundle = up two levels.
	app := filepath.Dir(filepath.Dir(installDir))
	if filepath.Ext(app) != ".app" {
		return // not an app-bundle install path — nothing to watch
	}
	go func() {
		missing := 0
		t := time.NewTicker(watchdogInterval)
		defer t.Stop()
		for range t.C {
			if _, err := os.Stat(app); os.IsNotExist(err) {
				missing++
				if missing >= watchdogMissThreshold {
					selfUninstall(app, sup)
					return
				}
			} else {
				missing = 0 // present (or a transient stat error) → reset
			}
		}
	}()
}

func containsPathListSep(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == os.PathListSeparator {
			return true
		}
	}
	return false
}

// selfUninstall tears the helper down completely after its owning app was removed.
// Ordering matters: release the kill-switch FIRST (never leave the host stranded),
// then restore pf, remove installed files, and finally unload the daemon (the plist is
// removed first so the KeepAlive can't restart it).
func selfUninstall(app string, sup *helper.Supervisor) {
	log.Printf("tunnex-helper: owning app %q removed — self-uninstalling", app)

	// 1. Release any armed kill-switch (pf anchor rules) so networking is restored.
	if err := sup.SelfHeal(); err != nil {
		log.Printf("tunnex-helper: self-uninstall self-heal: %v", err)
	}
	// 2. Restore /etc/pf.conf (remove the tunnex anchor reference) + flush the anchor.
	restorePf()
	// 3. Remove installed files.
	_ = os.RemoveAll(installDest)
	_ = os.RemoveAll(runtimeDir)
	_ = os.Remove(daemonPlist)
	// 4. Unload self — plist already gone, so no KeepAlive restart. bootout SIGTERMs us.
	_ = exec.Command("launchctl", "bootout", "system", daemonPlist).Run()
	os.Exit(0)
}

func restorePf() {
	if data, err := os.ReadFile(pfConfBackup); err == nil {
		if err := os.WriteFile("/etc/pf.conf", data, 0o644); err == nil {
			_ = os.Remove(pfConfBackup)
			_ = exec.Command("pfctl", "-f", "/etc/pf.conf").Run()
		}
	}
	// Flush the anchor regardless (harmless if already empty).
	_ = exec.Command("pfctl", "-a", "tunnex", "-F", "all").Run()
}
