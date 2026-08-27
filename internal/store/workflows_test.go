package store

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAddWorkflowWithRepos(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	fe, _ := s.AddRepo(g.ID, "frontend", "/code/frontend", "")
	be, _ := s.AddRepo(g.ID, "backend", "/code/backend", "")
	s.AddRepo(g.ID, "ai", "/code/ai", "")

	wf, err := s.AddWorkflow(g.ID, "local", "local", []int64{fe.ID, be.ID})
	if err != nil {
		t.Fatalf("AddWorkflow() error = %v", err)
	}
	if wf.Tag != "local" {
		t.Errorf("Tag = %q, want %q", wf.Tag, "local")
	}

	repos, err := s.WorkflowRepos(wf.ID)
	if err != nil {
		t.Fatalf("WorkflowRepos() error = %v", err)
	}
	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if diff := cmp.Diff([]string{"backend", "frontend"}, names); diff != "" {
		t.Errorf("WorkflowRepos() mismatch (-want +got):\n%s", diff)
	}
}

func TestAddWorkflowWithNoReposIsAllowed(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	wf, err := s.AddWorkflow(g.ID, "empty", "", nil)
	if err != nil {
		t.Fatalf("AddWorkflow() error = %v", err)
	}

	repos, err := s.WorkflowRepos(wf.ID)
	if err != nil {
		t.Fatalf("WorkflowRepos() error = %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("WorkflowRepos() = %d repos, want 0", len(repos))
	}
}

func TestAddWorkflowRejectsReservedNames(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	for _, name := range []string{"up", "status", "sync", "edit", "import", "ls", "help"} {
		if _, err := s.AddWorkflow(g.ID, name, "", nil); !errors.Is(err, ErrReservedName) {
			t.Errorf("AddWorkflow(%q) error = %v, want ErrReservedName", name, err)
		}
	}
}

func TestAddWorkflowRejectsDuplicate(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.AddWorkflow(g.ID, "local", "", nil); err != nil {
		t.Fatalf("first AddWorkflow() error = %v", err)
	}
	if _, err := s.AddWorkflow(g.ID, "local", "", nil); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate AddWorkflow() error = %v, want ErrExists", err)
	}
}

func TestAddWorkflowRollsBackOnBadRepoID(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.AddWorkflow(g.ID, "local", "", []int64{9999}); err == nil {
		t.Fatal("AddWorkflow(bad repo id) error = nil, want foreign key error")
	}
	if _, err := s.WorkflowByName(g.ID, "local"); !errors.Is(err, ErrNotFound) {
		t.Error("workflow survived a failed AddWorkflow; transaction did not roll back")
	}
}

func TestWorkflowByNameMissing(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.WorkflowByName(g.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("WorkflowByName(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListWorkflowsSortedByName(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	for _, n := range []string{"production", "debug-payments", "local"} {
		s.AddWorkflow(g.ID, n, "", nil)
	}

	wfs, err := s.ListWorkflows(g.ID)
	if err != nil {
		t.Fatalf("ListWorkflows() error = %v", err)
	}
	var names []string
	for _, w := range wfs {
		names = append(names, w.Name)
	}
	if diff := cmp.Diff([]string{"debug-payments", "local", "production"}, names); diff != "" {
		t.Errorf("ListWorkflows() mismatch (-want +got):\n%s", diff)
	}
}
