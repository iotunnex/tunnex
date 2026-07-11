//go:build !windows

package main

import "errors"

// The WFP escape hatch + dev arm-test are Windows-only (they operate on the WFP kill-switch).
func wfpClean() error   { return errors.New("--wfp-clean is Windows-only") }
func wfpArmTest() error { return errors.New("--wfp-arm-test is Windows-only") }
