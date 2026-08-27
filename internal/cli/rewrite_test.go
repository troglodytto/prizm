package cli

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func testResolver() Resolver {
	commands := map[string]bool{
		"init": true, "add-repo": true, "add-workflow": true,
		"up": true, "ls": true, "var": true, "import": true,
		"completion": true, "help": true, "__complete": true,
	}
	groups := map[string]bool{"XYZ": true, "ABC": true}

	return Resolver{
		IsCommand:  func(s string) bool { return commands[s] },
		IsGroup:    func(s string) bool { return groups[s] },
		InferGroup: func() (string, bool) { return "", false },
		IsWorkflow: func(string, string) bool { return false },
	}
}

func TestRewrite(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "no args", in: nil, want: nil},
		{name: "explicit verb untouched", in: []string{"up", "XYZ", "local"}, want: []string{"up", "XYZ", "local"}},
		{name: "registration command untouched", in: []string{"init", "NEW"}, want: []string{"init", "NEW"}},
		{name: "group then verb", in: []string{"XYZ", "up", "local"}, want: []string{"up", "XYZ", "local"}},
		{name: "group then workflow implies up", in: []string{"XYZ", "local"}, want: []string{"up", "XYZ", "local"}},
		{name: "group alone lists", in: []string{"XYZ"}, want: []string{"ls", "XYZ"}},
		{name: "group then verb with args", in: []string{"XYZ", "var", "backend", "A=1"}, want: []string{"var", "XYZ", "backend", "A=1"}},
		{name: "group then workflow with flags", in: []string{"XYZ", "local", "--dry-run"}, want: []string{"up", "XYZ", "local", "--dry-run"}},
		{name: "unknown first word untouched", in: []string{"typo", "local"}, want: []string{"typo", "local"}},
		{name: "leading flag untouched", in: []string{"--help"}, want: []string{"--help"}},
		{name: "leading short flag untouched", in: []string{"-h"}, want: []string{"-h"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, Rewrite(tt.in, testResolver())); diff != "" {
				t.Errorf("Rewrite(%v) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}

// The shell calls `prizm __complete <words...>`; the words must be rewritten
// too, or completion would have to duplicate the sugar rules.
func TestRewriteHandlesCompletionPrefix(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "completing a workflow", in: []string{"__complete", "XYZ", ""}, want: []string{"__complete", "up", "XYZ", ""}},
		{name: "partial workflow", in: []string{"__complete", "XYZ", "lo"}, want: []string{"__complete", "up", "XYZ", "lo"}},
		{name: "completing the first word", in: []string{"__complete", ""}, want: []string{"__complete", ""}},
		{name: "explicit verb", in: []string{"__complete", "up", "XYZ", ""}, want: []string{"__complete", "up", "XYZ", ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diff := cmp.Diff(tt.want, Rewrite(tt.in, testResolver())); diff != "" {
				t.Errorf("Rewrite(%v) mismatch (-want +got):\n%s", tt.in, diff)
			}
		})
	}
}
