//go:build windows

package main

import "github.com/tunnexio/tunnex/apps/helper/internal/wfp"

// wfpClean is the `--wfp-clean` escape hatch: remove the persistent Tunnex WFP kill-switch. Same
// code path as the startup self-heal, runnable from an admin prompt even when the service is dead.
func wfpClean() error { return wfp.Clean() }

// wfpArmTest is the DEV `--wfp-arm-test`: arm a persistent block-all so the escape hatch can be
// deliberate-red'd (wedge the box, then prove --wfp-clean/reboot recovers it).
func wfpArmTest() error { return wfp.ArmBlockAll() }
