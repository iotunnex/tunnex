//go:build !darwin

package main

import "github.com/tunnexio/tunnex/apps/helper"

// startUninstallWatchdog is macOS-only (the app-bundle self-uninstall). On Windows the
// SCM service is removed by the uninstaller; elsewhere there is nothing to watch.
func startUninstallWatchdog(_ string, _ *helper.Supervisor) {}
