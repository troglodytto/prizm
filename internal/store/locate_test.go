package store

import (
	"errors"
	"testing"
)

func TestRepoForPathFindsContainingRepo(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")
	s.AddRepo(g.ID, "frontend", "/code/xyz/frontend", "")

	repo, group, err := s.RepoForPath("/code/xyz/backend/src/handlers")
	if err != nil {
		t.Fatalf("RepoForPath() error = %v", err)
	}
	if repo.Name != "backend" {
		t.Errorf("repo = %q, want %q", repo.Name, "backend")
	}
	if group.Name != "XYZ" {
		t.Errorf("group = %q, want %q", group.Name, "XYZ")
	}
}

func TestRepoForPathExactMatch(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")

	repo, _, err := s.RepoForPath("/code/xyz/backend")
	if err != nil {
		t.Fatalf("RepoForPath() error = %v", err)
	}
	if repo.Name != "backend" {
		t.Errorf("repo = %q, want %q", repo.Name, "backend")
	}
}

// A repo checked out inside another repo must win over its parent.
func TestRepoForPathPrefersLongestMatch(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "monorepo", "/code/xyz", "")
	s.AddRepo(g.ID, "nested", "/code/xyz/packages/api", "")

	repo, _, err := s.RepoForPath("/code/xyz/packages/api/src")
	if err != nil {
		t.Fatalf("RepoForPath() error = %v", err)
	}
	if repo.Name != "nested" {
		t.Errorf("repo = %q, want %q — the deeper repo should win", repo.Name, "nested")
	}
}

func TestRepoForPathRespectsPathBoundaries(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")

	if _, _, err := s.RepoForPath("/code/xyz/backend-old"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoForPath() error = %v, want ErrNotFound", err)
	}
}

func TestRepoForPathOutsideEveryRepo(t *testing.T) {
	s := newTestStore(t)
	g, _ := s.CreateGroup("XYZ")
	s.AddRepo(g.ID, "backend", "/code/xyz/backend", "")

	if _, _, err := s.RepoForPath("/somewhere/else"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoForPath() error = %v, want ErrNotFound", err)
	}
}

func TestRepoForPathWithNoRepos(t *testing.T) {
	s := newTestStore(t)

	if _, _, err := s.RepoForPath("/anywhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RepoForPath() error = %v, want ErrNotFound", err)
	}
}
