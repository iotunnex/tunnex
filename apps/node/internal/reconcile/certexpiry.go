package reconcile

import (
	"crypto/tls"
	"errors"
	"strings"
)

// alertCertificateExpired is TLS alert 45 (RFC 8446 §6.2) — what a server sends when it rejects the client
// certificate as expired. Matching the ALERT CODE rather than only the message text keeps this from breaking on
// a stdlib rewording.
const alertCertificateExpired = 45

// CertExpiredRemedy recognises the one failure an agent cannot retry its way out of, and returns the operator
// instruction for it (S11 walk WF-S11-6).
//
// WHY THIS EXISTS. The agent's certificate lives for 48 hours and it renews at half-life over the mTLS channel
// — so a gateway that is merely SLOW to reconnect recovers on its own, and a gateway that was OFF longer than
// the certificate's lifetime never does: /agent/renew is behind the same client-cert requirement as every other
// agent endpoint, so the only door that could issue a new certificate needs the certificate that expired.
//
// Before this, that agent logged `remote error: tls: expired certificate` every five seconds, forever. Accurate,
// and it named neither the cause an operator would recognise ("the box was off over the weekend") nor the only
// action that helps. A retry loop with no exit that never says so is indistinguishable, in a log, from a
// transient network fault — so the operator waits for a recovery that cannot arrive.
func CertExpiredRemedy(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var alert tls.AlertError
	if errors.As(err, &alert) && uint8(alert) == alertCertificateExpired {
		return remedy, true
	}
	// Fallback: the alert can arrive already flattened into a message by an intermediate wrapper, in which
	// case errors.As finds nothing. Checked second so the structured path is the primary one.
	if strings.Contains(err.Error(), "tls: expired certificate") {
		return remedy, true
	}
	return "", false
}

const remedy = "this gateway's agent certificate has EXPIRED, so it can no longer authenticate to the control " +
	"plane — including the renewal endpoint, which requires the certificate that expired. Retrying will NOT " +
	"recover it. RE-ENROLL this gateway: create a join token in the control plane (Sites → the site → enroll a " +
	"gateway) and run the enrollment command on this host. Certificates last 48h and renew automatically while " +
	"the agent is running, so this state means the host was unreachable for longer than that."
