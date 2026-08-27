// Package crypto encrypts variable values at rest.
//
// prizm encrypts values but not names: a leaked database file must not leak
// secrets, while group, repo, workflow and variable names stay queryable so
// shell completion never has to decrypt anything.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// KeySize is the required key length in bytes (AES-256).
const KeySize = 32

// Cipher encrypts and decrypts single variable values.
type Cipher interface {
	Encrypt(plaintext string) ([]byte, error)
	Decrypt(blob []byte) (string, error)
}

type aesgcm struct {
	aead cipher.AEAD
}

// NewAESGCM returns a Cipher using AES-256-GCM. key must be KeySize bytes.
func NewAESGCM(key []byte) (Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &aesgcm{aead: aead}, nil
}

// Encrypt returns nonce || ciphertext. A fresh nonce per call means the same
// value never encrypts to the same bytes twice.
func (c *aesgcm) Encrypt(plaintext string) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(plaintext), nil), nil
}

// Decrypt reverses Encrypt, and fails if the blob was modified.
func (c *aesgcm) Decrypt(blob []byte) (string, error) {
	n := c.aead.NonceSize()
	if len(blob) < n {
		return "", errors.New("ciphertext too short")
	}

	out, err := c.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(out), nil
}

// Plaintext is a no-op Cipher for tests. Never use it in production wiring.
type Plaintext struct{}

// Encrypt returns the value unchanged.
func (Plaintext) Encrypt(plaintext string) ([]byte, error) { return []byte(plaintext), nil }

// Decrypt returns the blob unchanged.
func (Plaintext) Decrypt(blob []byte) (string, error) { return string(blob), nil }
