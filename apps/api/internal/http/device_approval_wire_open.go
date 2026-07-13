//go:build !enterprise

package http

// NewDeviceApprovalEdition reports whether the device-approval gate (S7.3 device posture)
// is available in this build. FALSE in the open build: the approve/reject/pending/setting
// endpoints respond with the edition_required envelope.
//
// NAMED per-feature (not inferred from the policy subsystem's presence): device posture and
// Zero Trust policy are DISTINCT enterprise features that may tier apart, and the ledgered
// S12.1 build-tag -> runtime-license refactor rewrites every edition check — each must be a
// findable, dedicated boundary, not a proxy behind another feature's nil-check (the F2
// lesson: edition boundaries are named per feature).
func NewDeviceApprovalEdition() bool { return false }
