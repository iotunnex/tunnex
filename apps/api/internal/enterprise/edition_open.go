//go:build !enterprise

// Package enterprise is the boundary between the open-core build and the
// enterprise build. The DEFAULT (no build tag) is the open edition. Enterprise
// features live behind `-tags enterprise`; see edition_enterprise.go.
//
// The multi-tenant schema is fully present in both editions — the open build
// simply caps organization creation. Lifting the cap is an enterprise feature.
package enterprise

// Name identifies the compiled edition.
const Name = "open"

// Unlimited reports whether organization creation is uncapped.
const Unlimited = false

// MaxOrganizations is the org cap when not Unlimited.
const MaxOrganizations = 1
