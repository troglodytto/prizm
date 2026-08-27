package crypto

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

const (
	keyringService = "prizm"
	keyringUser    = "db-key"
)

// LoadOrCreateKey returns the database key from the OS keychain, generating
// and storing a fresh one the first time prizm runs.
//
// This is intentionally untested: it talks to a real keychain, which is not
// available in CI. Everything downstream depends on the Cipher interface, so
// tests inject Plaintext instead.
func LoadOrCreateKey() ([]byte, error) {
	encoded, err := keyring.Get(keyringService, keyringUser)

	switch {
	case err == nil:
		key, decErr := base64.StdEncoding.DecodeString(encoded)
		if decErr != nil {
			return nil, fmt.Errorf("stored prizm key is corrupt: %w", decErr)
		}
		if len(key) != KeySize {
			return nil, fmt.Errorf("stored prizm key is %d bytes, want %d", len(key), KeySize)
		}
		return key, nil

	case errors.Is(err, keyring.ErrNotFound):
		key := make([]byte, KeySize)
		if _, rErr := rand.Read(key); rErr != nil {
			return nil, rErr
		}
		if sErr := keyring.Set(keyringService, keyringUser, base64.StdEncoding.EncodeToString(key)); sErr != nil {
			return nil, fmt.Errorf("storing prizm key in the OS keychain: %w", sErr)
		}
		return key, nil

	default:
		return nil, fmt.Errorf("reading prizm key from the OS keychain: %w", err)
	}
}
