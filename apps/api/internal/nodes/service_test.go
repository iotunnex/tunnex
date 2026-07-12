package nodes

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tunnexio/tunnex/apps/api/db/sqlc"
	"github.com/tunnexio/tunnex/apps/api/internal/agentca"
	"github.com/tunnexio/tunnex/apps/api/internal/apierr"
	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
)

func genCSR(t *testing.T, cn string) string {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))
}

func serialOf(t *testing.T, ca *agentca.CA, certPEM string) string {
	t.Helper()
	blk, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	// The cert must chain to the CA.
	if _, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("issued cert does not verify against CA: %v", err)
	}
	return hex.EncodeToString(cert.SerialNumber.Bytes())
}

func code(err error) string {
	var a *apierr.Error
	if err != nil && errors.As(err, &a) {
		return a.Code
	}
	return ""
}

func TestNodeEnrollmentLifecycle(t *testing.T) {
	dsn := os.Getenv("TUNNEX_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TUNNEX_TEST_DATABASE_URL to run this integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New(tx)

	org, actor := uuid.New(), uuid.New()
	if _, err := tx.Exec(ctx, "INSERT INTO organizations (id,name,slug) VALUES ($1,$2,$3)", org, "O", "n-"+org.String()); err != nil {
		t.Fatalf("org: %v", err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users (id,email,name) VALUES ($1,$2,$3)", actor, "a@t", "A"); err != nil {
		t.Fatalf("actor: %v", err)
	}
	key := make([]byte, crypto.KeySize)
	_, _ = rand.Read(key)
	sealer, _ := crypto.NewSealer(key)
	ca, _, err := agentca.LoadOrCreate(ctx, q, sealer)
	if err != nil {
		t.Fatalf("ca: %v", err)
	}
	svc := &Service{q: q, ca: ca, sealer: sealer}

	// Issue a name-pinned token and enroll.
	raw, err := svc.IssueJoinToken(ctx, actor, org, "gw-1")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	res, err := svc.Enroll(ctx, raw, genCSR(t, "gw-1"), "gw-1", "0.1.0")
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	serial := serialOf(t, ca, res.CertPEM) // also verifies cert chains to CA

	// Audit correlation (S4.8/F3): the node.token_issued and node.enrolled rows
	// carry the SAME keyed token fingerprint, and neither carries the raw token.
	wantFP := sealer.Fingerprint([]byte(raw))
	for _, action := range []string{"node.token_issued", "node.enrolled"} {
		var metadata []byte
		if err := tx.QueryRow(ctx,
			"SELECT metadata FROM audit_logs WHERE org_id=$1 AND action=$2 ORDER BY created_at DESC LIMIT 1",
			org, action).Scan(&metadata); err != nil {
			t.Fatalf("audit row %s: %v", action, err)
		}
		var meta map[string]any
		if err := json.Unmarshal(metadata, &meta); err != nil {
			t.Fatalf("audit metadata %s: %v", action, err)
		}
		if fp, _ := meta["token_fingerprint"].(string); fp != wantFP {
			t.Fatalf("%s token_fingerprint: want %q, got %q (meta=%v)", action, wantFP, fp, meta)
		}
		if strings.Contains(string(metadata), raw) {
			t.Fatalf("%s metadata leaks the raw token", action)
		}
	}

	// Cert identity resolves to the node.
	node, err := svc.AuthenticateCert(ctx, serial)
	if err != nil || node.Name != "gw-1" {
		t.Fatalf("authenticate: node=%+v err=%v", node, err)
	}

	// Token is single-use.
	if _, err := svc.Enroll(ctx, raw, genCSR(t, "gw-1"), "gw-1", "0.1.0"); code(err) != "invalid_join_token" {
		t.Fatalf("token reuse: want invalid_join_token, got %v", err)
	}

	// Renewal of an active node issues a fresh cert (new serial).
	renewed, err := svc.Renew(ctx, node, genCSR(t, "gw-1"), "0.2.0")
	if err != nil {
		t.Fatalf("renew: %v", err)
	}
	newSerial := serialOf(t, ca, renewed)
	if newSerial == serial {
		t.Fatal("renewal did not rotate the serial")
	}
	node, err = svc.AuthenticateCert(ctx, newSerial)
	if err != nil {
		t.Fatalf("authenticate renewed: %v", err)
	}

	// WG key reporting: a malformed key is rejected; a well-formed 32-byte base64
	// key is stored on the active node.
	if err := svc.ReportWGInfo(ctx, node, "not-a-key", "", false, AppliedPolicy{}); code(err) != "invalid_wg_key" {
		t.Fatalf("malformed key: want invalid_wg_key, got %v", err)
	}
	wgKeyBytes := make([]byte, 32)
	wgKeyBytes[0] = 1 // non-zero: an all-zero key is a degenerate point (rejected)
	wgKey := base64.StdEncoding.EncodeToString(wgKeyBytes)
	if err := svc.ReportWGInfo(ctx, node, wgKey, "1.2.3.4:51820", true, AppliedPolicy{}); err != nil {
		t.Fatalf("report valid key: %v", err)
	}
	if stored, _ := q.GetNodeByCertSerial(ctx, newSerial); stored.WgPublicKey != wgKey || stored.Endpoint != "1.2.3.4:51820" {
		t.Fatalf("key/endpoint not persisted: %+v", stored)
	}
	if stored, _ := q.GetNodeByCertSerial(ctx, newSerial); !Capabilities(stored.Capabilities).EgressNAT {
		t.Fatalf("egress_nat capability not stored: %s", stored.Capabilities)
	}
	// A malformed endpoint (newline injection) is rejected.
	if err := svc.ReportWGInfo(ctx, node, wgKey, "1.2.3.4:51820\nInject = x", false, AppliedPolicy{}); code(err) != "invalid_endpoint" {
		t.Fatalf("injection endpoint: want invalid_endpoint, got %v", err)
	}
	// An empty endpoint report does NOT clobber the previously-stored good value.
	if err := svc.ReportWGInfo(ctx, node, wgKey, "", true, AppliedPolicy{}); err != nil {
		t.Fatalf("empty-endpoint report: %v", err)
	}
	if stored, _ := q.GetNodeByCertSerial(ctx, newSerial); stored.Endpoint != "1.2.3.4:51820" {
		t.Fatalf("empty report clobbered endpoint: got %q", stored.Endpoint)
	}

	// Revoke -> cert auth fails AND renewal is refused (the revocation mechanism).
	if err := svc.Revoke(ctx, actor, org, node.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := svc.AuthenticateCert(ctx, newSerial); code(err) != "agent_revoked" {
		t.Fatalf("authenticate revoked: want agent_revoked, got %v", err)
	}
	revoked, _ := q.GetNodeByCertSerial(ctx, newSerial)
	if _, err := svc.Renew(ctx, revoked, genCSR(t, "gw-1"), "0.3.0"); code(err) != "agent_revoked" {
		t.Fatalf("renew revoked: want agent_revoked, got %v", err)
	}
	// Reporting a key for a revoked node is a zero-row update -> surfaced as a
	// conflict, not a silent 204/no-op.
	if err := svc.ReportWGInfo(ctx, revoked, wgKey, "1.2.3.4:51820", false, AppliedPolicy{}); code(err) != "node_not_active" {
		t.Fatalf("report on revoked: want node_not_active, got %v", err)
	}

	// Versioned handshake.
	ds, err := svc.DesiredState(ctx, revoked)
	if err != nil || ds.ProtocolVersion != ProtocolVersion {
		t.Fatalf("desired-state version: %+v err=%v", ds, err)
	}
}
