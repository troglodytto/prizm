package cli

import (
	"strings"
	"testing"
)

// Acting on the first flag and ignoring the rest is the worst answer a
// destructive command can give: the user believes both happened.
func TestRemoveRefusesTwoTargets(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "auth")
	h.run(t, "add-workflow", "acme", "local", "--repos", "auth")

	err := h.run(t, "rm", "acme", "--repo", "auth", "--workflow", "local", "--yes")
	if err == nil {
		t.Fatal("error = nil, want a refusal")
	}
	for _, want := range []string{"--repo", "--workflow", "one at a time"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}

	// Neither may have been touched.
	g, _ := h.app.Store.GroupByName("acme")
	if _, err := h.app.Store.RepoByName(g.ID, "auth"); err != nil {
		t.Error("the repo was removed despite the refusal")
	}
	if _, err := h.app.Store.WorkflowByName(g.ID, "local"); err != nil {
		t.Error("the workflow was removed despite the refusal")
	}
}

func TestRenameRefusesTwoTargets(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "auth")
	h.run(t, "add-workflow", "acme", "local", "--repos", "auth")

	err := h.run(t, "rename", "acme", "x", "--repo", "auth", "--workflow", "local")
	if err == nil || !strings.Contains(err.Error(), "one at a time") {
		t.Errorf("error = %v, want a refusal", err)
	}

	g, _ := h.app.Store.GroupByName("acme")
	if _, err := h.app.Store.RepoByName(g.ID, "auth"); err != nil {
		t.Error("the repo was renamed despite the refusal")
	}
}

// A bag name is not unique across workflows, so removing one by name alone
// is ambiguous — and deleting the wrong environment's credentials is not
// something an error message can undo.
func TestRemoveRefusesAnAmbiguousBag(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "auth")
	h.run(t, "add-workflow", "acme", "local", "--repos", "auth")
	h.run(t, "add-workflow", "acme", "staging", "--repos", "auth")
	h.run(t, "shared-add", "acme", "local", "infra", "--repos", "auth")
	h.run(t, "shared-add", "acme", "staging", "infra", "--repos", "auth")

	err := h.run(t, "rm", "acme", "--bag", "infra", "--yes")
	if err == nil {
		t.Fatal("error = nil, want a refusal")
	}
	for _, want := range []string{"local", "staging", "will not guess"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}

	bags, _ := h.app.Store.AllSharedGroups()
	if len(bags) != 2 {
		t.Errorf("bags = %d, want both still present", len(bags))
	}
}

// One bag of that name is not ambiguous.
func TestRemoveAcceptsAnUnambiguousBag(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "auth")
	h.run(t, "add-workflow", "acme", "local", "--repos", "auth")
	h.run(t, "shared-add", "acme", "local", "infra", "--repos", "auth")

	if err := h.run(t, "rm", "acme", "--bag", "infra", "--yes"); err != nil {
		t.Fatalf("rm --bag error = %v", err)
	}
	if bags, _ := h.app.Store.AllSharedGroups(); len(bags) != 0 {
		t.Errorf("bags = %d, want it removed", len(bags))
	}
}

// Naming no target is how you mean "the group itself" — still allowed.
func TestRemoveWithNoTargetStillMeansTheGroup(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")
	h.run(t, "add-repo", "acme", "auth")

	if err := h.run(t, "rm", "acme", "--yes"); err != nil {
		t.Fatalf("rm error = %v", err)
	}
	if groups, _ := h.app.Store.ListGroups(); len(groups) != 0 {
		t.Errorf("groups = %d, want the group removed", len(groups))
	}
}

// An unresolvable group must stop the command before anything is touched.
func TestRemoveRefusesWhenTheGroupIsAmbiguous(t *testing.T) {
	h := newHarness(t)
	h.run(t, "init", "acme")
	h.run(t, "init", "other")
	h.cwd = "/somewhere/outside/every/repo"

	err := h.run(t, "rm", "--yes")
	if err == nil {
		t.Fatal("error = nil, want a refusal")
	}
	if !strings.Contains(err.Error(), "not inside a registered repo") {
		t.Errorf("error = %q, want it to explain the group cannot be determined", err)
	}
	if groups, _ := h.app.Store.ListGroups(); len(groups) != 2 {
		t.Errorf("groups = %d, want both untouched", len(groups))
	}
}
