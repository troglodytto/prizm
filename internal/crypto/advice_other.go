//go:build !linux

package crypto

// keychainAdvice is the fallback for platforms whose keychain ships with the
// OS and is unlocked with the login session.
func keychainAdvice() string {
	return "Unlock your login keychain and try again."
}
