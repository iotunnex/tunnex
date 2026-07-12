package policyspec

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// CanonicalHash is THE policy content fingerprint: 12 hex of SHA-256 over the
// canonical Compiled JSON — encoding/json marshal of the struct, whose byte
// stability the compiler's determinism test asserts (sorted Allow, fixed field
// order). The node agent computes the SAME hash over its mirror type
// (apps/node internal/nodepolicy) for the applied-policy status report, so
// pushed-vs-applied comparison is meaningful: both sides hash identical
// canonical bytes, never their own private serialization. A cross-module golden
// test on each side pins the two implementations to the same output — if either
// struct's field order/tags drift, its golden test goes red.
func CanonicalHash(c Compiled) string {
	b, err := json.Marshal(c)
	if err != nil {
		return "" // unreachable: Compiled is plain data
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:6])
}
