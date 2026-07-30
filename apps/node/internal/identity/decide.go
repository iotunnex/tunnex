// Package identity decides, at agent boot, whether to use the stored gateway identity or enroll with a join
// token (EPIC 13 / S13.1, from EPIC 11 walk finding WF-S11-11).
//
// WHY THIS IS A PACKAGE AND NOT AN `if`. Before this, the choice was an inline branch in main.go: if credentials
// loaded, use them; otherwise enroll. Untestable, and wrong in one case that matters enormously — a stored
// identity whose certificate has EXPIRED. The agent preferred it, discarded a valid join token it had just been
// handed, logged a WARN, and looped forever on `remote error: tls: expired certificate`, because /agent/renew sits
// behind the same client-cert requirement as every other agent route. The operator did exactly what the docs
// prescribe and the product silently ignored them.
//
// The EPIC 11 walk also taught why the decision must be a pure function: a check written as an inline condition
// cannot be red-tested, and a red that cannot fail is worse than none (docs/laws.md, COULD THIS CHECK HAVE
// FAILED?). Every branch below is reachable from a test.
package identity

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"
)

// Action is what the agent should do with the identity it found (or failed to find).
type Action int

const (
	// UseStored — proceed with the stored certificate. The DEFAULT and the safe direction.
	UseStored Action = iota
	// UseToken — enroll fresh with the join token, replacing whatever is stored.
	UseToken
	// Idle — there is no usable identity AND no token. Stay up (liveness) but not ready, and say why.
	Idle
)

// Verdict is the decision plus the evidence for it. Evidence is not decoration: the ruling on WF-S11-11 requires
// that "unusable" be a DETERMINATION rather than an assumption, so the agent must be able to say what made it so.
// An operator reading the log needs to distinguish "your certificate expired three days ago" from "I could not
// parse your certificate" — the causes differ and so do the remedies.
type Verdict struct {
	Action   Action
	Reason   string // short, stable, machine-greppable
	Evidence string // the specific fact behind Reason, for a human
	StoredCN string // the common name of the STORED identity; "" when none/unparseable
	// NameMismatch: the stored certificate names a different node than TUNNEX_NODE_NAME requested (WF-S11-11b).
	// Reported SEPARATELY from Action, because on its own it must never authorize discarding a working identity.
	NameMismatch bool
}

// Decide is the whole rule. now is injected so expiry is testable.
//
// THE SAFE DIRECTION IS `UseStored`, and every uncertain case resolves there. That is not timidity: enrolling
// with a token abandons the stored identity, and abandoning a LIVE gateway's identity makes it appear dead to the
// control plane while a second node takes its name. The S8.2c WF-2 incident (a re-used VM silently keeping its
// old identity and org) is why the stored identity is preferred at all; this function narrows that preference to
// the cases where the stored identity can actually still work.
func Decide(certPEM []byte, loadErr error, requestedName string, haveToken bool, now time.Time) Verdict {
	// 1. NOTHING STORED. The original bootstrap case.
	if loadErr != nil || len(certPEM) == 0 {
		if haveToken {
			return Verdict{Action: UseToken, Reason: "no_stored_identity",
				Evidence: "no credentials in the state directory; enrolling with the join token"}
		}
		return Verdict{Action: Idle, Reason: "no_identity_no_token",
			Evidence: "no credentials in the state directory and TUNNEX_JOIN_TOKEN is unset — " +
				"provide a join token to enroll this gateway"}
	}

	leaf, parseErr := parseLeaf(certPEM)

	// 2. STORED BUT UNREADABLE. Nothing can be recovered from it, so a token loses nothing.
	if parseErr != nil {
		if haveToken {
			return Verdict{Action: UseToken, Reason: "stored_identity_unreadable",
				Evidence: "stored certificate could not be parsed (" + parseErr.Error() +
					"); enrolling with the join token"}
		}
		return Verdict{Action: Idle, Reason: "stored_identity_unreadable_no_token",
			Evidence: "stored certificate could not be parsed (" + parseErr.Error() +
				") and no join token was supplied — this gateway must be re-enrolled"}
	}

	cn := leaf.Subject.CommonName
	mismatch := requestedName != "" && cn != "" && requestedName != cn
	expired := now.After(leaf.NotAfter)

	// 3. EXPIRED. The case this package exists for. /agent/renew requires the certificate that expired, so no
	//    amount of waiting recovers it — a token is the only way forward, and the stored identity is worthless.
	if expired {
		age := now.Sub(leaf.NotAfter).Round(time.Minute)
		ev := fmt.Sprintf("stored certificate for %q expired %s ago (NotAfter %s)",
			cn, age, leaf.NotAfter.UTC().Format(time.RFC3339))
		if haveToken {
			return Verdict{Action: UseToken, Reason: "stored_identity_expired", StoredCN: cn,
				NameMismatch: mismatch,
				Evidence:     ev + "; enrolling with the join token — renewal is impossible once the certificate has lapsed"}
		}
		return Verdict{Action: Idle, Reason: "stored_identity_expired_no_token", StoredCN: cn,
			NameMismatch: mismatch,
			Evidence: ev + " and no join token was supplied. This gateway CANNOT recover on its own: the " +
				"renewal endpoint requires the certificate that expired. Re-enroll it — mint a join token in " +
				"the control plane and restart this agent with TUNNEX_JOIN_TOKEN set"}
	}

	// 4. NAME MISMATCH ON A STILL-VALID CERTIFICATE — loud, but it does NOT authorize re-enrollment.
	//
	//    This is the case where the literal reading of "unusable" would be actively destructive. On the EPIC 11
	//    walk the enrollment command was pasted on the WRONG HOST: azure-gw, carrying azure-gw's valid identity,
	//    with TUNNEX_NODE_NAME=aws-gw-1. Had a mismatch authorized using the token, the agent would have
	//    abandoned azure-gw's identity and enrolled that host as aws-gw-1 — a working gateway made to look dead,
	//    which is exactly the S8.2c WF-2 disaster the stored-identity preference was built to prevent.
	//
	//    So: keep the identity, and shout. A mismatch is almost always operator error (a mis-set env var, a
	//    cloned VM image, a command run on the wrong box) and the operator, not the agent, must resolve it.
	if mismatch {
		return Verdict{Action: UseStored, Reason: "stored_identity_name_mismatch", StoredCN: cn,
			NameMismatch: true,
			Evidence: fmt.Sprintf("stored certificate is for %q but TUNNEX_NODE_NAME requests %q — KEEPING the "+
				"stored identity because its certificate is still valid. If you meant to re-enroll this host as "+
				"%q, wipe the state directory first; if you are on the wrong host, stop this agent",
				cn, requestedName, requestedName)}
	}

	// 5. A valid stored identity that matches. The overwhelmingly common path, and a token present alongside it
	//    is ignored ON PURPOSE — that is the WF-2 protection, still intact.
	return Verdict{Action: UseStored, Reason: "stored_identity_valid", StoredCN: cn,
		Evidence: fmt.Sprintf("stored certificate for %q is valid until %s",
			cn, leaf.NotAfter.UTC().Format(time.RFC3339))}
}

// EffectiveName is the name the agent should present. It comes from the STORED CERTIFICATE whenever one is being
// used (WF-S11-11b): `nodeName` was read from TUNNEX_NODE_NAME and never reconciled with the certificate, so the
// reuse warning reported the name the operator ASKED for rather than the identity actually kept. On the walk that
// printed `node_name: aws-gw-1` while reusing azure-gw's certificate — the diagnostic that exists to reveal which
// identity is in use named the one that was not.
func EffectiveName(v Verdict, requestedName string) string {
	if v.Action == UseStored && v.StoredCN != "" {
		return v.StoredCN
	}
	return requestedName
}

func parseLeaf(certPEM []byte) (*x509.Certificate, error) {
	blk, _ := pem.Decode(certPEM)
	if blk == nil {
		return nil, fmt.Errorf("no PEM block found")
	}
	return x509.ParseCertificate(blk.Bytes)
}
