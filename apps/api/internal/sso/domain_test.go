package sso

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
)

type fakeDNS struct{ records map[string][]string }

func (f *fakeDNS) LookupTXT(_ context.Context, name string) ([]string, error) {
	return f.records[name], nil
}

func TestDomainCaptureSecurityProperties(t *testing.T) {
	h := newFlowHarness(t) // gives us a rolled-back tx + a real org
	dns := &fakeDNS{records: map[string][]string{}}
	svc := &DomainService{q: h.q, dns: dns}

	// A verified admin at acme.com.
	admin := uuid.New()
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", admin, "admin@acme.com", "Admin"); err != nil {
		t.Fatalf("admin: %v", err)
	}

	// (b) public domains are refused.
	if _, err := svc.CreateClaim(h.ctx, admin, "admin@acme.com", true, h.org, "gmail.com"); code(err) != "public_domain" {
		t.Fatalf("public domain: want public_domain, got %v", err)
	}
	// (b) inversion guard: claimer must have a verified account at the domain.
	if _, err := svc.CreateClaim(h.ctx, admin, "admin@other.com", true, h.org, "acme.com"); code(err) != "domain_ownership_required" {
		t.Fatalf("wrong-domain admin: want domain_ownership_required, got %v", err)
	}
	if _, err := svc.CreateClaim(h.ctx, admin, "admin@acme.com", false, h.org, "acme.com"); code(err) != "domain_ownership_required" {
		t.Fatalf("unverified admin: want domain_ownership_required, got %v", err)
	}

	// Valid claim returns a TXT record to publish.
	txt, err := svc.CreateClaim(h.ctx, admin, "admin@acme.com", true, h.org, "acme.com")
	if err != nil {
		t.Fatalf("create claim: %v", err)
	}
	if !strings.HasPrefix(txt, "tunnex-verify=") {
		t.Fatalf("unexpected txt record: %q", txt)
	}

	// (a) verify fails without the TXT record...
	if err := svc.Verify(h.ctx, admin, h.org, "acme.com"); code(err) != "verification_failed" {
		t.Fatalf("verify without record: want verification_failed, got %v", err)
	}
	// ...and succeeds once it's present.
	dns.records["acme.com"] = []string{txt}
	if err := svc.Verify(h.ctx, admin, h.org, "acme.com"); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// Capture now resolves for that domain.
	if org, ok := svc.CapturingOrgForEmail(h.ctx, "bob@acme.com"); !ok || org != h.org {
		t.Fatalf("capture should resolve to org, got %v ok=%v", org, ok)
	}
	// (a) verification loss suspends capture (record removed / domain changed hands).
	delete(dns.records, "acme.com")
	if _, ok := svc.CapturingOrgForEmail(h.ctx, "bob@acme.com"); ok {
		t.Fatal("capture should be suspended after TXT record disappears")
	}
	// The claim is suspended (verified_at cleared), not deleted.
	claim, err := svc.q.GetDomainClaim(h.ctx, sqlc.GetDomainClaimParams{OrgID: h.org, Domain: "acme.com"})
	if err != nil || claim.VerifiedAt.Valid {
		t.Fatalf("claim should be suspended (verified_at NULL), got valid=%v err=%v", claim.VerifiedAt.Valid, err)
	}
}

// TestDomainCaptureUniquenessRace proves the DB partial-unique index resolves a
// concurrent verify of the same domain: the second org loses cleanly.
func TestDomainCaptureUniquenessRace(t *testing.T) {
	h := newFlowHarness(t)
	dns := &fakeDNS{records: map[string][]string{}}
	svc := &DomainService{q: h.q, dns: dns}

	// Second org + an admin at the shared domain.
	orgB := uuid.New()
	admin := uuid.New()
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", orgB, "B", "b-"+orgB.String()); err != nil {
		t.Fatalf("orgB: %v", err)
	}
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", admin, "admin@shared.com", "Admin"); err != nil {
		t.Fatalf("admin: %v", err)
	}

	txtA, _ := svc.CreateClaim(h.ctx, admin, "admin@shared.com", true, h.org, "shared.com")
	txtB, _ := svc.CreateClaim(h.ctx, admin, "admin@shared.com", true, orgB, "shared.com")
	// Both tokens published (both would pass the TXT check).
	dns.records["shared.com"] = []string{txtA, txtB}

	if err := svc.Verify(h.ctx, admin, h.org, "shared.com"); err != nil {
		t.Fatalf("org A verify: %v", err)
	}
	// Org B loses at the partial-unique index.
	if err := svc.Verify(h.ctx, admin, orgB, "shared.com"); code(err) != "domain_taken" {
		t.Fatalf("org B verify: want domain_taken, got %v", err)
	}
}

// TestDomainCaptureAutoJoinThroughFlow proves an SSO login auto-joins the
// capturing org (in addition to the login org), routed AFTER DecideLink.
func TestDomainCaptureAutoJoinThroughFlow(t *testing.T) {
	h := newFlowHarness(t)
	dns := &fakeDNS{records: map[string][]string{}}
	h.svc.domains = &DomainService{q: h.q, dns: dns}

	// Org B captures captured.com (seed a verified claim directly + publish TXT).
	orgB := uuid.New()
	token := "capture-token"
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", orgB, "B", "b-"+orgB.String()); err != nil {
		t.Fatalf("orgB: %v", err)
	}
	if _, err := h.tx.Exec(h.ctx,
		"INSERT INTO domain_claims (org_id,domain,verification_token,verified_at) VALUES ($1,$2,$3,now())",
		orgB, "captured.com", token); err != nil {
		t.Fatalf("claim: %v", err)
	}
	dns.records["captured.com"] = []string{"tunnex-verify=" + token}

	state, nonce := h.start(t)
	h.idp.mint(h.idp.key, map[string]any{
		"sub": "g-1", "email": "worker@captured.com", "email_verified": true, "nonce": nonce,
	})
	userID, err := h.svc.HandleCallback(h.ctx, "google", "code", state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	// Member of the login org (flow) AND the capturing org B.
	if _, err := h.q.GetMembership(h.ctx, sqlc.GetMembershipParams{OrgID: h.org, UserID: userID}); err != nil {
		t.Fatalf("login-org membership missing: %v", err)
	}
	if _, err := h.q.GetMembership(h.ctx, sqlc.GetMembershipParams{OrgID: orgB, UserID: userID}); err != nil {
		t.Fatalf("captured-org membership missing: %v", err)
	}
}

// TestReclaimIsRecoveryNotAFalseCrossTenantAccusation pins the S14.13 finding.
//
// ⛔ EVERY `domain_taken` AN OPERATOR COULD SEE ON THE CLAIM PATH WAS FALSE. mapClaimErr
// mapped any 23505 to "already captured by another organization", but the two unique indexes
// here mean opposite things and only one of them is reachable from CreateClaim:
//
//	domain_claims_org_domain_key       UNIQUE (org_id, domain)                        <- same org
//	domain_claims_verified_domain_key  UNIQUE (domain) WHERE verified_at IS NOT NULL   <- cross-org
//
// A fresh insert has verified_at = NULL and so is not in the partial index at all. This test
// proves that directly (arm 2), which is why the old message was never true here.
//
// The weight of the finding is the INTERACTION: with no GET, re-claiming is the only way back
// to a lost TXT value, so the write-only-state gap MANUFACTURED the false accusation at the
// exact moment an admin was recovering. Arm 1 is therefore both the fix and the recovery.
func TestReclaimIsRecoveryNotAFalseCrossTenantAccusation(t *testing.T) {
	h := newFlowHarness(t)
	dns := &fakeDNS{records: map[string][]string{}}
	svc := &DomainService{q: h.q, dns: dns}

	admin := uuid.New()
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", admin, "admin@reclaim.com", "Admin"); err != nil {
		t.Fatalf("admin: %v", err)
	}

	// ARM 1 — SAME ORG re-claims: returns the EXISTING token, never an error.
	first, err := svc.CreateClaim(h.ctx, admin, "admin@reclaim.com", true, h.org, "reclaim.com")
	if err != nil {
		t.Fatalf("first claim: %v", err)
	}
	again, err := svc.CreateClaim(h.ctx, admin, "admin@reclaim.com", true, h.org, "reclaim.com")
	if err != nil {
		t.Fatalf("re-claim must be a RECOVERY, not a refusal: %v", err)
	}
	if again != first {
		t.Fatalf("re-claim must return the SAME token (it is the only way back to a lost TXT value):\n first=%q\n again=%q", first, again)
	}
	// The exact regression, named: this path must never accuse another tenant.
	if code(err) == "domain_taken" {
		t.Fatal("re-claim returned domain_taken — the false cross-tenant accusation is back")
	}

	// ARM 2 — the measured surprise, pinned: a SECOND ORG can CREATE a claim on a domain that
	// another org has already VERIFIED, because the partial index only covers verified rows.
	// This is why the old message could never have been true on the claim path.
	orgB := uuid.New()
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", orgB, "B", "b-"+orgB.String()); err != nil {
		t.Fatalf("orgB: %v", err)
	}
	dns.records["reclaim.com"] = []string{first}
	if err := svc.Verify(h.ctx, admin, h.org, "reclaim.com"); err != nil {
		t.Fatalf("org A verify: %v", err)
	}
	txtB, err := svc.CreateClaim(h.ctx, admin, "admin@reclaim.com", true, orgB, "reclaim.com")
	if err != nil {
		t.Fatalf("a second org must still be able to CREATE a claim (only VERIFY is globally unique): %v", err)
	}

	// ARM 3 — and the genuinely-taken path STILL says taken, at the seam that owns it.
	// Org B's OWN token must be published, or Verify refuses at the DNS check and never
	// reaches the index — which would make this arm pass for the wrong reason.
	dns.records["reclaim.com"] = []string{first, txtB}
	if err := svc.Verify(h.ctx, admin, orgB, "reclaim.com"); code(err) != "domain_taken" {
		t.Fatalf("org B verify: want domain_taken, got %v", err)
	}
}

// TestSuspendWritesASystemAuditRowAgainstTheCAPTURINGOrg pins the second of the two
// destructive-and-silent verbs (S14.15; the first was idpsync.UnmapGroup).
//
// ⛔ SUSPENDING A CAPTURE IS NOT A HUMAN ACTION AND IT IS CROSS-ORG. It fires from a SIGNUP, so the
// principal in context is the person joining — who did not do this. And the row belongs to the
// CAPTURING org, not to the signup. Both are easy to get wrong in a way no user would ever report:
// the evidence would simply be filed under the wrong actor, or the wrong tenant.
func TestSuspendWritesASystemAuditRowAgainstTheCAPTURINGOrg(t *testing.T) {
	h := newFlowHarness(t)
	dns := &fakeDNS{records: map[string][]string{}}
	svc := &DomainService{q: h.q, dns: dns}

	admin := uuid.New()
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", admin, "admin@suspend.com", "Admin"); err != nil {
		t.Fatalf("admin: %v", err)
	}
	txt, err := svc.CreateClaim(h.ctx, admin, "admin@suspend.com", true, h.org, "suspend.com")
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	dns.records["suspend.com"] = []string{txt}
	if err := svc.Verify(h.ctx, admin, h.org, "suspend.com"); err != nil {
		t.Fatalf("verify: %v", err)
	}

	// A SECOND org exists and must NOT receive the audit row — the discriminating case for the
	// cross-org write. Without it, "an audit row exists somewhere" would pass a wrong implementation.
	orgB := uuid.New()
	if _, err := h.tx.Exec(h.ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", orgB, "B", "b-"+orgB.String()); err != nil {
		t.Fatalf("orgB: %v", err)
	}

	// The record disappears from DNS: the next signup lookup must suspend the capture.
	delete(dns.records, "suspend.com")
	if _, ok := svc.CapturingOrgForEmail(h.ctx, "someone@suspend.com"); ok {
		t.Fatal("capture must be refused once the TXT record is gone")
	}

	var n int
	var actorSystem *string
	var actorUser *uuid.UUID
	var meta []byte
	if err := h.tx.QueryRow(h.ctx,
		`SELECT count(*), max(actor_system), max(actor_user_id::text)::uuid, max(metadata::text)::jsonb
		   FROM audit_logs WHERE org_id = $1 AND action = 'domain.capture_suspended'`,
		h.org).Scan(&n, &actorSystem, &actorUser, &meta); err != nil {
		t.Fatalf("audit read: %v", err)
	}
	if n != 1 {
		t.Fatalf("the capturing org must get exactly ONE suspend audit row, got %d", n)
	}
	// ⛔ NAMED SYSTEM ACTOR, AND NO HUMAN ACTOR. Recording the signing-up user here would blame a
	// person for something they did not do.
	if actorSystem == nil || *actorSystem != "domain-capture" {
		t.Fatalf("actor_system must NAME the system actor, got %v", actorSystem)
	}
	if actorUser != nil {
		t.Fatalf("actor_user_id must be NULL — nobody did this; got %v", actorUser)
	}
	// The CAUSE is the point: "suspended" alone sends an operator hunting for a person.
	if !strings.Contains(string(meta), "txt_record_missing") {
		t.Fatalf("metadata must carry the CAUSE, got %s", meta)
	}

	// ⛔ THE CROSS-ORG HALF. The other org must have nothing.
	var other int
	if err := h.tx.QueryRow(h.ctx,
		`SELECT count(*) FROM audit_logs WHERE org_id = $1 AND action = 'domain.capture_suspended'`,
		orgB).Scan(&other); err != nil {
		t.Fatalf("audit read (orgB): %v", err)
	}
	if other != 0 {
		t.Fatalf("the suspend row must be filed against the CAPTURING org only; orgB got %d", other)
	}
}
