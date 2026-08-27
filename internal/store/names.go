package store

import (
	"errors"
	"fmt"
	"regexp"
)

// ErrInvalidName is returned for a group, repo, workflow or shared-bag name
// that is not a safe identifier.
var ErrInvalidName = errors.New("invalid name")

// namePattern is deliberately strict. These names are not just labels:
//
//   - they become path segments under the data directory, so "..", "/" and
//     friends must never reach filepath.Join;
//   - they are typed as command arguments and emitted as completion
//     candidates, so whitespace and shell metacharacters make them unusable.
//
// Requiring a leading alphanumeric excludes "." and ".." without a special case.
var namePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidName reports whether name is a safe identifier.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// checkName validates a name, naming the kind in the error so the message
// reads well wherever it surfaces.
func checkName(kind, name string) error {
	if ValidName(name) {
		return nil
	}
	return fmt.Errorf("%s name %q: %w (letters, digits, dot, dash and underscore only, starting with a letter or digit)",
		kind, name, ErrInvalidName)
}
