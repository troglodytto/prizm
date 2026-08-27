// Package resolve turns prizm's stored variable layers into the exact map that
// gets written to a repo's env file.
//
// The three stages stay separate on purpose: Merge produces templates, Expand
// resolves them, and Emit drops the internal plumbing. Reconciliation compares
// templates while applying compares outputs, and both need a place to stop.
package resolve

import "strings"

// InternalPrefix marks a variable that exists only as plumbing: it can be
// referenced from any template but is never written to a repo's env file.
//
// Internal-ness lives in the key name rather than a flag, which means it
// cannot be lost — an override of an internal key is still internally named,
// so a secret can never be published by an edit that forgot something.
const InternalPrefix = "_PRIZM_"

// IsInternal reports whether key is prizm-internal.
func IsInternal(key string) bool { return strings.HasPrefix(key, InternalPrefix) }

// LayerKind identifies which of the four variable layers this is.
type LayerKind int

const (
	// LayerGroup is true of the whole group, in every workflow.
	LayerGroup LayerKind = iota
	// LayerRepoShared applies in every workflow that touches the repo.
	LayerRepoShared
	// LayerSharedGroup is a named bag scoped to (workflow, repo subset).
	LayerSharedGroup
	// LayerWorkflowRepo is one repo inside one workflow. Highest precedence.
	LayerWorkflowRepo
)

// Layer is one contributor to a repo's variables. Name appears in errors and
// in sync's explanations; SharedGroupID is set only for LayerSharedGroup.
type Layer struct {
	Name          string
	Kind          LayerKind
	SharedGroupID int64
	GroupID       int64
	Vars          map[string]string
}

// Merge folds layers low-precedence first; later layers win.
func Merge(layers []Layer) map[string]string {
	out := make(map[string]string)
	for _, layer := range layers {
		for key, value := range layer.Vars {
			out[key] = value
		}
	}
	return out
}
