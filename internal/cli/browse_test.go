package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/troglodytto/prizm/internal/tui"
)

// scriptPicker answers picker prompts in order, recording what it was offered.
func (h *harness) scriptPicker(answers ...string) *[][]tui.Option {
	var offered [][]tui.Option

	i := 0
	h.app.pickerInjected = true
	h.app.PickOne = func(_ string, options []tui.Option) (string, error) {
		offered = append(offered, options)
		if i >= len(answers) {
			return "", tui.ErrCancelled
		}
		answer := answers[i]
		i++
		return answer, nil
	}
	return &offered
}

func TestBrowsePicksGroupThenWorkflowThenApplies(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local", "--repos", "backend")
	h.run(t, "var", "XYZ", "backend", "A=1", "--workflow", "local")

	offered := h.scriptPicker("XYZ", "local")

	if err := h.run(t, "pick"); err != nil {
		t.Fatalf("pick error = %v", err)
	}

	if len(*offered) != 2 {
		t.Fatalf("picker shown %d times, want 2 (group then workflow)", len(*offered))
	}
	if (*offered)[0][0].Value != "XYZ" {
		t.Errorf("first prompt offered %+v, want the groups", (*offered)[0])
	}
	if (*offered)[1][0].Value != "local" {
		t.Errorf("second prompt offered %+v, want the workflows", (*offered)[1])
	}
	if _, err := os.Lstat(filepath.Join(beDir, ".env")); err != nil {
		t.Errorf("workflow was not applied: %v", err)
	}
}

func TestBrowseSkipsTheGroupPromptWhenGroupIsGiven(t *testing.T) {
	h := newHarness(t)
	h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-workflow", "XYZ", "local")

	offered := h.scriptPicker("local")
	h.run(t, "pick", "XYZ")

	if len(*offered) != 1 {
		t.Errorf("picker shown %d times, want 1 (workflow only)", len(*offered))
	}
}

func TestBrowseCarriesTagsAndReposIntoThePicker(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend")
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")

	offered := h.scriptPicker("XYZ", "production")
	h.run(t, "pick")

	wf := (*offered)[1][0]
	if wf.Tag != "prod" {
		t.Errorf("Tag = %q, want it carried into the picker", wf.Tag)
	}
	if !strings.Contains(wf.Desc, "backend") {
		t.Errorf("Desc = %q, want the covered repos", wf.Desc)
	}
}

// Escape is not a failure.
func TestBrowseCancellationIsQuietAndSuccessful(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	h.app.pickerInjected = true
	h.app.PickOne = func(string, []tui.Option) (string, error) { return "", tui.ErrCancelled }

	if err := h.run(t, "pick"); err != nil {
		t.Errorf("cancelling returned %v, want nil — esc is not an error", err)
	}
}

// Without a terminal it must list rather than block.
func TestBrowseWithoutAPickerFallsBackToListing(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "init", "ABC")

	h.app.PickOne = nil
	h.app.pickerInjected = false

	if err := h.run(t, "pick"); err != nil {
		t.Fatalf("pick error = %v", err)
	}
	out := h.out.String()
	if !strings.Contains(out, "XYZ") || !strings.Contains(out, "ABC") {
		t.Errorf("output = %q, want the groups listed as text", out)
	}
}

// `ls` is an explicit list command and must never become interactive — one
// that blocks on input is unusable in a pipe, a script, or a hurry.
func TestLsNeverOpensAPicker(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-workflow", "XYZ", "local")

	h.app.pickerInjected = true
	h.app.PickOne = func(string, []tui.Option) (string, error) {
		t.Error("ls opened a picker")
		return "", nil
	}

	if err := h.run(t, "ls"); err != nil {
		t.Fatalf("ls error = %v", err)
	}
	if err := h.run(t, "ls", "XYZ"); err != nil {
		t.Fatalf("ls XYZ error = %v", err)
	}
}

func TestBrowsePropagatesRealPickerErrors(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	want := errors.New("terminal exploded")
	h.app.pickerInjected = true
	h.app.PickOne = func(string, []tui.Option) (string, error) { return "", want }

	if err := h.run(t, "pick"); !errors.Is(err, want) {
		t.Errorf("error = %v, want the underlying failure to surface", err)
	}
}

// add-workflow opens the checkbox list preselected with everything, so Enter
// reproduces the non-interactive default of "all repos".
func TestAddWorkflowOpensCheckboxListPreselectedWithAll(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "auth")
	h.run(t, "add-repo", "XYZ", "backend")

	var preselected []string
	h.app.pickerInjected = true
	h.app.PickMany = func(_ string, _ []tui.Option, pre []string) ([]string, error) {
		preselected = pre
		return []string{"auth"}, nil
	}

	if err := h.run(t, "add-workflow", "XYZ", "auth-only"); err != nil {
		t.Fatalf("add-workflow error = %v", err)
	}
	if len(preselected) != 2 {
		t.Errorf("preselected %v, want both repos — Enter must mean 'all'", preselected)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "auth-only")
	repos, _ := h.app.Store.WorkflowRepos(wf.ID)
	if len(repos) != 1 || repos[0].Name != "auth" {
		t.Errorf("workflow repos = %+v, want only auth", repos)
	}
}

func TestAddWorkflowExplicitReposSkipsThePicker(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "auth")

	h.app.pickerInjected = true
	h.app.PickMany = func(string, []tui.Option, []string) ([]string, error) {
		t.Error("PickMany was called despite --repos")
		return nil, nil
	}

	if err := h.run(t, "add-workflow", "XYZ", "local", "--repos", "auth"); err != nil {
		t.Fatalf("add-workflow error = %v", err)
	}
}

// The front door tells you what the tool does; it does not take over the
// terminal before you have asked for anything.
func TestBarePrizmShowsHelpNotThePicker(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-workflow", "XYZ", "local")

	h.app.pickerInjected = true
	h.app.PickOne = func(string, []tui.Option) (string, error) {
		t.Error("bare prizm opened the picker")
		return "", nil
	}

	if err := h.run(t); err != nil {
		t.Fatalf("bare prizm error = %v", err)
	}
	if got := h.help(); !strings.Contains(got, "COMMANDS") {
		t.Errorf("output = %q, want the help text", got)
	}
}
