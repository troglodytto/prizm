package style

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"unicode"
)

// Values are masked by default wherever prizm prints a diff.
//
// The tool exists to hold credentials, and its most-used commands — `up
// --dry-run`, `sync`, `audit` — print old and new values side by side. Run any
// of them while screen-sharing, in a logged terminal, or in CI output, and the
// secret is in someone else's scrollback. Masking is the default because the
// unsafe case is the invisible one: nobody remembers to pass --safe.
var showValues bool

// ShowValues reveals values for this process. Wired to --show-values.
func ShowValues() { showValues = true }

// userinfo matches the credential embedded in a connection string:
// scheme://user:password@host. This is where secrets most often hide in plain
// sight — the value is a URL, so a rule that skips URLs to avoid masking
// endpoints will wave a database password straight through.
var userinfo = regexp.MustCompile(`^([a-zA-Z][\w+.-]*://)([^:/@\s]+):([^@/\s]+)@`)

// sensitiveKey matches names that conventionally hold a credential.
var sensitiveKey = regexp.MustCompile(
	`(?i)(secret|password|passwd|token|api[_-]?key|access[_-]?key|private[_-]?key|credential|auth|salt|signature|cert|dsn|conn)`)

// Secret renders a value for display, masking it unless it is plainly benign.
//
// Both the key and the value are consulted. A name-only rule misses
// PAC_SECRET renamed to PAC_S; a value-only rule masks every URL. Taking the
// union errs toward masking, which is the direction to err in.
func Secret(key, value string) string {
	if showValues || value == "" {
		return value
	}
	// A connection string is redacted rather than replaced: the host and
	// database name are the useful half of the diff, and only the password
	// needs hiding.
	if m := userinfo.FindStringSubmatch(value); m != nil {
		return m[1] + m[2] + ":" + mask(m[3]) + "@" + value[len(m[0]):]
	}
	if !sensitive(key, value) {
		return value
	}
	return mask(value)
}

// sensitive reports whether a value should be hidden.
func sensitive(key, value string) bool {
	if sensitiveKey.MatchString(key) {
		return true
	}
	// A long, high-entropy, unbroken token is a credential whatever it is
	// called. URLs and paths contain separators; keys and hashes do not.
	trimmed := strings.TrimSpace(value)
	if len(trimmed) < 20 || strings.ContainsAny(trimmed, " /\\:,") {
		return false
	}
	return looksRandom(trimmed)
}

// looksRandom reports whether a string mixes character classes the way a
// generated credential does and an ordinary word does not.
func looksRandom(s string) bool {
	var upper, lower, digit int
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			upper++
		case unicode.IsLower(r):
			lower++
		case unicode.IsDigit(r):
			digit++
		}
	}
	classes := 0
	for _, n := range []int{upper, lower, digit} {
		if n > 0 {
			classes++
		}
	}
	return classes >= 2 && digit > 0
}

// mask replaces a value with a stable fingerprint.
//
// A fingerprint rather than a row of dots: a diff has to answer "which of
// these two is the production key", and it can only do that if the same value
// renders the same way every time. Four hex characters distinguish values
// without being enough to reconstruct one.
func mask(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "••••" + hex.EncodeToString(sum[:])[:4]
}
