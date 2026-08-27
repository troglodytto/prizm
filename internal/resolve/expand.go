package resolve

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Expansion errors.
var (
	// ErrUnresolved means a template referenced a key that is not defined.
	ErrUnresolved = errors.New("unresolved reference")
	// ErrCycle means references form a loop.
	ErrCycle = errors.New("reference cycle")
)

// escapeSentinel stands in for `$${` while a value is being expanded, so an
// escaped reference is never mistaken for a real one.
const escapeSentinel = "\x00PRIZM_ESCAPED_DOLLAR\x00"

// refPattern matches ${NAME}. A lone `$` is always literal, because env values
// are full of them — passwords, regexes, shell snippets.
var refPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Expand resolves every ${NAME} reference against vars itself. Keys are
// processed in sorted order so errors are reported deterministically.
func Expand(vars map[string]string) (map[string]string, error) {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]string, len(vars))
	for _, key := range keys {
		v, err := expandOne(key, vars, out, nil)
		if err != nil {
			return nil, err
		}
		out[key] = v
	}
	return out, nil
}

// expandOne resolves a single key, recursing into its references. chain carries
// the path taken so a cycle can be reported by name.
func expandOne(key string, vars, done map[string]string, chain []string) (string, error) {
	if v, ok := done[key]; ok {
		return v, nil
	}

	for _, seen := range chain {
		if seen == key {
			return "", fmt.Errorf("%w: %s → %s", ErrCycle, strings.Join(chain, " → "), key)
		}
	}
	chain = append(chain, key)

	raw := strings.ReplaceAll(vars[key], "$${", escapeSentinel)

	var expandErr error
	result := refPattern.ReplaceAllStringFunc(raw, func(match string) string {
		if expandErr != nil {
			return ""
		}

		ref := refPattern.FindStringSubmatch(match)[1]
		if _, ok := vars[ref]; !ok {
			expandErr = fmt.Errorf("%w: %s references ${%s}, which is not defined", ErrUnresolved, key, ref)
			return ""
		}

		value, err := expandOne(ref, vars, done, chain)
		if err != nil {
			expandErr = err
			return ""
		}
		return value
	})
	if expandErr != nil {
		return "", expandErr
	}

	result = strings.ReplaceAll(result, escapeSentinel, "${")
	done[key] = result
	return result, nil
}

// Emit returns only the variables that belong in a repo's env file: every
// expanded value except the internal plumbing. What lands on disk carries no
// trace of where it was derived from.
func Emit(expanded map[string]string) map[string]string {
	out := make(map[string]string, len(expanded))
	for key, value := range expanded {
		if IsInternal(key) {
			continue
		}
		out[key] = value
	}
	return out
}
