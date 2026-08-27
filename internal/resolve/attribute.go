package resolve

import "strings"

// Origin describes where a key's winning value was defined.
type Origin struct {
	Layer         string
	Kind          LayerKind
	SharedGroupID int64
	GroupID       int64
	Template      string
}

// Attribute returns the highest-precedence layer defining key. layers must be
// in the same low-to-high order Merge expects.
func Attribute(layers []Layer, key string) (Origin, bool) {
	for i := len(layers) - 1; i >= 0; i-- {
		template, ok := layers[i].Vars[key]
		if !ok {
			continue
		}
		return Origin{
			Layer:         layers[i].Name,
			Kind:          layers[i].Kind,
			SharedGroupID: layers[i].SharedGroupID,
			GroupID:       layers[i].GroupID,
			Template:      template,
		}, true
	}
	return Origin{}, false
}

// SoleRef reports the referenced name when template is exactly one reference
// and nothing else.
//
// This is the only template shape that can be inverted: given a new expanded
// value, the whole of it is the referenced variable's new value. Anything
// composite — "postgres://${HOST}/db" — cannot be split back apart reliably,
// so prizm does not try.
func SoleRef(template string) (string, bool) {
	trimmed := strings.TrimSpace(template)

	match := refPattern.FindStringSubmatch(trimmed)
	if match == nil || match[0] != trimmed {
		return "", false
	}
	return match[1], true
}

// IsTemplate reports whether a stored value contains any reference at all.
func IsTemplate(value string) bool { return strings.Contains(value, "${") }
