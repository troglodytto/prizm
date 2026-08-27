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
			return nil, keychainError("storing", sErr)
		}
		return key, nil

	default:
		return nil, keychainError("reading", err)
	}
}

// keychainError explains what the keychain is for and why it might be absent.
//
// The underlying failure is usually something like "dbus: invalid bus address",
// which is accurate and tells a person nothing. This is the error people hit
// on a server over SSH or inside a container, where there is genuinely no
// keychain — and the fix is environmental, not something they can guess.
func keychainError(verb string, err error) error {
	return fmt.Errorf(
		"%s prizm key from the OS keychain: %w\n"+
			"       prizm keeps variable values encrypted and the key lives in your keychain.\n"+
			"       %s",
		verb, err, keychainAdvice())
}
