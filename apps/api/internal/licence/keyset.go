package licence

import (
	"crypto/ed25519"
	"encoding/base64"
)

// TrustedKeys is the SET of public keys this build accepts, keyed by kid.
//
// ⛔ COMPILED IN — not a file, not an env var, not configuration. BAKED AT BUILD TIME MEANS A ROTATION IS
// A RELEASE, and saying it any other way implies an operator could fix a signing-key compromise. They
// cannot: rotation is add-a-kid → ship → customers upgrade → issue under the new kid → later remove the
// old kid → ship again.
//
// ⭐ A SET RATHER THAN ONE KEY (D4, ruled) is what makes that sequence expressible at all. It does NOT
// make rotation cheap — keys minted under the old kid run to their own expiry, the installed base still
// upgrades twice, and a compromise remains undetectable because deployments never call home.
//
// ⚠ THE GOLDEN KEY IS PRESENT DELIBERATELY. It signs the cross-repo golden vector and nothing else; its
// private half is a published test seed. It is harmless — no real key is ever minted under it, and its
// presence is what lets the vector be verified by the SAME code path production uses, rather than by a
// test-only shim that could drift from it.
var TrustedKeys = mustKeys(map[string]string{
	// kid           base64url of the raw 32-byte Ed25519 public key (a JWK "x" value)
	GoldenKid: "2XAC4iGhtpJ-P3VxrW-6_OU9XHF-T2DXvDGlw6JTv_s",
})

// GoldenKid identifies the cross-repo golden vector's signing key. Test-only in effect; see TrustedKeys.
const GoldenKid = "k-golden-1"

func mustKeys(raw map[string]string) map[string]ed25519.PublicKey {
	out := make(map[string]ed25519.PublicKey, len(raw))
	for kid, b64 := range raw {
		b, err := base64.RawURLEncoding.DecodeString(b64)
		if err != nil || len(b) != ed25519.PublicKeySize {
			// A malformed literal is a BUILD defect, and failing at init is the only honest moment:
			// discovering it when a customer pastes a key means the deployment shipped unable to verify.
			panic("licence: malformed trusted key for kid " + kid)
		}
		out[kid] = ed25519.PublicKey(b)
	}
	return out
}
