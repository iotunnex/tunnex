package secrets

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tunnexio/tunnex/apps/api/internal/crypto"
)

func TestLoadOrInitGeneratesThenReuses(t *testing.T) {
	dir := t.TempDir()

	first, err := LoadOrInit(dir)
	if err != nil {
		t.Fatalf("first LoadOrInit: %v", err)
	}
	if !first.GeneratedAny {
		t.Fatal("expected GeneratedAny=true on first boot")
	}
	if len(first.MasterKey) != crypto.KeySize {
		t.Fatalf("master key len = %d, want %d", len(first.MasterKey), crypto.KeySize)
	}

	second, err := LoadOrInit(dir)
	if err != nil {
		t.Fatalf("second LoadOrInit: %v", err)
	}
	if second.GeneratedAny {
		t.Fatal("expected GeneratedAny=false on reuse — keys must not regenerate")
	}
	if Fingerprint(first.MasterKey) != Fingerprint(second.MasterKey) {
		t.Fatal("master key changed across loads — regeneration bug")
	}
}

func TestGeneratedSecretsAre0600(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrInit(dir); err != nil {
		t.Fatalf("LoadOrInit: %v", err)
	}
	for _, name := range []string{masterKeyFile, sessionSecretFile} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if perm := info.Mode().Perm(); perm != secretPerm {
			t.Fatalf("%s perms = %o, want %o", name, perm, secretPerm)
		}
	}
}

func TestFailsLoudOnMalformedMasterKey(t *testing.T) {
	dir := t.TempDir()
	// A present-but-garbage master key must never be silently replaced.
	if err := os.WriteFile(filepath.Join(dir, masterKeyFile), []byte("not-valid-base64!!!"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrInit(dir); err == nil {
		t.Fatal("expected LoadOrInit to fail on malformed master key, got nil")
	}
}

func TestFailsLoudOnWrongLengthMasterKey(t *testing.T) {
	dir := t.TempDir()
	// Valid base64 but only 16 bytes — wrong for AES-256; must fail, not regen.
	if err := os.WriteFile(filepath.Join(dir, masterKeyFile), []byte("AAAAAAAAAAAAAAAAAAAAAA=="), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrInit(dir); err == nil {
		t.Fatal("expected LoadOrInit to fail on wrong-length master key, got nil")
	}
}

func TestGeneratedMasterKeyWorksWithSealer(t *testing.T) {
	dir := t.TempDir()
	s, err := LoadOrInit(dir)
	if err != nil {
		t.Fatalf("LoadOrInit: %v", err)
	}
	sealer, err := crypto.NewSealer(s.MasterKey)
	if err != nil {
		t.Fatalf("NewSealer with bootstrapped key: %v", err)
	}
	if err := crypto.SelfTest(sealer); err != nil {
		t.Fatalf("SelfTest: %v", err)
	}
}
