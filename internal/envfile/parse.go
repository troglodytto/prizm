// Package envfile converts between .env text and key/value maps.
//
// prizm compares and stores variables as maps, never as text: env files are
// not prose, so a change means "this key's value differs", not "this line
// moved". Text is only an edge format.
package envfile

import (
	"fmt"
	"strings"
)

// Parse reads .env-style text into a map. Later duplicate keys win.
func Parse(text string) (map[string]string, error) {
	out := make(map[string]string)

	for i, raw := range strings.Split(text, "\n") {
		lineNo := i + 1

		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, rest, found := strings.Cut(line, "=")
		if !found {
			return nil, fmt.Errorf("line %d: missing '=' in %q", lineNo, line)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", lineNo)
		}

		value, err := parseValue(strings.TrimSpace(rest))
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		out[key] = value
	}

	return out, nil
}

func parseValue(v string) (string, error) {
	if v == "" {
		return "", nil
	}

	switch v[0] {
	case '"':
		body, ok := strings.CutSuffix(v[1:], `"`)
		if !ok || len(v) < 2 {
			return "", fmt.Errorf("unterminated double quote")
		}
		return unescape(body), nil

	case '\'':
		body, ok := strings.CutSuffix(v[1:], `'`)
		if !ok || len(v) < 2 {
			return "", fmt.Errorf("unterminated single quote")
		}
		return body, nil
	}

	// Bare value: an unquoted " #" starts a trailing comment.
	if idx := strings.Index(v, " #"); idx >= 0 {
		v = v[:idx]
	}
	return strings.TrimSpace(v), nil
}

func unescape(s string) string {
	return strings.NewReplacer(
		`\n`, "\n",
		`\r`, "\r",
		`\t`, "\t",
		`\"`, `"`,
		`\\`, `\`,
	).Replace(s)
}
