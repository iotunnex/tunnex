package sites

import "strings"

// S8.4 D1-addition — the disjointness law's DNS cousin: within an org, a forwarded DOMAIN maps to exactly
// ONE resolver. Two sites both claiming `corp.local` is a conflict (which resolver wins is undefined), so it
// is REFUSED at write time — decided here in Slice 1, wired into the CRUD in Slice 2 (never discovered in
// the walk). NormalizeDomain must agree with the agent forwarder's normalizer on what a domain IS.

// NormalizeDomain lowercases + strips a trailing dot; ok=false for empty / space-bearing / empty-label input.
func NormalizeDomain(d string) (string, bool) {
	d = strings.TrimSpace(strings.ToLower(d))
	d = strings.TrimSuffix(d, ".")
	if d == "" || strings.Contains(d, " ") {
		return "", false
	}
	for _, lbl := range strings.Split(d, ".") {
		if lbl == "" {
			return "", false
		}
	}
	return d, true
}

// DNSDomainConflict reports whether candidate's normalized domain already exists in the org's set of
// forwarded domains (one zone → one resolver). A malformed candidate is treated as a conflict=false here
// (the caller rejects malformed separately with a distinct error); comparison is on the normalized form so
// `Corp.Local.` and `corp.local` collide.
func DNSDomainConflict(existing []string, candidate string) bool {
	cand, ok := NormalizeDomain(candidate)
	if !ok {
		return false
	}
	for _, e := range existing {
		if n, ok := NormalizeDomain(e); ok && n == cand {
			return true
		}
	}
	return false
}
