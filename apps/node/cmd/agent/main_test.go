package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnexio/tunnex/apps/node/internal/control"
)

// TestLoadOrCreateWGKey covers the node-side re-key flow (watch-item a): the key
// is generated locally and persisted; a reload returns the SAME key (stable
// pubkey to report); deleting the file re-keys (new private key, new pubkey).
func TestLoadOrCreateWGKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wg.key")

	priv1, pub1, err := loadOrCreateWGKey(path)
	if err != nil {
		t.Fatalf("first generate: %v", err)
	}
	if priv1 == "" || pub1 == "" {
		t.Fatal("empty key material")
	}

	// Reload must be stable — same private key, same public key to report.
	priv2, pub2, err := loadOrCreateWGKey(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if priv2 != priv1 || pub2 != pub1 {
		t.Fatal("reload changed the key: pubkey would spuriously re-report")
	}

	// Re-key: losing the file yields a fresh key (private AND public differ).
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	priv3, pub3, err := loadOrCreateWGKey(path)
	if err != nil {
		t.Fatalf("re-key: %v", err)
	}
	if priv3 == priv1 || pub3 == pub1 {
		t.Fatal("re-key produced the same key — new pubkey must be reported after key loss")
	}
}

// TestPendingKeyIsPersistedBeforeAnySubmit — the half of D10 that lives on the agent, and the half that makes the
// fingerprint identifier usable at all.
//
// A key that exists only in memory when the re-key request goes out is a key this agent cannot prove possession of if
// the response is lost — and the control plane will by then have RECORDED it. So the mint is a mint-and-persist, and
// a second call must return the SAME key rather than a fresh one: a fresh key per attempt would walk the identity
// forward on every retry, leaving the agent proving possession of something the control plane never saw. That is the
// same brick by a longer route.
func TestPendingKeyIsPersistedBeforeAnySubmit(t *testing.T) {
	dir := t.TempDir()

	first, wasOnDisk, err := loadOrCreatePendingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if wasOnDisk {
		t.Fatal("a freshly minted key must report wasOnDisk=false — that flag is what decides whether the " +
			"fingerprint identity is worth trying, and on a first attempt the control plane cannot possibly hold it")
	}
	path := filepath.Join(dir, pendingKeyFile)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("the pending key must be ON DISK before any request is built: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("pending key mode is %v, want 0600 — it is private key material like key.pem", fi.Mode().Perm())
	}

	second, wasOnDisk, err := loadOrCreatePendingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !wasOnDisk {
		t.Fatal("an existing pending key must report wasOnDisk=true")
	}
	if string(second) != string(first) {
		t.Fatal("the pending key must be REUSED across attempts. A fresh key each time means that after a lost " +
			"response the agent holds a key the control plane never recorded — so neither identifier resolves, " +
			"which is the brick D10 exists to remove")
	}

	// It must NOT be mistakable for the real identity: loadCreds and identity.Decide read cert.pem/key.pem, and a
	// pending key that landed in key.pem would be a key with no matching certificate.
	if _, err := os.Stat(filepath.Join(dir, "key.pem")); !os.IsNotExist(err) {
		t.Fatal("minting a pending key must not create key.pem — only a promotion may")
	}
}

// TestUnreadablePendingKeyIsReplacedNotCarried — garbage in the pending file would be submitted, refused, and read
// exactly like a control-plane refusal, sending the operator to look at the wrong side of the wire.
func TestUnreadablePendingKeyIsReplacedNotCarried(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, pendingKeyFile), []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, wasOnDisk, err := loadOrCreatePendingKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	if wasOnDisk {
		t.Fatal("unusable material must not be reported as a key an earlier attempt submitted — the control plane " +
			"cannot be holding it, and trying its fingerprint spends a challenge to learn nothing")
	}
	if _, ferr := control.KeyFingerprintFromPEM(key); ferr != nil {
		t.Fatalf("the replacement must be a usable key: %v", ferr)
	}
}

// TestSaveCredsClearsThePendingKey — promotion is the end of the pending key's life, whichever path got there
// (re-key, renewal, or join-token enrolment; all three go through saveCreds).
//
// A superseded pending key left on disk would make the NEXT recovery try its fingerprint first, spend a challenge and
// a refusal on an identifier the control plane does not hold, and log a refusal that says nothing about the cause.
func TestSaveCredsClearsThePendingKey(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := loadOrCreatePendingKey(dir); err != nil {
		t.Fatal(err)
	}
	if err := saveCreds(dir, []byte("cert"), []byte("key"), []byte("ca")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, pendingKeyFile)); !os.IsNotExist(err) {
		t.Fatal("saveCreds must clear the pending key: it has been superseded by a real identity")
	}
	for _, f := range []string{"cert.pem", "key.pem", "ca.pem"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s must exist after a promotion: %v", f, err)
		}
	}
}
