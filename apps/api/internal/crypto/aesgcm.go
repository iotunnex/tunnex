// Package crypto provides authenticated encryption for data at rest.
//
// Every sensitive per-org value (starting with SSO/IdP client secrets in
// S2.3/S2.4) is sealed with AES-256-GCM under the bootstrap master key
// (see internal/secrets). GCM is authenticated, so tampering with stored
// ciphertext is detected on Open rather than silently decrypting to garbage.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

// KeySize is the master-key length for AES-256.
const KeySize = 32

// Sealer encrypts and decrypts values under a fixed master key.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer builds a Sealer from a 32-byte master key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts plaintext and returns base64(nonce || ciphertext || tag).
// A fresh random nonce is used per call.
func (s *Sealer) Seal(plaintext []byte) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal. It returns an error if the input is malformed or if the
// authentication tag does not verify (tampered or wrong key).
func (s *Sealer) Open(encoded string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode sealed value: %w", err)
	}
	ns := s.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("sealed value too short")
	}
	nonce, ciphertext := raw[:ns], raw[ns:]
	return s.aead.Open(nil, nonce, ciphertext, nil)
}

// SelfTest performs a seal/open round-trip at startup so a misconfigured or
// truncated master key fails loudly before any real data is written.
func SelfTest(s *Sealer) error {
	const probe = "tunnex-crypto-selftest"
	sealed, err := s.Seal([]byte(probe))
	if err != nil {
		return fmt.Errorf("selftest seal: %w", err)
	}
	out, err := s.Open(sealed)
	if err != nil {
		return fmt.Errorf("selftest open: %w", err)
	}
	if string(out) != probe {
		return errors.New("selftest round-trip mismatch")
	}
	return nil
}
