//go:build enterprise

package enterprise

// Name identifies the compiled edition.
const Name = "enterprise"

// Unlimited reports whether organization creation is uncapped.
const Unlimited = true

// MaxOrganizations is unused when Unlimited; kept for symmetry with the open build.
const MaxOrganizations = 0
