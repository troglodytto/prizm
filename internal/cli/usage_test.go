package cli

import (
	"strings"
	"testing"
)

// help() is what the user actually sees: prizm writes the error and the help
// block to stderr.
func (h *harness) help() string { return h.err.String() + h.out.String() }

func TestMissingArgumentsShowThatCommandsHelp(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantIn  string
		wantUse string
	}{
		{name: "add-repo alone", args: []string{"add-repo"}, wantIn: "Register a repo", wantUse: "add-repo <group> <repo>"},
		{name: "add-workflow alone", args: []string{"add-workflow"}, wantIn: "Without --repos", wantUse: "add-workflow <group> <workflow>"},
		{name: "init alone", args: []string{"init"}, wantIn: "Create a new group", wantUse: "init <group>"},
		{name: "shared-add alone", args: []string{"shared-add"}, wantIn: "backed by a real .env file", wantUse: "shared-add"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)

			if err := h.run(t, tt.args...); err == nil {
				t.Fatal("error = nil, want a usage error")
			}

			got := h.help()
			if !strings.Contains(got, tt.wantIn) {
				t.Errorf("output missing the command's description %q:\n%s", tt.wantIn, got)
			}
			if !strings.Contains(got, tt.wantUse) {
				t.Errorf("output missing the usage line %q:\n%s", tt.wantUse, got)
			}
			if !strings.Contains(got, "Flags:") {
				t.Errorf("output missing the flag list — that is the part people need:\n%s", got)
			}
		})
	}
}

func TestTooManyArgumentsShowsHelp(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "init", "A", "B", "C"); err == nil {
		t.Fatal("error = nil, want a usage error")
	}
	if got := h.help(); !strings.Contains(got, "Create a new group") {
		t.Errorf("output = %q, want init's help", got)
	}
}

func TestUnknownFlagShowsHelp(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	if err := h.run(t, "add-repo", "XYZ", "backend", "--nonsense"); err == nil {
		t.Fatal("error = nil, want a usage error")
	}

	got := h.help()
	if !strings.Contains(got, "nonsense") {
		t.Errorf("output = %q, want the bad flag named", got)
	}
	if !strings.Contains(got, "--env-file") {
		t.Errorf("output = %q, want the real flags listed", got)
	}
}

// The inference case: prizm cannot tell which group you mean, and neither
// the given nor the inferred values resolve.
func TestFailedInferenceShowsHelp(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.cwd = "/somewhere/outside/every/repo"

	if err := h.run(t, "up", "local"); err == nil {
		t.Fatal("error = nil, want a usage error")
	}

	got := h.help()
	if !strings.Contains(got, "not inside a registered repo") {
		t.Errorf("output = %q, want the inference failure explained", got)
	}
	if !strings.Contains(got, "up [group] <workflow>") {
		t.Errorf("output = %q, want up's usage line showing the group is optional", got)
	}
}

func TestUnknownCommandShowsRootHelp(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t, "definitely-not-a-command"); err == nil {
		t.Fatal("error = nil, want a usage error")
	}

	got := h.help()
	if !strings.Contains(got, "definitely-not-a-command") {
		t.Errorf("output = %q, want the unknown word named", got)
	}
	if !strings.Contains(got, "Available Commands") {
		t.Errorf("output = %q, want the command list", got)
	}
}

// Runtime failures must NOT dump help — it buries the actual message.
func TestRuntimeErrorsDoNotShowHelp(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown group", args: []string{"add-repo", "NOPE", "backend"}},
		{name: "duplicate group", args: []string{"init", "XYZ"}},
		{name: "reserved workflow name", args: []string{"add-workflow", "XYZ", "status"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.run(t, "init", "XYZ")

			if err := h.run(t, tt.args...); err == nil {
				t.Fatal("error = nil, want a runtime error")
			}
			if got := h.help(); strings.Contains(got, "Available Commands") || strings.Contains(got, "Flags:") {
				t.Errorf("a runtime error dumped help, burying the message:\n%s", got)
			}
		})
	}
}

// A bare `prizm` is not an error — it is someone looking around.
func TestBarePrizmShowsHelpWithoutError(t *testing.T) {
	h := newHarness(t)

	if err := h.run(t); err != nil {
		t.Fatalf("bare prizm error = %v, want nil", err)
	}
	if got := h.help(); !strings.Contains(got, "Available Commands") {
		t.Errorf("output = %q, want the help text", got)
	}
}
