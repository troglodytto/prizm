package envfile

import (
	"regexp"
	"sort"
	"strings"
)

// bareSafe matches values that need no quoting.
//
// '?' is included because query-string DSNs are ubiquitous in env files and a
// '?' is inert on the right-hand side of a shell assignment — bash performs no
// globbing or word splitting there. '&', '*', ';' and friends stay excluded:
// they are genuinely dangerous if someone `source`s the file.
var bareSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./?-]*$`)

// Render writes vars as .env text with keys in ascending order, so the output
// is byte-identical for the same map regardless of insertion order.
func Render(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quote(vars[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

func quote(v string) string {
	if bareSafe.MatchString(v) {
		return v
	}

	escaped := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	).Replace(v)

	return `"` + escaped + `"`
}
