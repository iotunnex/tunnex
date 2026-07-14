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

	// --- ENTERPRISE seed (S7.4c) — laid down by cmd/seed-enterprise ON TOP of the
	// base seed, never forking it. These rows exist only to let the enterprise
	// e2e stack exercise the REAL S4.5/S4.5b assertions (SSO no-secret payload +
	// live 409 orphan render) instead of a gate-check / page.route mock.

	// DemoSSOProvider is the seeded SSO provider whose config View (S4.5) must
	// return a non-secret projection (client_id + keyed fingerprint, NEVER the
	// secret). "google" matches an OIDC provider the enterprise build wires.
	DemoSSOProvider = "google"
	// DemoSSOClientID is the seeded OIDC client id — surfaced by the config View.
	DemoSSOClientID = "demo-enterprise-client.apps.googleusercontent.com"
	// DemoSSOClientSecret is the plaintext secret the seed SEALS before storage.
	// It must never appear in any read-path payload (the S4.5 assertion).
	DemoSSOClientSecret = "demo-enterprise-client-secret-do-not-ship"

	// DemoGatewayNodeID is a seeded gateway node row. The 409 orphan check
	// (devices.ResizePool) reads device rows purely from the DB — a device's
	// node_id FK just needs a node ROW to exist; no enrolled agent, mTLS, or
	// join token is involved (verified S7.4c D-c4).
	DemoGatewayNodeID = "01900000-0000-7000-8000-000000000010"
	// DemoGatewayNodeName is that gateway's display name.
	DemoGatewayNodeName = "demo-gateway"

	// DemoStrandableDeviceID is a device holding a pool IP that a shrink strands,
	// producing the REAL 409 orphan list (S4.5b).
	DemoStrandableDeviceID = "01900000-0000-7000-8000-000000000011"
	// DemoStrandableDeviceName is that device's display name — it appears in the
	// rendered orphan list.
	DemoStrandableDeviceName = "demo-strandable-laptop"
	// DemoStrandableDeviceIP is the pool address the device holds. The demo org's
	// pool is 10.99.0.0/24; .200 is inside it but OUTSIDE a shrink to /25
	// (.0–.127), so the shrink strands it (reason: out_of_range).
	DemoStrandableDeviceIP = "10.99.0.200"
	// DemoStrandShrinkCIDR is the CIDR the e2e shrink targets to strand the device.
	DemoStrandShrinkCIDR = "10.99.0.0/25"
	// DemoStrandableDevicePubKey is a fixed, syntactically-valid WG public key
	// (32 zero bytes, base64). Never a private key.
	DemoStrandableDevicePubKey = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEA="
)
