package cli

import "strings"

// completePrefix is the hidden command cobra's shell completion invokes.
const completePrefix = "__complete"

// Resolver supplies everything Rewrite needs to know about the world.
type Resolver struct {
	IsCommand  func(name string) bool
	IsGroup    func(name string) bool
	InferGroup func() (string, bool)
	IsWorkflow func(group, name string) bool
}

// Rewrite turns the group-first sugar into canonical verb-first arguments:
//
//	prizm XYZ up local  →  prizm up XYZ local
//	prizm XYZ local     →  prizm up XYZ local
//	prizm XYZ           →  prizm pick XYZ
//	prizm local         →  prizm up XYZ local   (inside one of XYZ's repos)
//
// This works because every group-scoped verb takes the group as its first
// positional, so moving the group from the front to the second slot is always
// valid and needs no per-command knowledge.
//
// Anything starting with a command or a flag is returned untouched, as is
// anything that cannot be resolved, so cobra reports the error itself.
func Rewrite(args []string, r Resolver) []string {
	if len(args) == 0 {
		return args
	}

	// Shell completion: rewrite the words the user actually typed.
	if args[0] == completePrefix {
		return append([]string{completePrefix}, Rewrite(args[1:], r)...)
	}

	head := args[0]
	if strings.HasPrefix(head, "-") || r.IsCommand(head) {
		return args
	}

	if r.IsGroup(head) {
		if len(args) == 1 {
			return []string{"pick", head}
		}
		if r.IsCommand(args[1]) {
			return append([]string{args[1], head}, args[2:]...)
		}
		return append([]string{"up", head}, args[1:]...)
	}

	// Not a command, not a group: it may be a workflow of the group we are
	// standing in.
	if group, ok := r.InferGroup(); ok && r.IsWorkflow(group, head) {
		return append([]string{"up", group, head}, args[1:]...)
	}
	return args
}
