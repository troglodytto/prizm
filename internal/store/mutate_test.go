package store

import (
	"errors"
	"testing"
)

func TestRenameGroup(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("old")

	if err := s.RenameGroup(g.ID, "new"); err != nil {
		t.Fatalf("RenameGroup() error = %v", err)
	}
	if _, err := s.GroupByName("new"); err != nil {
		t.Errorf("GroupByName(new) error = %v", err)
	}
	if _, err := s.GroupByName("old"); !errors.Is(err, ErrNotFound) {
		t.Error("the old name still resolves")
	}
}

func TestRenameRejectsUnsafeAndDuplicateNames(t *testing.T) {
	s := newTestStore(t)
	a, _ := s.CreateGroup("a")
	s.CreateGroup("b")

	if err := s.RenameGroup(a.ID, "../escape"); !errors.Is(err, ErrInvalidName) {
		t.Errorf("RenameGroup(unsafe) error = %v, want ErrInvalidName", err)
	}
	if err := s.RenameGroup(a.ID, "b"); !errors.Is(err, ErrExists) {
		t.Errorf("RenameGroup(duplicate) error = %v, want ErrExists", err)
	}
}

func TestRenameWorkflowRejectsReservedNames(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	wf, _ := s.AddWorkflow(g.ID, "local", "", nil)

	if err := s.RenameWorkflow(wf.ID, "status"); !errors.Is(err, ErrReservedName) {
		t.Errorf("RenameWorkflow(reserved) error = %v, want ErrReservedName", err)
	}
}

// Renaming a repo must not orphan its variables.
func TestRenameRepoKeepsItsVariables(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	r, _ := s.AddRepo(g.ID, "old", "/tmp/x", "")
	s.SetRepoVar(r.ID, "A", "1")

	if err := s.RenameRepo(r.ID, "new"); err != nil {
		t.Fatalf("RenameRepo() error = %v", err)
	}

	renamed, err := s.RepoByName(g.ID, "new")
	if err != nil {
		t.Fatalf("RepoByName(new) error = %v", err)
	}
	vars, _ := s.RepoVars(renamed.ID)
	if vars["A"] != "1" {
		t.Errorf("variables lost on rename: %v", vars)
	}
}

func TestDeleteGroupCascades(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	r, _ := s.AddRepo(g.ID, "repo", "/tmp/x", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	sg, _ := s.CreateSharedGroup(wf.ID, "bag")
	s.SetSharedGroupVar(sg.ID, "K", "v")
	s.SetRepoVar(r.ID, "A", "1")
	s.SetGroupVar(g.ID, "G", "1")

	if err := s.DeleteGroup(g.ID); err != nil {
		t.Fatalf("DeleteGroup() error = %v", err)
	}

	for name, query := range map[string]string{
		"repos":             `SELECT COUNT(*) FROM repos`,
		"workflows":         `SELECT COUNT(*) FROM workflows`,
		"shared_groups":     `SELECT COUNT(*) FROM shared_groups`,
		"shared_group_vars": `SELECT COUNT(*) FROM shared_group_vars`,
		"repo_vars":         `SELECT COUNT(*) FROM repo_vars`,
		"group_vars":        `SELECT COUNT(*) FROM group_vars`,
	} {
		var n int
		s.db.QueryRow(query).Scan(&n)
		if n != 0 {
			t.Errorf("%s still has %d rows after deleting the group", name, n)
		}
	}
}

func TestDeleteRepoLeavesTheGroupIntact(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	keep, _ := s.AddRepo(g.ID, "keep", "/tmp/a", "")
	drop, _ := s.AddRepo(g.ID, "drop", "/tmp/b", "")
	s.AddWorkflow(g.ID, "local", "", []int64{keep.ID, drop.ID})

	if err := s.DeleteRepo(drop.ID); err != nil {
		t.Fatalf("DeleteRepo() error = %v", err)
	}

	repos, _ := s.ListRepos(g.ID)
	if len(repos) != 1 || repos[0].Name != "keep" {
		t.Errorf("ListRepos() = %+v, want only keep", repos)
	}

	var members int
	s.db.QueryRow(`SELECT COUNT(*) FROM workflow_repos`).Scan(&members)
	if members != 1 {
		t.Errorf("workflow membership = %d rows, want 1 — the deleted repo must drop out", members)
	}
}

func TestDeleteVars(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	r, _ := s.AddRepo(g.ID, "repo", "/tmp/x", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{r.ID})

	s.SetGroupVar(g.ID, "G", "1")
	s.SetRepoVar(r.ID, "R", "1")
	s.SetWorkflowRepoVar(wf.ID, r.ID, "W", "1")

	s.DeleteGroupVar(g.ID, "G")
	s.DeleteRepoVar(r.ID, "R")
	s.DeleteWorkflowRepoVar(wf.ID, r.ID, "W")

	if v, _ := s.GroupVars(g.ID); len(v) != 0 {
		t.Errorf("group var survived: %v", v)
	}
	if v, _ := s.RepoVars(r.ID); len(v) != 0 {
		t.Errorf("repo var survived: %v", v)
	}
	if v, _ := s.WorkflowRepoVars(wf.ID, r.ID); len(v) != 0 {
		t.Errorf("workflow var survived: %v", v)
	}
}

func TestCountsFor(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	r, _ := s.AddRepo(g.ID, "repo", "/tmp/x", "")
	s.AddWorkflow(g.ID, "local", "", []int64{r.ID})
	s.SetRepoVar(r.ID, "A", "1")
	s.SetGroupVar(g.ID, "B", "1")

	repos, workflows, vars, err := s.CountsFor(g.ID)
	if err != nil {
		t.Fatalf("CountsFor() error = %v", err)
	}
	if repos != 1 || workflows != 1 || vars != 2 {
		t.Errorf("CountsFor() = (%d, %d, %d), want (1, 1, 2)", repos, workflows, vars)
	}
}

func TestSetWorkflowTag(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	wf, _ := s.AddWorkflow(g.ID, "local", "local", nil)

	if err := s.SetWorkflowTag(wf.ID, "qa"); err != nil {
		t.Fatalf("SetWorkflowTag() error = %v", err)
	}
	got, _ := s.WorkflowByName(g.ID, "local")
	if got.Tag != "qa" {
		t.Errorf("Tag = %q, want %q", got.Tag, "qa")
	}

	if err := s.SetWorkflowTag(wf.ID, ""); err != nil {
		t.Fatalf("clearing the tag error = %v", err)
	}
	if got, _ := s.WorkflowByName(g.ID, "local"); got.Tag != "" {
		t.Errorf("Tag = %q, want it cleared", got.Tag)
	}
}

func TestReplaceWorkflowRepos(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	a, _ := s.AddRepo(g.ID, "a", "/tmp/a", "")
	b, _ := s.AddRepo(g.ID, "b", "/tmp/b", "")
	c, _ := s.AddRepo(g.ID, "c", "/tmp/c", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{a.ID, b.ID})

	if err := s.ReplaceWorkflowRepos(wf.ID, []int64{b.ID, c.ID}); err != nil {
		t.Fatalf("ReplaceWorkflowRepos() error = %v", err)
	}

	repos, _ := s.WorkflowRepos(wf.ID)
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if len(names) != 2 || names[0] != "b" || names[1] != "c" {
		t.Errorf("WorkflowRepos() = %v, want [b c]", names)
	}
}

// Dropping a repo from a workflow is a change of scope, not a decision to
// discard its configuration — adding it back must not mean retyping.
func TestReplaceWorkflowReposKeepsTheVariablesOfDroppedRepos(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("g")
	a, _ := s.AddRepo(g.ID, "a", "/tmp/a", "")
	b, _ := s.AddRepo(g.ID, "b", "/tmp/b", "")
	wf, _ := s.AddWorkflow(g.ID, "local", "", []int64{a.ID, b.ID})
	s.SetWorkflowRepoVar(wf.ID, b.ID, "KEEP", "me")

	s.ReplaceWorkflowRepos(wf.ID, []int64{a.ID})
	s.ReplaceWorkflowRepos(wf.ID, []int64{a.ID, b.ID})

	vars, _ := s.WorkflowRepoVars(wf.ID, b.ID)
	if vars["KEEP"] != "me" {
		t.Errorf("variables lost when the repo left and came back: %v", vars)
	}
}
