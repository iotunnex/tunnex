//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/tunnexio/tunnex/apps/helper"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	winWatchdogInterval      = 30 * time.Second
	winWatchdogMissThreshold = 3 // ~90s — tolerates an in-place app update
)

// startUninstallWatchdog (Windows) makes uninstall robust even when the NSIS uninstaller
// is skipped or corrupt: it watches the install dir's Tunnex.exe and, once it's been gone
// past the debounce, releases the WFP kill-switch and DELETES the helper's own SCM service.
// A clean Add/Remove Programs uninstall already does sc delete first (so this never fires
// then); this covers "the app folder was deleted but the service lingered". TUNNEX_INSTALL_DIR
// on Windows is the single install dir ($INSTDIR) where Tunnex.exe lives.
func startUninstallWatchdog(installDir string, sup *helper.Supervisor) {
	if installDir == "" || containsPathListSep(installDir) {
		return
	}
	appExe := filepath.Join(installDir, "Tunnex.exe")
	go func() {
		missing := 0
		t := time.NewTicker(winWatchdogInterval)
		defer t.Stop()
		for range t.C {
			if _, err := os.Stat(appExe); os.IsNotExist(err) {
				missing++
				if missing >= winWatchdogMissThreshold {
					selfUninstall(sup)
					return
				}
			} else {
				missing = 0
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

// selfUninstall releases the kill-switch, marks the service for deletion, and exits.
// DeleteService marks the service DELETE_PENDING; once this process exits the SCM removes
// it (a delete-pending service is not restarted by the failure policy).
func selfUninstall(sup *helper.Supervisor) {
	log.Printf("tunnex-helper: install dir removed — self-uninstalling the service")
	if err := sup.SelfHeal(); err != nil { // release any armed WFP kill-switch first
		log.Printf("tunnex-helper: self-uninstall self-heal: %v", err)
	}
	if m, err := mgr.Connect(); err == nil {
		if s, err := m.OpenService("tunnex-helper"); err == nil {
			if err := s.Delete(); err != nil {
				log.Printf("tunnex-helper: self-delete service: %v", err)
			}
			_ = s.Close()
		}
		_ = m.Disconnect()
	}
	os.Exit(0)
}
