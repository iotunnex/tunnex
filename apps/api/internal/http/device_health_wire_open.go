//go:build !enterprise

package http

// NewDeviceHealthEdition: device health / posture checks (S7.5.3) are an
// enterprise feature; the open build returns 403 edition_required. NAMED per
// feature (not a nil-port proxy) — the standing discipline (F2 / S12.1): device
// health, device approval, and Zero Trust policy are distinct enterprise
// capabilities that must unlock independently.
func NewDeviceHealthEdition() bool { return false }
