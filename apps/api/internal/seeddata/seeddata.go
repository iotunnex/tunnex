// Package seeddata holds fixed identifiers for the demo/seed dataset.
//
// These are STABLE, documented constants so e2e tests and fixtures can reference
// the demo org/user directly instead of querying for them. Never reuse these IDs
// for real data. The values are valid UUIDv7-shaped constants.
package seeddata

const (
	// DemoOrgID is the fixed id of the seeded demo organization.
	DemoOrgID = "01900000-0000-7000-8000-000000000001"
	// DemoOwnerUserID is the fixed id of the demo org's owner user.
	DemoOwnerUserID = "01900000-0000-7000-8000-000000000002"

	// DemoOrgName is the demo organization's display name.
	DemoOrgName = "Demo Organization"
	// DemoOrgSlug is the demo organization's slug.
	DemoOrgSlug = "demo"
	// DemoOwnerEmail is the demo owner's login email.
	DemoOwnerEmail = "owner@demo.tunnex.local"
	// DemoOwnerName is the demo owner's display name.
	DemoOwnerName = "Demo Owner"
	// DemoOwnerPassword is the demo owner's password (development only).
	DemoOwnerPassword = "tunnex-demo-password"

	// DemoMemberUserID is a second seeded user with the plain 'member' role, so
	// the Users roster is populated and the role-gated UI (owner sees controls,
	// member does not) is exercisable end-to-end.
	DemoMemberUserID = "01900000-0000-7000-8000-000000000003"
	// DemoMemberEmail is the demo member's login email.
	DemoMemberEmail = "member@demo.tunnex.local"
	// DemoMemberName is the demo member's display name.
	DemoMemberName = "Demo Member"
	// DemoMemberPassword is the demo member's password (development only).
	DemoMemberPassword = "tunnex-demo-password"

	// DemoUnverifiedAdminUserID is an admin whose email is intentionally NOT
	// verified, so the UI can prove it hides mutating controls (invite/role/
	// deactivate) that the server would 403 with email_not_verified — a role
	// grant is necessary but not sufficient.
	DemoUnverifiedAdminUserID = "01900000-0000-7000-8000-000000000004"
	// DemoUnverifiedAdminEmail is that admin's login email.
	DemoUnverifiedAdminEmail = "unverified-admin@demo.tunnex.local"
	// DemoUnverifiedAdminName is that admin's display name.
	DemoUnverifiedAdminName = "Demo Unverified Admin"
	// DemoUnverifiedAdminPassword is that admin's password (development only).
	DemoUnverifiedAdminPassword = "tunnex-demo-password"

	// DemoNoOrgUserID is a VERIFIED user with NO membership — the fresh-signup
	// state the onboarding funnel (S4.7) targets. It lets the create-org routing
	// and the open-edition single-org cap (invitation-only) be exercised against
	// the REAL backend (the demo org already occupies the single-org slot), not a
	// mock.
	DemoNoOrgUserID = "01900000-0000-7000-8000-000000000005"
	// DemoNoOrgEmail is that user's login email.
	DemoNoOrgEmail = "fresh-user@demo.tunnex.local"
	// DemoNoOrgName is that user's display name.
	DemoNoOrgName = "Demo Fresh User"
	// DemoNoOrgPassword is that user's password (development only).
	DemoNoOrgPassword = "tunnex-demo-password"
)
