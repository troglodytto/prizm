package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/troglodytto/prizm/internal/tui"
)

func (h *harness) seedWorkflow(t *testing.T) {
	t.Helper()

	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "auth")
	h.run(t, "add-repo", "acme", "backend")
	h.run(t, "add-repo", "acme", "ai")
	h.run(t, "add-workflow", "acme", "local", "--tag", "local", "--repos", "auth,backend")
}

func (h *harness) workflowRepos(t *testing.T, name string) []string {
	t.Helper()

	g, _ := h.app.Store.GroupByName("acme")
	wf, err := h.app.Store.WorkflowByName(g.ID, name)
	if err != nil {
		t.Fatalf("WorkflowByName(%q) error = %v", name, err)
	}

	repos, _ := h.app.Store.WorkflowRepos(wf.ID)
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Name)
	}
	return out
}

func TestEditWorkflowSetsTheTag(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	if err := h.run(t, "edit-workflow", "acme", "local", "--tag", "qa"); err != nil {
		t.Fatalf("edit-workflow error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	if wf.Tag != "qa" {
		t.Errorf("Tag = %q, want %q", wf.Tag, "qa")
	}
}

func TestEditWorkflowClearsTheTag(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	if err := h.run(t, "edit-workflow", "acme", "local", "--tag", ""); err != nil {
		t.Fatalf("edit-workflow error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	if wf.Tag != "" {
		t.Errorf("Tag = %q, want it cleared", wf.Tag)
	}
}

// Only the tag was named, so membership must be left alone.
func TestEditWorkflowTagOnlyDoesNotTouchMembers(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	h.app.pickerInjected = true
	h.app.PickMany = func(string, []tui.Option, []string) ([]string, error) {
		t.Error("the repo picker opened when only --tag was given")
		return nil, nil
	}

	if err := h.run(t, "edit-workflow", "acme", "local", "--tag", "qa"); err != nil {
		t.Fatalf("edit-workflow error = %v", err)
	}
	if diff := cmp.Diff([]string{"auth", "backend"}, h.workflowRepos(t, "local")); diff != "" {
		t.Errorf("members changed (-want +got):\n%s", diff)
	}
}

func TestEditWorkflowSetsMembersFromFlag(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	if err := h.run(t, "edit-workflow", "acme", "local", "--repos", "ai,auth"); err != nil {
		t.Fatalf("edit-workflow error = %v", err)
	}
	if diff := cmp.Diff([]string{"ai", "auth"}, h.workflowRepos(t, "local")); diff != "" {
		t.Errorf("members mismatch (-want +got):\n%s", diff)
	}
}

// No flags means the repo set is what you came to change, and the picker
// opens with the current members already ticked.
func TestEditWorkflowWithNoFlagsPicksReposPreselected(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	var preselected []string
	h.app.pickerInjected = true
	h.app.PickMany = func(_ string, _ []tui.Option, pre []string) ([]string, error) {
		preselected = pre
		return []string{"ai"}, nil
	}

	if err := h.run(t, "edit-workflow", "acme", "local"); err != nil {
		t.Fatalf("edit-workflow error = %v", err)
	}
	if diff := cmp.Diff([]string{"auth", "backend"}, preselected); diff != "" {
		t.Errorf("preselection mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"ai"}, h.workflowRepos(t, "local")); diff != "" {
		t.Errorf("members mismatch (-want +got):\n%s", diff)
	}
}

func TestEditWorkflowRenames(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	if err := h.run(t, "edit-workflow", "acme", "local", "--name", "dev"); err != nil {
		t.Fatalf("edit-workflow error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	if _, err := h.app.Store.WorkflowByName(g.ID, "dev"); err != nil {
		t.Fatalf("WorkflowByName(dev) error = %v", err)
	}
}

// Dropping a repo is a change of scope, not a decision to discard values.
func TestEditWorkflowKeepsVariablesOfDroppedRepos(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)
	h.run(t, "var", "acme", "backend", "KEEP=me", "--workflow", "local")

	h.run(t, "edit-workflow", "acme", "local", "--repos", "auth")
	h.run(t, "edit-workflow", "acme", "local", "--repos", "auth,backend")

	g, _ := h.app.Store.GroupByName("acme")
	wf, _ := h.app.Store.WorkflowByName(g.ID, "local")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")

	vars, _ := h.app.Store.WorkflowRepoVars(wf.ID, repo.ID)
	if vars["KEEP"] != "me" {
		t.Errorf("variables lost when the repo left and came back: %v", vars)
	}
}

func TestEditWorkflowWithoutATerminalNamesTheFlag(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	h.app.PickMany = nil
	h.app.pickerInjected = false

	err := h.run(t, "edit-workflow", "acme", "local")
	if err == nil {
		t.Fatal("error = nil, want a usage error")
	}
	if !strings.Contains(err.Error(), "--repos auth,backend") {
		t.Errorf("error = %q, want it to suggest the current members as a flag", err)
	}
}

func TestEditWorkflowUnknownWorkflow(t *testing.T) {
	h := newHarness(t)
	h.seedWorkflow(t)

	if err := h.run(t, "edit-workflow", "acme", "ghost", "--tag", "qa"); err == nil ||
		!strings.Contains(err.Error(), "ghost") {
		t.Errorf("error = %v, want it to name the unknown workflow", err)
	}
}
