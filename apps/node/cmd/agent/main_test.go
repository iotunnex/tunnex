package main

import (
	"os"
	"path/filepath"
	"testing"
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
