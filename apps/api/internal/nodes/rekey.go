package nodes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/rekey"
)

// challengeTTL bounds a re-key nonce's life. Short because the agent fetches it and immediately signs — minutes,
// not hours — and every second of validity is a second a captured challenge could be replayed against.
const challengeTTL = 2 * time.Minute

// ErrRekeyRefused is the ONE error every re-key failure returns, and the uniformity is the point (D8).
//
// A live node, a nonexistent serial, an expired nonce, a replayed nonce, a malformed CSR and a wrong-key signature
// are all indistinguishable to the caller. Anything finer turns an unauthenticated endpoint into an oracle — for
// whether a serial is known, for whether a gateway is alive, for whether a key guess was close. The specific reason
// goes to the log, where an operator can read it and an attacker cannot.
var ErrRekeyRefused = apierr.New(http.StatusForbidden, "rekey_refused",
	"re-key refused. If this gateway was revoked, recover it with a join token instead.")

// IssueRekeyChallenge mints a single-use nonce for a certificate serial.
//
// IT DOES NOT CHECK THAT THE SERIAL EXISTS. A challenge that succeeded only for known serials would be an
// enumeration oracle, and node certificate serials would then be probeable one request at a time. So the nonce is
// minted and recorded for whatever serial is asked about; a serial nobody has fails at SUBMIT, with the same
// uniform refusal as every other failure. Flood protection is the endpoint's rate limit, not this function.
func (s *Service) IssueRekeyChallenge(ctx context.Context, certSerial string) ([]byte, error) {
	if certSerial == "" {
		return nil, ErrRekeyRefused
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	if err := s.q.CreateRekeyChallenge(ctx, sqlc.CreateRekeyChallengeParams{
		Nonce:      nonce,
		CertSerial: certSerial,
		ExpiresAt:  time.Now().Add(challengeTTL),
	}); err != nil {
		return nil, err
	}
	return nonce, nil
}

// Rekey issues a fresh certificate for an EXISTING node, authenticated by proof of possession of the keypair the
// control plane already recorded for it (S13.1 D1(c) + D2 + D3 + D9).
//
// THE ORDER OF OPERATIONS IS THE SECURITY DESIGN, not an implementation detail:
//
//  1. CONSUME the nonce — single-use, so a captured request cannot be replayed. Consumed even when the attempt then
//     fails, so a probe cannot retry with the same challenge.
//  2. RESOLVE the node by serial.
//  3. GATE (D3) — BEFORE any cryptographic work. RSA verification is the expensive, timing-visible step; running it
//     before the gate would let response latency reveal whether a node is alive. Expiry authorizes; revocation
//     REFUSES (a proof of possession must never overturn a human decision — see RekeyAuthorized).
//  4. VERIFY the proof against the recorded public key, bound to (nonce ‖ CSR).
//  5. SIGN the new CSR.
//  6. COMMIT the identity change and its audited succession in ONE transaction.
//  7. PUSH — AFTER the commit, never inside it.
//
// WHY THE PUSH IS OUTSIDE THE TRANSACTION. A database transaction must not depend on a network call to a fleet.
// Inside, the transaction's success is hostage to gateway reachability: a slow or partitioned agent holds a write
// lock on the node row, and a failed push rolls back a re-key that already succeeded cryptographically. Outside, the
// CP's record is authoritative the instant it commits and the push is a reconciliation that retries — which is what
// every other desired-state change in this product already does.
//
// THE WINDOW THIS LEAVES, STATED HONESTLY: between commit and push the control plane believes the new key and the
// fleet has not been told. The recovering gateway's own next reconcile closes it for itself; other gateways converge
// on the push, or on their next poll if the push is lost. A lost push is a DELAYED convergence, never a lost one.
func (s *Service) Rekey(ctx context.Context, certSerial string, nonce, csrPEM, signature []byte, agentVersion string) (certPEM, caPEM string, err error) {
	log := slog.With("op", "rekey", "cert_serial", certSerial)

	// (1) Single-use nonce. The UPDATE's own WHERE enforces it, so two concurrent submits cannot both win.
	if _, e := s.q.ConsumeRekeyChallenge(ctx, sqlc.ConsumeRekeyChallengeParams{Nonce: nonce, CertSerial: certSerial}); e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			log.Warn("rekey_refused", "reason", "challenge unknown, expired, already used, or bound to another serial")
			return "", "", ErrRekeyRefused
		}
		return "", "", e
	}

	// (2) Resolve.
	node, e := s.q.GetNodeByCertSerial(ctx, certSerial)
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) {
			log.Warn("rekey_refused", "reason", "no node holds this certificate serial")
			return "", "", ErrRekeyRefused
		}
		return "", "", e
	}
	log = log.With("node_id", node.ID.String(), "org_id", node.OrgID.String())

	// (3) GATE FIRST — before any cryptographic work, so timing cannot become a liveness oracle.
	authorized, why := RekeyAuthorized(node.Status, node.CertNotAfter.Time, node.CertNotAfter.Valid, time.Now())
	if !authorized {
		log.Warn("rekey_refused", "reason", why)
		return "", "", ErrRekeyRefused
	}

	// (4) Proof of possession, bound to this exact CSR and nonce.
	recorded := ""
	if node.CertPublicKey != nil {
		recorded = *node.CertPublicKey
	}
	if e := rekey.Verify(recorded, nonce, csrPEM, signature); e != nil {
		log.Warn("rekey_refused", "reason", e.Error())
		return "", "", ErrRekeyRefused
	}

	// (5) Sign.
	iss, e := s.ca.SignCSR(csrPEM, node.Name)
	if e != nil {
		log.Warn("rekey_refused", "reason", "CSR could not be signed: "+e.Error())
		return "", "", ErrRekeyRefused
	}

	// (6) ONE transaction: the identity change AND its audit row. The audit commits WITH the change — a re-key that
	//     happened must leave a record even if the push never lands.
	oldFP, newFP := keyFingerprint(recorded), keyFingerprint(base64.StdEncoding.EncodeToString(iss.PublicKeySPKI))
	if e := s.withTx(ctx, func(q *sqlc.Queries) error {
		updated, ue := q.RekeyNode(ctx, sqlc.RekeyNodeParams{
			ID:            node.ID,
			CertSerial:    iss.Serial,
			CertPublicKey: spkiText(iss.PublicKeySPKI),
			CertNotAfter:  pgtype.Timestamptz{Time: iss.NotAfter, Valid: true},
			AgentVersion:  agentVersion,
			CertSerial_2:  certSerial,
		})
		if ue != nil {
			if errors.Is(ue, pgx.ErrNoRows) {
				// The row moved under us — revoked, or already re-keyed by a concurrent request. Refusing is
				// correct: the decision in (3) was made about a state that no longer holds.
				return ErrRekeyRefused
			}
			return ue
		}
		// A SUCCESSION, not a mutation: one node whose credential changed, with both key fingerprints, so
		// "this gateway was rebuilt on the 4th" is answerable later. actor_system because no human was present —
		// the caller is the gateway itself, proving possession.
		//
		// NOTE for S11-6 (audit-action unification): `node.rekeyed` is added in the EXISTING style deliberately.
		// Inventing a parallel mechanism now would hand that story fifteen helpers to collapse instead of
		// fourteen, with the newest one as the exception.
		return audit(ctx, q, node.OrgID, nil, "node.rekeyed", "node", node.ID.String(), map[string]any{
			"old_cert_serial":     certSerial,
			"new_cert_serial":     updated.CertSerial,
			"old_key_fingerprint": oldFP,
			"new_key_fingerprint": newFP,
			"authorized_by":       why,
		})
	}); e != nil {
		return "", "", e
	}
	log.Info("node_rekeyed", "new_cert_serial", iss.Serial, "old_key_fingerprint", oldFP,
		"new_key_fingerprint", newFP, "authorized_by", why)

	// (7) AFTER commit. The WireGuard public key will change on the agent's next report, so every peer's AllowedIPs
	//     and every site link must reconcile — a full sweep, not a field update.
	if s.pushOrg != nil {
		s.pushOrg(ctx, node.OrgID)
	}
	return iss.CertPEM, string(s.ca.CertPEM()), nil
}

// keyFingerprint renders a short, non-reversible id for a recorded public key, for audit and logs. Public keys are
// not secret, so this is for READABILITY rather than protection — a 12-hex prefix is comparable at a glance where a
// 392-character base64 blob is not.
func keyFingerprint(spkiB64 string) string {
	if spkiB64 == "" {
		return "none"
	}
	sum := sha256.Sum256([]byte(spkiB64))
	return hex.EncodeToString(sum[:6])
}
