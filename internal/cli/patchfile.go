package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// patchKeyInFile rewrites one assignment in a shared file, leaving every other
// byte alone.
//
// The previous approach regenerated the whole file from the database, which
// destroyed anything the user had written but not yet run `shared-sync` on —
// silently, with the run still reporting success. A hand-added key and its
// comment were simply gone, and being absent from both the database and any
// snapshot, unrecoverable.
//
// Editing one line keeps comments, ordering, blank lines and unsynced
// additions exactly as they were. A key that is not present is appended.
func patchKeyInFile(path, key, value string) error {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil // no backing file to keep in step
	}
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	assignment := key + "=" + value
	pattern := regexp.MustCompile(`(?m)^[ \t]*` + regexp.QuoteMeta(key) + `[ \t]*=.*$`)

	out := string(raw)
	if pattern.MatchString(out) {
		out = pattern.ReplaceAllLiteralString(out, assignment)
	} else {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += assignment + "\n"
	}

	return os.WriteFile(path, []byte(out), 0o600)
}
