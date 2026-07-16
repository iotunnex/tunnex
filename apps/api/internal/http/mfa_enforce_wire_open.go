//go:build !enterprise

package http

// NewMfaEnforceEdition: org-level MFA enforcement (S7.5.5) is an enterprise feature; the open
// build returns false → the enforce/admin endpoints answer 403 edition_required AND the
// enrollment gate never engages (D2 downgrade-release by construction: enforcement releases in
// open, enrolled TOTP secrets are retained and self-service enrollment is untouched).
func NewMfaEnforceEdition() bool { return false }
