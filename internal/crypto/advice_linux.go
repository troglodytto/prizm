//go:build linux

package crypto

// keychainAdvice names what has to be running on Linux. The Secret Service
// API is provided by a daemon, not the OS, so a headless session commonly has
// none at all.
func keychainAdvice() string {
	return "On Linux that is a Secret Service provider — gnome-keyring or KWallet — " +
		"which a headless\n       session often has none of. Start one, or run prizm " +
		"where a desktop session is available."
}
