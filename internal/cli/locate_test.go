package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// seedLocated registers two repos with real directories and returns them.
func (h *harness) seedLocated(t *testing.T) (beDir, feDir string) {
	t.Helper()

	beDir, feDir = h.repoDir(t, "backend"), h.repoDir(t, "frontend")

	h.run(t, "init", "XYZ")
	h.run(t, "add-repo", "XYZ", "backend", "--path", beDir)
	h.run(t, "add-repo", "XYZ", "frontend", "--path", feDir)
	h.run(t, "add-workflow", "XYZ", "local")
	return beDir, feDir
}

func TestSplitGroupUsesExplicitName(t *testing.T) {
	h := newHarness(t)
	h.seedLocated(t)
	h.cwd = "/somewhere/else"

	g, rest, err := h.app.splitGroup([]string{"XYZ", "local"}, 1)
	if err != nil {
		t.Fatalf("splitGroup() error = %v", err)
	}
	if g.Name != "XYZ" {
		t.Errorf("group = %q, want %q", g.Name, "XYZ")
	}
	if diff := cmp.Diff([]string{"local"}, rest); diff != "" {
		t.Errorf("rest mismatch (-want +got):\n%s", diff)
	}
}

func TestSplitGroupInfersFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	g, rest, err := h.app.splitGroup([]string{"local"}, 1)
	if err != nil {
		t.Fatalf("splitGroup() error = %v", err)
	}
	if g.Name != "XYZ" {
		t.Errorf("group = %q, want %q inferred from cwd", g.Name, "XYZ")
	}
	if diff := cmp.Diff([]string{"local"}, rest); diff != "" {
		t.Errorf("rest mismatch (-want +got):\n%s", diff)
	}
}

func TestSplitGroupInfersFromASubdirectory(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir + "/src/handlers"

	g, _, err := h.app.splitGroup([]string{"local"}, 1)
	if err != nil {
		t.Fatalf("splitGroup() error = %v", err)
	}
	if g.Name != "XYZ" {
		t.Errorf("group = %q, want %q", g.Name, "XYZ")
	}
}

func TestSplitGroupAmbiguousOutsideAnyRepo(t *testing.T) {
	h := newHarness(t)
	h.seedLocated(t)
	h.cwd = "/somewhere/else"

	_, _, err := h.app.splitGroup([]string{"local"}, 1)
	if err == nil {
		t.Fatal("splitGroup() error = nil, want an ambiguity error")
	}
	if !strings.Contains(err.Error(), "group") {
		t.Errorf("error = %q, want it to tell the user to name the group", err)
	}
}

func TestSplitGroupRepoInfersBoth(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	g, repo, rest, err := h.app.splitGroupRepo([]string{"PORT=8080"}, 1)
	if err != nil {
		t.Fatalf("splitGroupRepo() error = %v", err)
	}
	if g.Name != "XYZ" || repo.Name != "backend" {
		t.Errorf("got %s/%s, want XYZ/backend", g.Name, repo.Name)
	}
	if diff := cmp.Diff([]string{"PORT=8080"}, rest); diff != "" {
		t.Errorf("rest mismatch (-want +got):\n%s", diff)
	}
}

func TestSplitGroupRepoInfersGroupOnly(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	g, repo, _, err := h.app.splitGroupRepo([]string{"frontend", "PORT=3000"}, 1)
	if err != nil {
		t.Fatalf("splitGroupRepo() error = %v", err)
	}
	if g.Name != "XYZ" || repo.Name != "frontend" {
		t.Errorf("got %s/%s, want XYZ/frontend — an explicit repo overrides cwd", g.Name, repo.Name)
	}
}

func TestSplitGroupRepoFullyExplicit(t *testing.T) {
	h := newHarness(t)
	h.seedLocated(t)
	h.cwd = "/somewhere/else"

	g, repo, _, err := h.app.splitGroupRepo([]string{"XYZ", "backend", "PORT=8080"}, 1)
	if err != nil {
		t.Fatalf("splitGroupRepo() error = %v", err)
	}
	if g.Name != "XYZ" || repo.Name != "backend" {
		t.Errorf("got %s/%s, want XYZ/backend", g.Name, repo.Name)
	}
}

func TestCountAssignments(t *testing.T) {
	tests := []struct {
		in   []string
		want int
	}{
		{in: []string{"PORT=8080"}, want: 1},
		{in: []string{"backend", "PORT=8080"}, want: 1},
		{in: []string{"XYZ", "backend", "A=1", "B=2"}, want: 2},
		{in: []string{"XYZ", "backend"}, want: 1},
	}

	for _, tt := range tests {
		if got := countAssignments(tt.in); got != tt.want {
			t.Errorf("countAssignments(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

// End-to-end: the command itself, run from inside a repo, with no group.
func TestUpInfersGroupFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.run(t, "var", "XYZ", "backend", "PORT=8080", "--workflow", "local")
	h.cwd = beDir

	if err := h.run(t, "up", "local"); err != nil {
		t.Fatalf("up local error = %v\nout: %s", err, h.out.String())
	}
	if !strings.Contains(h.out.String(), "backend") {
		t.Errorf("output = %q, want it to have applied the inferred group", h.out.String())
	}
}

func TestVarInfersGroupAndRepoFromCwd(t *testing.T) {
	h := newHarness(t)
	beDir, _ := h.seedLocated(t)
	h.cwd = beDir

	if err := h.run(t, "var", "LOG_LEVEL=debug"); err != nil {
		t.Fatalf("var error = %v", err)
	}

	g, _ := h.app.Store.GroupByName("XYZ")
	repo, _ := h.app.Store.RepoByName(g.ID, "backend")
	got, _ := h.app.Store.RepoVars(repo.ID)
	if got["LOG_LEVEL"] != "debug" {
		t.Errorf("RepoVars() = %v, want LOG_LEVEL set on the inferred repo", got)
	}
}

// The bare-workflow sugar, from inside a repo: `prizm local`.
func TestRewriteInfersGroupForBareWorkflow(t *testing.T) {
	r := Resolver{
		IsCommand:  func(s string) bool { return s == "up" || s == "ls" },
		IsGroup:    func(s string) bool { return s == "XYZ" },
		InferGroup: func() (string, bool) { return "XYZ", true },
		IsWorkflow: func(group, name string) bool { return group == "XYZ" && name == "local" },
	}

	if diff := cmp.Diff([]string{"up", "XYZ", "local"}, Rewrite([]string{"local"}, r)); diff != "" {
		t.Errorf("Rewrite() mismatch (-want +got):\n%s", diff)
	}
}

func TestRewriteLeavesUnknownWordAloneWhenNotAWorkflow(t *testing.T) {
	r := Resolver{
		IsCommand:  func(s string) bool { return s == "up" },
		IsGroup:    func(string) bool { return false },
		InferGroup: func() (string, bool) { return "XYZ", true },
		IsWorkflow: func(string, string) bool { return false },
	}

	if diff := cmp.Diff([]string{"typo"}, Rewrite([]string{"typo"}, r)); diff != "" {
		t.Errorf("Rewrite() should leave it untouched so cobra reports it (-want +got):\n%s", diff)
	}
}
