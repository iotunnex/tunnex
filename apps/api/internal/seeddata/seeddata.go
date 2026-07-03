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
)
