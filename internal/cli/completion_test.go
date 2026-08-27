package cli

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/spf13/cobra"
)

func TestCompleteGroupsRanksByDirectory(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "AAA") // alphabetically first, unrelated
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.cwd = beDir

	got, directive := h.app.completeGroups("")

	if len(got) != 2 {
		t.Fatalf("completeGroups() = %v, want both groups — it must sort, not filter", got)
	}
	if got[0] != "XYZ" {
		t.Errorf("completeGroups()[0] = %q, want %q for the group containing cwd", got[0], "XYZ")
	}
	if directive&cobra.ShellCompDirectiveKeepOrder == 0 {
		t.Error("directive lacks KeepOrder; the shell would alphabetise and discard the ranking")
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Error("directive lacks NoFileComp; the shell would offer filenames too")
	}
}

func TestCompleteGroupsWithNoGroups(t *testing.T) {
	h := newHarness(t)

	if got, _ := h.app.completeGroups(""); len(got) != 0 {
		t.Errorf("completeGroups() = %v, want none", got)
	}
}

func TestCompleteGroupsFiltersByPrefix(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "alpha")
	h.run(t, "init", "beta")

	got, _ := h.app.completeGroups("al")
	if diff := cmp.Diff([]string{"alpha"}, got); diff != "" {
		t.Errorf("completeGroups(\"al\") mismatch (-want +got):\n%s", diff)
	}
}

func TestCompleteWorkflows(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-workflow", "XYZ", "production", "--tag", "prod")
	h.run(t, "add-workflow", "XYZ", "local")

	got, directive := h.app.completeWorkflows("XYZ", "")

	if diff := cmp.Diff([]string{"local", "production"}, got); diff != "" {
		t.Errorf("completeWorkflows() mismatch (-want +got):\n%s", diff)
	}
	if directive&cobra.ShellCompDirectiveNoFileComp == 0 {
		t.Error("directive lacks NoFileComp")
	}
}

func TestCompleteWorkflowsUnknownGroup(t *testing.T) {
	h := newHarness(t)

	if got, _ := h.app.completeWorkflows("NOPE", ""); len(got) != 0 {
		t.Errorf("completeWorkflows(unknown) = %v, want none", got)
	}
}

func TestCompleteRepos(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "frontend")
	h.run(t, "add-repo", "XYZ", "backend")

	got, _ := h.app.completeRepos("XYZ", "")
	if diff := cmp.Diff([]string{"backend", "frontend"}, got); diff != "" {
		t.Errorf("completeRepos() mismatch (-want +got):\n%s", diff)
	}
}

// `prizm up <TAB>` → group names; `prizm up XYZ <TAB>` → that group's workflows.
func TestUpCompletionByPosition(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")
	h.run(t, "add-workflow", "XYZ", "local")

	first, _ := h.app.completeGroupThenWorkflow(nil, "")
	if len(first) == 0 || first[0] != "XYZ" {
		t.Errorf("first position = %v, want the group names", first)
	}

	second, _ := h.app.completeGroupThenWorkflow([]string{"XYZ"}, "")
	if diff := cmp.Diff([]string{"local"}, second); diff != "" {
		t.Errorf("second position mismatch (-want +got):\n%s", diff)
	}

	if third, _ := h.app.completeGroupThenWorkflow([]string{"XYZ", "local"}, ""); len(third) != 0 {
		t.Errorf("third position = %v, want nothing left to complete", third)
	}
}

// Standing inside a repo, the first position offers that group's workflows too,
// because `prizm up local` is valid there.
func TestUpCompletionOffersWorkflowsWhenGroupIsInferable(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.cwd = beDir

	got, _ := h.app.completeGroupThenWorkflow(nil, "")

	var sawWorkflow bool
	for _, c := range got {
		if c == "local" {
			sawWorkflow = true
		}
	}
	if !sawWorkflow {
		t.Errorf("first position = %v, want it to include the inferred group's workflows", got)
	}
}

// completeRoot must contribute groups only: cobra emits the verbs itself, and
// adding them here would duplicate every one.
func TestCompleteRootOffersGroupsWithoutDuplicatingVerbs(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "XYZ")

	got, _ := h.app.completeRoot(NewRootCmd(h.app), "")

	if diff := cmp.Diff([]string{"XYZ"}, got); diff != "" {
		t.Errorf("completeRoot() mismatch (-want +got):\n%s", diff)
	}
}

func TestCompleteRootAddsInferredGroupsWorkflows(t *testing.T) {
	h := newHarness(t)
	beDir := h.repoDir(t, "backend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-workflow", "XYZ", "local")
	h.cwd = beDir

	got, _ := h.app.completeRoot(NewRootCmd(h.app), "")

	if diff := cmp.Diff([]string{"XYZ", "local"}, got); diff != "" {
		t.Errorf("completeRoot() mismatch (-want +got):\n%s", diff)
	}
}
