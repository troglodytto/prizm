package store

import (
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestAddAndGetRepo(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	added, err := s.AddRepo(g.ID, "backend", "/home/u/code/backend", "")
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if added.EnvFile != ".env" {
		t.Errorf("EnvFile = %q, want %q (default)", added.EnvFile, ".env")
	}

	got, err := s.RepoByName(g.ID, "backend")
	if err != nil {
		t.Fatalf("RepoByName() error = %v", err)
	}
	if got.Path != "/home/u/code/backend" {
		t.Errorf("Path = %q, want %q", got.Path, "/home/u/code/backend")
	}
	if got.GroupID != g.ID {
		t.Errorf("GroupID = %d, want %d", got.GroupID, g.ID)
	}
}

func TestAddRepoCustomEnvFile(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	added, err := s.AddRepo(g.ID, "frontend", "/home/u/code/frontend", ".env.local")
	if err != nil {
		t.Fatalf("AddRepo() error = %v", err)
	}
	if added.EnvFile != ".env.local" {
		t.Errorf("EnvFile = %q, want %q", added.EnvFile, ".env.local")
	}
}

func TestAddRepoRejectsDuplicateNameInSameGroup(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.AddRepo(g.ID, "backend", "/a", ""); err != nil {
		t.Fatalf("first AddRepo() error = %v", err)
	}
	if _, err := s.AddRepo(g.ID, "backend", "/b", ""); !errors.Is(err, ErrExists) {
		t.Errorf("duplicate AddRepo() error = %v, want ErrExists", err)
	}
}

func TestAddRepoAllowsSameNameInDifferentGroups(t *testing.T) {
	s := newTestStore(t)
	g1, _ := s.CreateGroup("XYZ")
	g2, _ := s.CreateGroup("ABC")

	if _, err := s.AddRepo(g1.ID, "backend", "/a", ""); err != nil {
		t.Fatalf("AddRepo(g1) error = %v", err)
	}
	if _, err := s.AddRepo(g2.ID, "backend", "/b", ""); err != nil {
		t.Errorf("AddRepo(g2) error = %v, want nil", err)
	}
}

func TestRepoByNameMissing(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")

	if _, err := s.RepoByName(g.ID, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoByName(missing) error = %v, want ErrNotFound", err)
	}
}

func TestListReposSortedByName(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	for _, n := range []string{"frontend", "ai", "backend"} {
		s.AddRepo(g.ID, n, "/"+n, "")
	}

	repos, err := s.ListRepos(g.ID)
	if err != nil {
		t.Fatalf("ListRepos() error = %v", err)
	}

	var names []string
	for _, r := range repos {
		names = append(names, r.Name)
	}
	if diff := cmp.Diff([]string{"ai", "backend", "frontend"}, names); diff != "" {
		t.Errorf("ListRepos() names mismatch (-want +got):\n%s", diff)
	}
}

func TestRepoPathsByGroup(t *testing.T) {
	s := newTestStore(t)
	g1, _ := s.CreateGroup("XYZ")
	g2, _ := s.CreateGroup("ABC")
	s.AddRepo(g1.ID, "backend", "/code/backend", "")
	s.AddRepo(g1.ID, "frontend", "/code/frontend", "")
	s.AddRepo(g2.ID, "svc", "/other/svc", "")

	got, err := s.RepoPathsByGroup()
	if err != nil {
		t.Fatalf("RepoPathsByGroup() error = %v", err)
	}

	want := map[string][]string{
		"XYZ": {"/code/backend", "/code/frontend"},
		"ABC": {"/other/svc"},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("RepoPathsByGroup() mismatch (-want +got):\n%s", diff)
	}
}
